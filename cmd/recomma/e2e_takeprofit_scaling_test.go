package main

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/recomma"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

// TestE2E_TakeProfitScalingHandlesRepeatedUpdates simulates the repeated
// take-profit resize scenario we observed in production. 3Commas emits two
// take-profit "Placing" events with the same fingerprint (thus identical CLOIDs)
// and the scaler must update Hyperliquid without tripping over UNIQUE
// constraints in scaled_orders. The current bug causes the second resize to be
// dropped, so this test will fail until that is fixed.
func TestE2E_TakeProfitScalingHandlesRepeatedUpdates(t *testing.T) {
	t.Parallel()

	const (
		botID         = 8801
		dealID        = 8801001
		baseCoin      = "DOGE"
		basePrice     = 0.161
		baseSize      = 155.0
		firstTPPrice  = 0.171
		secondTPPrice = 0.182
	)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)

	harness := NewE2ETestHarness(t, ctx)
	t.Cleanup(harness.Shutdown)

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(botID, "TP Scaling Bot", 4200, true))

	deal := threecommasmock.NewDeal(dealID, botID, "USDT_"+baseCoin, "active")
	addBaseOrderEvent(&deal, baseCoin, basePrice, baseSize)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal))

	harness.Start(ctx)

	// Submit the base order so we can fill it before any take-profit sizing.
	harness.TriggerDealProduction(ctx)
	harness.WaitForDealProcessing(uint32(dealID), 5*time.Second)
	harness.WaitForOrderInDatabase(15 * time.Second)

	baseOrder := waitForOrderByType(t, harness, ctx, tc.MarketOrderDealOrderTypeBase, 5*time.Second)
	recordFillForOrder(t, harness, ctx, baseOrder, basePrice)

	// Append the first take-profit event now that the position is long.
	appendTakeProfitEvent(t, harness, dealID, baseCoin, firstTPPrice, baseSize)
	harness.TriggerDealProduction(ctx)

	assertTakeProfitSubmissionPrice(t, harness, ctx, firstTPPrice, 15*time.Second)

	// Emit another take-profit event with the same fingerprint but a new price.
	appendTakeProfitEvent(t, harness, dealID, baseCoin, secondTPPrice, baseSize)
	harness.TriggerDealProduction(ctx)

	// Expect storage submissions to reflect the latest take-profit size/price.
	// The bug leaves the submission stuck at the original price, so this check
	// fails until scaling handles repeats correctly.
	assertTakeProfitSubmissionPrice(t, harness, ctx, secondTPPrice, 15*time.Second)
}

func waitForOrderByType(
	t *testing.T,
	harness *E2ETestHarness,
	ctx context.Context,
	orderType tc.MarketOrderDealOrderType,
	timeout time.Duration,
) api.OrderItem {
	t.Helper()

	var record api.OrderItem
	require.Eventuallyf(t, func() bool {
		orders, _, err := harness.Store.ListOrders(ctx, api.ListOrdersOptions{})
		if err != nil {
			return false
		}
		for _, candidate := range orders {
			if candidate.BotEvent != nil && candidate.BotEvent.OrderType == orderType {
				record = candidate
				return true
			}
		}
		return false
	}, timeout, 100*time.Millisecond, "waiting for %s order", orderType)

	return record
}

func appendTakeProfitEvent(
	t *testing.T,
	harness *E2ETestHarness,
	dealID int,
	coin string,
	price float64,
	baseSize float64,
) {
	t.Helper()

	quoteVolume := price * baseSize
	msg := fmt.Sprintf(
		"Placing TakeProfit trade.  Price: %.5f USDT Size: %.5f USDT (%.5f %s), the price should rise for 2.00%% to close the trade",
		price,
		quoteVolume,
		baseSize,
		coin,
	)

	require.NoError(t, harness.ThreeCommasMock.AddBotEventToDeal(dealID, msg))
}

func recordFillForOrder(t *testing.T, harness *E2ETestHarness, ctx context.Context, order api.OrderItem, price float64) {
	t.Helper()

	require.NotNil(t, order.BotEvent, "expected bot event details")
	side := "B"
	if order.BotEvent.Type == tc.SELL {
		side = "A"
	}

	ident := recomma.NewOrderIdentifier(recomma.VenueID(harness.VenueID), harness.HLWallet, order.OrderId)
	now := time.Now().UnixMilli()
	cloid := order.OrderId.Hex()

	status := hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      order.BotEvent.Coin,
			Side:      side,
			LimitPx:   fmt.Sprintf("%.5f", price),
			Sz:        "0",
			Oid:       now,
			Timestamp: now,
			OrigSz:    fmt.Sprintf("%.5f", order.BotEvent.Size),
			Cloid:     &cloid,
		},
		Status:          "filled",
		StatusTimestamp: now,
	}

	require.NoError(t, harness.Store.RecordHyperliquidStatus(ctx, ident, status))
	require.NoError(t, harness.App.FillTracker.UpdateStatus(ctx, ident, status))
}

func assertTakeProfitSubmissionPrice(
	t *testing.T,
	harness *E2ETestHarness,
	ctx context.Context,
	expected float64,
	timeout time.Duration,
) {
	t.Helper()

	require.Eventuallyf(t, func() bool {
		order, ok := findOrderByType(harness, ctx, tc.MarketOrderDealOrderTypeTakeProfit)
		if !ok {
			return false
		}
		price, ok := submissionPrice(order.LatestSubmission)
		if !ok {
			return false
		}
		return math.Abs(price-expected) < 1e-6
	}, timeout, 200*time.Millisecond, "waiting for take-profit submission to reach %.5f", expected)
}

func findOrderByType(
	harness *E2ETestHarness,
	ctx context.Context,
	orderType tc.MarketOrderDealOrderType,
) (api.OrderItem, bool) {
	orders, _, err := harness.Store.ListOrders(ctx, api.ListOrdersOptions{})
	if err != nil {
		return api.OrderItem{}, false
	}
	for _, candidate := range orders {
		if candidate.BotEvent != nil && candidate.BotEvent.OrderType == orderType {
			return candidate, true
		}
	}
	return api.OrderItem{}, false
}

func submissionPrice(submission interface{}) (float64, bool) {
	switch req := submission.(type) {
	case *hyperliquid.CreateOrderRequest:
		return req.Price, true
	case *hyperliquid.ModifyOrderRequest:
		return req.Order.Price, true
	default:
		return 0, false
	}
}
