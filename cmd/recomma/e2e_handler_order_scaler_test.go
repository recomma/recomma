package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/stretchr/testify/require"
)

// TestE2E_OrderScalerAPIClampsMultiplier exercises the handler layer for
// /api/orders/scalers to ensure the effective multiplier reported to the UI
// respects the configured max even when the underlying override exceeds it.
func TestE2E_OrderScalerAPIClampsMultiplier(t *testing.T) {
	t.Parallel()

	const (
		botID  = 9801
		dealID = 9801001
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	harness := NewE2ETestHarness(t, ctx)
	defer harness.Shutdown()

	now := time.Now()
	require.NoError(t, harness.Store.RecordBot(ctx, tc.Bot{Id: botID}, now))

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(botID, "OrderScaler API Bot", 4444, true))
	deal := threecommasmock.NewDeal(dealID, botID, "USDT_BTC", "active")
	addBaseOrderEvent(&deal, "BTC", 51000.0, 0.002)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal))

	// Configure an override above the enforced max to verify the response clamps it.
	maxMultiplier := harness.App.Config.OrderScalerMaxMultiplier
	override := maxMultiplier * 2
	_, err := harness.Store.UpsertBotOrderScaler(ctx, botID, &override, nil, "e2e-handler")
	require.NoError(t, err)

	harness.Start(ctx)
	harness.TriggerDealProduction(ctx)
	harness.WaitForDealProcessing(dealID, 5*time.Second)
	harness.WaitForOrderInDatabase(5 * time.Second)

	resp := harness.APIGet(fmt.Sprintf("/api/orders/scalers?bot_id=%d", botID))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload api.ListOrderScalers200JSONResponse
	decodeResponse(t, resp.Body, &payload)

	require.NotEmpty(t, payload.Items, "expected order scaler entries")
	require.Nil(t, payload.NextPageToken)

	record := payload.Items[0]
	require.Equal(t, "e2e-handler", record.Actor)
	require.Equal(t, api.BotOverride, record.Config.Source)
	require.InDelta(t, maxMultiplier, record.Config.Multiplier, 1e-9, "effective multiplier must be clamped")

	if record.Config.Override == nil || record.Config.Override.Multiplier == nil {
		t.Fatalf("expected override details to be present: %#v", record.Config)
	}
	require.Greater(t, *record.Config.Override.Multiplier, record.Config.Multiplier, "raw override should remain above the max cap")
	require.Equal(t, api.BotOverride, record.Config.Source)
}
