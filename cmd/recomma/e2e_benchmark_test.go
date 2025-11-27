package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/recomma"
	"github.com/stretchr/testify/require"
)

type benchmarkDealSpec struct {
	id    uint32
	botID uint32
	coin  string
	price float64
	size  float64
}

func BenchmarkMultiVenueLoad(b *testing.B) {
	if testing.Short() {
		b.Skip("e2e benchmarks skipped in short mode")
	}

	venueCounts := []int{5, 10, 15}
	loadTiers := []struct {
		name        string
		bots        int
		dealsPerBot int
	}{{
		name:        "bots100_deals50",
		bots:        100,
		dealsPerBot: 50,
	}}

	for _, tier := range loadTiers {
		for _, venueCount := range venueCounts {
			tier := tier
			venueCount := venueCount
			b.Run(fmt.Sprintf("%s/%d-venues", tier.name, venueCount), func(b *testing.B) {
				b.Helper()

				totalDuration := time.Duration(0)
				var totalOrders int64
				var totalErrors int64
				dealsPerIteration := tier.bots * tier.dealsPerBot

				for iter := 0; iter < b.N; iter++ {
					b.StopTimer()
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

					opts := make([]E2EHarnessOption, 0, venueCount-1)
					for i := 1; i < venueCount; i++ {
						opts = append(opts, WithAdditionalHyperliquidVenue(
							fmt.Sprintf("hyperliquid:%02d", i),
							fmt.Sprintf("Hyperliquid %02d", i),
						))
					}

					harness := NewE2ETestHarness(b, ctx, opts...)
					harness.ThreeCommasMock.AllowDuplicateIDs(true)

					deals := seedBenchmarkDeals(b, ctx, harness, tier.bots, tier.dealsPerBot)

					harness.Start(ctx)
					b.StartTimer()

					iterDuration, progressed, errs := runBenchmarkIteration(b, ctx, harness, deals)
					totalDuration += iterDuration
					atomic.AddInt64(&totalOrders, int64(progressed))
					atomic.AddInt64(&totalErrors, int64(errs))

					b.StopTimer()
					harness.Shutdown()
					cancel()
				}

				if totalDuration > 0 {
					durationSeconds := totalDuration.Seconds()
					b.ReportMetric(float64(totalOrders)/durationSeconds, "orders/sec")
					b.ReportMetric(float64(dealsPerIteration*b.N)/durationSeconds, "deals/sec")
					b.ReportMetric(float64(totalErrors), "errors")
				}
			})
		}
	}
}

func seedBenchmarkDeals(b *testing.B, ctx context.Context, harness *E2ETestHarness, bots, dealsPerBot int) []benchmarkDealSpec {
	b.Helper()

	deals := make([]benchmarkDealSpec, 0, bots*dealsPerBot)
	coins := []string{"BTC", "ETH", "SOL", "DOGE", "XRP"}

	for bot := 0; bot < bots; bot++ {
		botID := uint32(bot + 1)
		harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(int(botID), fmt.Sprintf("Benchmark Bot %d", botID), 1000+bot, true))
		require.NoError(b, harness.Store.RecordBot(ctx, tc.Bot{Id: int(botID)}, time.Now()))

		// Ensure each bot is assigned to every venue so submissions spread across mocks.
		require.NoError(b, harness.Store.UpsertBotVenueAssignment(ctx, botID, recomma.VenueID(harness.VenueID), true))
		for venueID := range harness.AdditionalVenues {
			require.NoError(b, harness.Store.UpsertBotVenueAssignment(ctx, botID, recomma.VenueID(venueID), false))
		}

		coin := coins[bot%len(coins)]
		basePrice := 100.0 + float64(bot)
		baseSize := 0.01 + float64(bot%3)*0.005

		for dealIdx := 0; dealIdx < dealsPerBot; dealIdx++ {
			dealID := uint32(bot*dealsPerBot + dealIdx + 1)
			deal := threecommasmock.NewDeal(int(dealID), int(botID), fmt.Sprintf("USDT_%s", coin), "active")
			addBaseOrderEvent(&deal, coin, basePrice, baseSize)
			require.NoError(b, harness.ThreeCommasMock.AddDeal(deal))

			deals = append(deals, benchmarkDealSpec{
				id:    dealID,
				botID: botID,
				coin:  coin,
				price: basePrice,
				size:  baseSize,
			})
		}
	}

	return deals
}

func runBenchmarkIteration(b *testing.B, ctx context.Context, harness *E2ETestHarness, deals []benchmarkDealSpec) (time.Duration, int, int) {
	b.Helper()

	var errCount atomic.Int64
	workerCount := runtime.GOMAXPROCS(0)
	dealCh := make(chan benchmarkDealSpec)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for deal := range dealCh {
				// Add another bot event to fan-out work and ensure per-deal ordering is exercised.
				price := deal.price + float64(worker)*0.1
				if err := harness.ThreeCommasMock.AddBotEventToDeal(int(deal.id), baseOrderEventMessage(deal.coin, price, deal.size)); err != nil {
					errCount.Add(1)
					continue
				}
				harness.TriggerDealProduction(ctx)
			}
		}(i)
	}

	for _, deal := range deals {
		dealCh <- deal
	}
	close(dealCh)
	wg.Wait()

	expectedOrders := len(deals)
	require.Eventually(b, func() bool {
		orders, _, err := harness.Store.ListOrders(ctx, api.ListOrdersOptions{})
		if err != nil {
			return false
		}
		return len(orders) >= expectedOrders
	}, 30*time.Second, 200*time.Millisecond, "orders did not reach expected volume")

	progressed := progressHyperliquidOrders(b, harness)
	return time.Since(start), progressed, int(errCount.Load())
}

func progressHyperliquidOrders(b *testing.B, harness *E2ETestHarness) int {
	b.Helper()

	reqs := harness.HyperliquidMock.GetExchangeRequests()
	state := harness.HyperliquidMock.State()
	progressed := 0

	for idx, req := range reqs {
		for _, order := range extractOrdersFromAction(req.Action) {
			cloid := firstString(order, "cloid", "c")
			if cloid == "" {
				continue
			}

			price := 0.0
			if px, ok := parseNumeric(firstString(order, "limitPx", "p")); ok {
				price = px
			}
			if price == 0 {
				price = 1.0
			}

			if idx%2 == 0 {
				if err := harness.HyperliquidMock.FillOrder(cloid, price); err == nil {
					progressed++
				}
				continue
			}

			if state != nil && state.CancelOrder(cloid) {
				progressed++
			}
		}
	}

	return progressed
}
