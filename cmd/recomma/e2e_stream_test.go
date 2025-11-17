package main

import (
	"context"
	"strings"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

// TestE2E_OrderStreamPublishesLifecycleEvents exercises the /api/orders/stream SSE
// endpoint to ensure it publishes sequence-ordered events filtered to the target deal.
func TestE2E_OrderStreamPublishesLifecycleEvents(t *testing.T) {
	t.Parallel()

	const (
		targetBotID  = 701
		noiseBotID   = 702
		targetDealID = 11001
		noiseDealID  = 22002
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	harness := NewE2ETestHarness(t, ctx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	now := time.Now()
	require.NoError(t, harness.Store.RecordBot(ctx, tc.Bot{Id: targetBotID}, now))
	require.NoError(t, harness.Store.RecordBot(ctx, tc.Bot{Id: noiseBotID}, now))

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(targetBotID, "Order Stream Target", 9001, true))
	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(noiseBotID, "Order Stream Noise", 9002, true))

	target := threecommasmock.NewDeal(targetDealID, targetBotID, "USDT_BTC", "active")
	addBaseOrderEvent(&target, "BTC", 50000.0, 0.0025)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(target))

	noise := threecommasmock.NewDeal(noiseDealID, noiseBotID, "USDT_ETH", "active")
	addBaseOrderEvent(&noise, "ETH", 3500.0, 0.25)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(noise))

	harness.Start(ctx)

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	eventsCh, err := harness.App.StreamController.Subscribe(streamCtx, api.StreamFilter{})
	require.NoError(t, err)

	harness.TriggerDealProduction(ctx)
	harness.WaitForDealProcessing(targetDealID, 5*time.Second)
	harness.WaitForDealProcessing(noiseDealID, 5*time.Second)
	harness.WaitForOrderInDatabase(5 * time.Second)

	var (
		lastSeq        int64 = -1
		sequenceSeen   bool
		gotSubmission  bool
		gotStatus      bool
		streamCanceled bool
	)

	require.Eventually(t, func() bool {
		select {
		case evt, ok := <-eventsCh:
			if !ok {
				streamCanceled = true
				return false
			}
			if evt.OrderID.DealID != uint32(targetDealID) {
				return gotSubmission && gotStatus
			}

			if evt.Sequence == nil {
				require.FailNowf(t, "stream event missing sequence number", "type=%s", evt.Type)
			}
			if sequenceSeen {
				require.Greater(t, *evt.Sequence, lastSeq, "sequence numbers must be strictly increasing")
			}
			lastSeq = *evt.Sequence
			sequenceSeen = true

			switch evt.Type {
			case api.HyperliquidSubmission:
				create, ok := evt.Submission.(hyperliquid.CreateOrderRequest)
				require.True(t, ok, "expected create submission")
				require.Equal(t, "BTC", strings.ToUpper(create.Coin))
				gotSubmission = true
			case api.HyperliquidStatus:
				require.NotNil(t, evt.Status)
				require.NotNil(t, evt.Status.Order.Cloid)
				require.Equal(t, "BTC", strings.ToUpper(evt.Status.Order.Coin))
				gotStatus = true
			default:
				// Ignore unrelated events.
			}
		default:
		}
		return gotSubmission && gotStatus
	}, 10*time.Second, 50*time.Millisecond, "timed out waiting for stream events")

	require.False(t, streamCanceled, "stream closed before required events arrived")
}
