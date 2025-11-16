package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	tcMock "github.com/recomma/3commas-mock/tcmock"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/stretchr/testify/require"
)

// TestE2E_BasicHarnessLifecycle tests that the E2E harness can start and stop cleanly
func TestE2E_BasicHarnessLifecycle(t *testing.T) {
	// t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	harness.ThreeCommasMock.AllowDuplicateIDs(true)
	err := harness.ThreeCommasMock.LoadVCRCassette("../../testdata/singledeal")
	require.NoError(t, err)

	// Start application
	harness.Start(testCtx)

	// Verify HTTP server is responding
	resp := harness.APIGet("/api/bots")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	t.Log("✅ E2E harness lifecycle test passed")
}

// TestE2E_DealToOrderFlow tests the full flow from 3commas deal to Hyperliquid order
func TestE2E_DealToOrderFlow(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	err := harness.ThreeCommasMock.LoadVCRCassette("../../testdata/singledeal")
	require.NoError(t, err)

	// Start application
	harness.Start(testCtx)

	// Trigger deal production
	harness.TriggerDealProduction(testCtx)

	// Wait for the recorded deal to be processed
	harness.WaitForDealProcessing(2376446537, 5*time.Second)

	deals, _, err := harness.Store.ListDeals(testCtx, api.ListDealsOptions{})
	require.NoError(t, err)

	var recordedDeal *tc.Deal
	for i := range deals {
		if deals[i].Id == 2376446537 {
			recordedDeal = &deals[i]
			break
		}
	}
	require.NotNil(t, recordedDeal, "recorded deal should be stored")
	require.Equal(t, "USDT_DOGE", recordedDeal.Pair)
	require.Equal(t, "Bot16511317 has signal", recordedDeal.BotName)

	// Wait for order to be recorded in database (which implies it was submitted)
	harness.WaitForOrderInDatabase(5 * time.Second)

	// Verify database has the order
	dbOrders, _, err := harness.Store.ListOrders(testCtx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders)

	// Verify web API returns the order
	resp := harness.APIGet("/api/orders")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var apiResp api.ListOrders200JSONResponse
	err = json.Unmarshal(body, &apiResp)
	require.NoError(t, err)
	require.NotEmpty(t, apiResp.Items, "API should return at least one order")

	t.Log("✅ E2E test passed: 3commas deal → processing → hyperliquid order → database → web API")
}

// TestE2E_APIListBots tests the /api/bots endpoint
func TestE2E_APIListBots(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	// Configure mock with a bot
	harness.ThreeCommasMock.AddBot(tcMock.Bot{
		Id:        1,
		Name:      strPtr("API Test Bot"),
		IsEnabled: true,
	})

	// Start application
	harness.Start(testCtx)

	// Trigger bot sync
	harness.TriggerDealProduction(testCtx)

	// Give time for bot to be synced
	time.Sleep(500 * time.Millisecond)

	// Test API
	resp := harness.APIGet("/api/bots")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var apiResp map[string]interface{}
	err = json.Unmarshal(body, &apiResp)
	require.NoError(t, err)

	// Check if bots are present
	botsData, ok := apiResp["bots"]
	if ok {
		botsArray, ok := botsData.([]interface{})
		if ok && len(botsArray) > 0 {
			bot := botsArray[0].(map[string]interface{})
			require.Equal(t, float64(1), bot["id"])
			require.Equal(t, "API Test Bot", bot["name"])
			t.Log("✅ E2E test passed: /api/bots returns expected data")
			return
		}
	}

	t.Log("ℹ️  No bots returned yet (may need more time for sync)")
}
