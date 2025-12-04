package main

import (
	"context"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/stretchr/testify/require"
)

// TestE2E_TakeProfitRequiresBaseFill reproduces the regression from
// bug_2025-11-29_reduce_only_takeprofit. The expectation is that we do not
// submit reduce-only take-profits until the mirrored base position exists. The
// current implementation emits the TP immediately and Hyperliquid rejects it
// with `reduceOnlyRejected`, so this test should fail until that behavior is
// fixed.
func TestE2E_TakeProfitRequiresBaseFill(t *testing.T) {
	t.Parallel()

	const (
		botID    = 9901
		dealID   = 9901001
		baseCoin = "DOGE"
		baseSize = 131.0
		basePx   = 0.15030
		tpPx     = 0.15196
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	harness := NewE2ETestHarness(t, ctx)
	t.Cleanup(harness.Shutdown)

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(botID, "Reduce-Only TP Bot", 4201, true))

	deal := threecommasmock.NewDeal(dealID, botID, "USDT_"+baseCoin, "active")
	addBaseOrderEvent(&deal, baseCoin, basePx, baseSize)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal))

	harness.Start(ctx)

	// Produce the base order but intentionally skip filling it.
	harness.TriggerDealProduction(ctx)
	harness.WaitForDealProcessing(uint32(dealID), 5*time.Second)
	harness.WaitForOrderInDatabase(5 * time.Second)
	harness.WaitForOrderQueueIdle(5 * time.Second)

	// 3Commas now announces the take-profit while the base is still unfilled.
	appendTakeProfitEvent(t, harness, dealID, baseCoin, tpPx, baseSize)
	harness.TriggerDealProduction(ctx)
	harness.WaitForOrderQueueIdle(5 * time.Second)

	// Until the base order is filled, the mirrored venue should not have any
	// take-profit order recorded. The bug causes a take-profit submission here,
	// so this assertion fails today.
	require.Never(t, func() bool {
		_, ok := findOrderByType(harness, ctx, tc.MarketOrderDealOrderTypeTakeProfit)
		return ok
	}, 2*time.Second, 100*time.Millisecond, "expected no take-profit submissions before base fill")

	// Once the base actually fills, emitting the take-profit is valid; make
	// sure the rest of the pipeline still works when we append another TP
	// event.
	baseOrder := waitForOrderByType(t, harness, ctx, tc.MarketOrderDealOrderTypeBase, 5*time.Second)
	recordFillForOrder(t, harness, ctx, baseOrder, basePx)

	appendTakeProfitEvent(t, harness, dealID, baseCoin, tpPx, baseSize)
	harness.TriggerDealProduction(ctx)
	harness.WaitForOrderQueueIdle(5 * time.Second)

	require.Eventually(t, func() bool {
		_, ok := findOrderByType(harness, ctx, tc.MarketOrderDealOrderTypeTakeProfit)
		return ok
	}, 5*time.Second, 100*time.Millisecond, "expected take-profit submission after base fill")
}
