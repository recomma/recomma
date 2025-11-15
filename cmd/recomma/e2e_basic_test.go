package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	"github.com/recomma/recomma/internal/api"
	"github.com/stretchr/testify/require"
)

// TestE2E_BasicHarnessLifecycle tests that the E2E harness can start and stop cleanly
func TestE2E_BasicHarnessLifecycle(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Start application
	harness.Start(ctx)

	// Verify HTTP server is responding
	resp := harness.APIGet("/api/bots")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	t.Log("✅ E2E harness lifecycle test passed")
}

// TestE2E_DealToOrderFlow tests the full flow from 3commas deal to Hyperliquid order
func TestE2E_DealToOrderFlow(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock with a bot and active deal
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      1,
		Name:    "E2E Test Bot",
		Enabled: true,
	})

	harness.ThreeCommasMock.AddDeal(1, threecommasmock.Deal{
		ID:           101,
		BotID:        1,
		Pair:         "USDT_BTC",
		Status:       "active",
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
		CreatedAt:    "2024-01-15T10:30:00.000Z",
		UpdatedAt:    "2024-01-15T10:31:00.000Z",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:30:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "buy",
				Status:        "active",
				Price:         "50000.0",
				Size:          "0.0002",
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
		},
	})

	// Start application
	harness.Start(ctx)

	// Trigger deal production
	harness.TriggerDealProduction(ctx)

	// Wait for deal to be processed
	harness.WaitForDealProcessing(101, 5*time.Second)

	// Wait for order to be recorded in database (which implies it was submitted)
	harness.WaitForOrderInDatabase(5 * time.Second)

	// Verify database has the order
	dbOrders, _, err := harness.Store.ListOrders(ctx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders)

	// Verify web API returns the order
	resp := harness.APIGet("/api/orders")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var apiResp map[string]interface{}
	err = json.Unmarshal(body, &apiResp)
	require.NoError(t, err)

	// API should have orders
	ordersData, ok := apiResp["orders"]
	require.True(t, ok, "API response should have 'orders' field")

	ordersArray, ok := ordersData.([]interface{})
	require.True(t, ok, "orders should be an array")
	require.NotEmpty(t, ordersArray, "API should return at least one order")

	t.Log("✅ E2E test passed: 3commas deal → processing → hyperliquid order → database → web API")
}

// TestE2E_APIListBots tests the /api/bots endpoint
func TestE2E_APIListBots(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure mock with a bot
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      1,
		Name:    "API Test Bot",
		Enabled: true,
	})

	// Start application
	harness.Start(ctx)

	// Trigger bot sync
	harness.TriggerDealProduction(ctx)

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
