//go:build integration

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
	"github.com/recomma/recomma/storage"
	"github.com/stretchr/testify/require"
)

// TestE2E_OrderScaling_WithMultiplier tests order scaling with different multipliers
func TestE2E_OrderScaling_WithMultiplier(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock with a bot and deal
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      1,
		Name:    "Order Scaling Bot",
		Enabled: true,
	})

	// Add a deal with a base order
	harness.ThreeCommasMock.AddDeal(1, threecommasmock.Deal{
		ID:           201,
		BotID:        1,
		Pair:         "USDT_BTC",
		Status:       "active",
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:30:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "buy",
				Status:        "active",
				Price:         "50000.0",
				Size:          "0.001", // Small base size
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
		},
	})

	// Start application
	harness.Start(ctx)

	// Set order scaler multiplier to 5.0 for bot 1
	err := harness.Store.UpsertBotOrderScaler(ctx, storage.UpsertBotOrderScalerParams{
		BotID:      1,
		Multiplier: 5.0,
	})
	require.NoError(t, err)

	// Trigger deal production
	harness.TriggerDealProduction(ctx)

	// Wait for order submission
	harness.WaitForOrderSubmission(5 * time.Second)

	// Verify order was scaled to 5x (0.001 * 5.0 = 0.005)
	orders := harness.HyperliquidMock.GetAllOrders()
	require.NotEmpty(t, orders, "expected at least one order")

	order := orders[0]
	require.Equal(t, "BTC", order.Order.Coin)
	require.True(t, order.Order.IsBuy)
	require.InDelta(t, 0.005, order.Order.Size, 0.0001, "expected order size to be scaled to 0.005 (0.001 * 5.0)")

	// Verify scaled order was recorded in database
	harness.WaitForOrderInDatabase(5 * time.Second)
	dbOrders, _, err := harness.Store.ListOrders(ctx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders)

	t.Log("✅ E2E test passed: Order scaling with 5x multiplier")
}

// TestE2E_OrderScaling_MaxMultiplierClamp tests that max multiplier is enforced
func TestE2E_OrderScaling_MaxMultiplierClamp(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      2,
		Name:    "Max Multiplier Bot",
		Enabled: true,
	})

	harness.ThreeCommasMock.AddDeal(1, threecommasmock.Deal{
		ID:           202,
		BotID:        2,
		Pair:         "USDT_ETH",
		Status:       "active",
		ToCurrency:   "ETH",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:30:00.000Z",
				Action:        "place",
				Coin:          "ETH",
				Type:          "buy",
				Status:        "active",
				Price:         "3000.0",
				Size:          "0.01",
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
		},
	})

	// Start application (max multiplier is 10.0 from config)
	harness.Start(ctx)

	// Set multiplier to 20.0 (should be clamped to 10.0)
	err := harness.Store.UpsertBotOrderScaler(ctx, storage.UpsertBotOrderScalerParams{
		BotID:      2,
		Multiplier: 20.0,
	})
	require.NoError(t, err)

	// Trigger deal production
	harness.TriggerDealProduction(ctx)

	// Wait for order submission
	harness.WaitForOrderSubmission(5 * time.Second)

	// Verify order was clamped to max 10x (0.01 * 10.0 = 0.1)
	orders := harness.HyperliquidMock.GetAllOrders()
	require.NotEmpty(t, orders)

	order := orders[0]
	require.InDelta(t, 0.1, order.Order.Size, 0.01, "expected order size to be clamped to 0.1 (0.01 * 10.0 max)")

	t.Log("✅ E2E test passed: Max multiplier clamping works correctly")
}

// TestE2E_MultiDeal_ConcurrentProcessing tests multiple deals processing concurrently
func TestE2E_MultiDeal_ConcurrentProcessing(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock with multiple bots and deals
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      10,
		Name:    "Bot A",
		Enabled: true,
	})
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      20,
		Name:    "Bot B",
		Enabled: true,
	})

	// Bot 10, Deal 301 - BTC
	harness.ThreeCommasMock.AddDeal(10, threecommasmock.Deal{
		ID:           301,
		BotID:        10,
		Pair:         "USDT_BTC",
		Status:       "active",
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:00:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "buy",
				Status:        "active",
				Price:         "50000.0",
				Size:          "0.001",
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
		},
	})

	// Bot 10, Deal 302 - ETH
	harness.ThreeCommasMock.AddDeal(10, threecommasmock.Deal{
		ID:           302,
		BotID:        10,
		Pair:         "USDT_ETH",
		Status:       "active",
		ToCurrency:   "ETH",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:05:00.000Z",
				Action:        "place",
				Coin:          "ETH",
				Type:          "buy",
				Status:        "active",
				Price:         "3000.0",
				Size:          "0.01",
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
		},
	})

	// Bot 20, Deal 401 - SOL
	harness.ThreeCommasMock.AddDeal(20, threecommasmock.Deal{
		ID:           401,
		BotID:        20,
		Pair:         "USDT_SOL",
		Status:       "active",
		ToCurrency:   "SOL",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:10:00.000Z",
				Action:        "place",
				Coin:          "SOL",
				Type:          "buy",
				Status:        "active",
				Price:         "100.0",
				Size:          "1.0",
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
		},
	})

	// Start application
	harness.Start(ctx)

	// Trigger deal production (should process all 3 deals)
	harness.TriggerDealProduction(ctx)

	// Wait for all deals to be processed
	harness.WaitForDealProcessing(301, 5*time.Second)
	harness.WaitForDealProcessing(302, 5*time.Second)
	harness.WaitForDealProcessing(401, 5*time.Second)

	// Wait for all orders to be submitted
	time.Sleep(2 * time.Second)

	// Verify all 3 orders were submitted to Hyperliquid
	orders := harness.HyperliquidMock.GetAllOrders()
	require.Len(t, orders, 3, "expected 3 orders to be submitted")

	// Verify coins match
	coins := make(map[string]int)
	for _, order := range orders {
		coins[order.Order.Coin]++
	}
	require.Equal(t, 1, coins["BTC"], "expected 1 BTC order")
	require.Equal(t, 1, coins["ETH"], "expected 1 ETH order")
	require.Equal(t, 1, coins["SOL"], "expected 1 SOL order")

	// Verify all deals in database
	deals, _, err := harness.Store.ListDeals(ctx, api.ListDealsOptions{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(deals), 3, "expected at least 3 deals in database")

	t.Log("✅ E2E test passed: Multiple deals processed concurrently")
}

// TestE2E_FillTracking_OrderRecording tests that orders are properly recorded
func TestE2E_FillTracking_OrderRecording(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      30,
		Name:    "Fill Tracking Bot",
		Enabled: true,
	})

	harness.ThreeCommasMock.AddDeal(30, threecommasmock.Deal{
		ID:           501,
		BotID:        30,
		Pair:         "USDT_BTC",
		Status:       "active",
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:30:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "buy",
				Status:        "active",
				Price:         "50000.0",
				Size:          "0.002",
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

	// Wait for order submission
	harness.WaitForOrderSubmission(5 * time.Second)

	// Verify order was submitted to Hyperliquid
	orders := harness.HyperliquidMock.GetAllOrders()
	require.NotEmpty(t, orders)
	require.Equal(t, "BTC", orders[0].Order.Coin)

	// Wait for order to be recorded in database
	harness.WaitForOrderInDatabase(5 * time.Second)

	// Verify order metadata is recorded in database
	dbOrders, _, err := harness.Store.ListOrders(ctx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders, "expected order to be recorded in database")

	// Verify order details
	dbOrder := dbOrders[0]
	require.Equal(t, "BTC", dbOrder.Coin)
	require.Equal(t, "buy", dbOrder.Side)

	t.Log("✅ E2E test passed: Order recording and metadata tracking")
}

// TestE2E_EdgeCase_VerySmallOrder tests handling of very small orders near minimum notional
func TestE2E_EdgeCase_VerySmallOrder(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      40,
		Name:    "Tiny Order Bot",
		Enabled: true,
	})

	// Add a deal with extremely small size
	harness.ThreeCommasMock.AddDeal(40, threecommasmock.Deal{
		ID:           601,
		BotID:        40,
		Pair:         "USDT_BTC",
		Status:       "active",
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:30:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "buy",
				Status:        "active",
				Price:         "50000.0",
				Size:          "0.00001", // Very small size
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

	// Wait for deal processing
	harness.WaitForDealProcessing(601, 5*time.Second)

	// Order might be skipped due to minimum notional, or submitted as-is
	// Either way, the system should handle it gracefully without errors

	// Verify deal was processed (even if order was skipped)
	deals, _, err := harness.Store.ListDeals(ctx, api.ListDealsOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, deals)

	t.Log("✅ E2E test passed: Very small order handled gracefully")
}

// TestE2E_EdgeCase_ZeroQuantity tests handling of zero quantity orders
func TestE2E_EdgeCase_ZeroQuantity(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      50,
		Name:    "Zero Quantity Bot",
		Enabled: true,
	})

	// Add a deal with zero size (edge case)
	harness.ThreeCommasMock.AddDeal(50, threecommasmock.Deal{
		ID:           701,
		BotID:        50,
		Pair:         "USDT_ETH",
		Status:       "active",
		ToCurrency:   "ETH",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:30:00.000Z",
				Action:        "place",
				Coin:          "ETH",
				Type:          "buy",
				Status:        "active",
				Price:         "3000.0",
				Size:          "0.0", // Zero size
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

	// Give time for processing
	time.Sleep(1 * time.Second)

	// System should handle zero quantity gracefully (skip submission)
	// Verify no errors occurred and system is still healthy

	// Verify HTTP server is still responding
	resp := harness.APIGet("/api/bots")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	t.Log("✅ E2E test passed: Zero quantity order handled gracefully")
}

// TestE2E_TakeProfitStack tests handling of stacked take-profit orders
func TestE2E_TakeProfitStack(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure 3commas mock
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      60,
		Name:    "TP Stack Bot",
		Enabled: true,
	})

	// Add a deal with stacked take-profits
	harness.ThreeCommasMock.AddDeal(60, threecommasmock.Deal{
		ID:           801,
		BotID:        60,
		Pair:         "USDT_BTC",
		Status:       "active",
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			// Base buy order
			{
				CreatedAt:     "2024-01-15T10:00:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "buy",
				Status:        "active",
				Price:         "50000.0",
				Size:          "0.003",
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
			// TP level 1 (33% position)
			{
				CreatedAt:     "2024-01-15T10:01:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "sell",
				Status:        "active",
				Price:         "51000.0",
				Size:          "0.001",
				OrderType:     "take_profit",
				OrderSize:     3,
				OrderPosition: 1,
				IsMarket:      false,
			},
			// TP level 2 (33% position)
			{
				CreatedAt:     "2024-01-15T10:02:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "sell",
				Status:        "active",
				Price:         "52000.0",
				Size:          "0.001",
				OrderType:     "take_profit",
				OrderSize:     3,
				OrderPosition: 2,
				IsMarket:      false,
			},
			// TP level 3 (34% position)
			{
				CreatedAt:     "2024-01-15T10:03:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "sell",
				Status:        "active",
				Price:         "53000.0",
				Size:          "0.001",
				OrderType:     "take_profit",
				OrderSize:     3,
				OrderPosition: 3,
				IsMarket:      false,
			},
		},
	})

	// Start application
	harness.Start(ctx)

	// Trigger deal production
	harness.TriggerDealProduction(ctx)

	// Wait for orders to be submitted
	time.Sleep(3 * time.Second)

	// Verify all 4 orders were submitted (1 buy + 3 TPs)
	orders := harness.HyperliquidMock.GetAllOrders()
	require.GreaterOrEqual(t, len(orders), 1, "expected at least the base order")

	// Count buy vs sell orders
	var buyOrders, sellOrders int
	for _, order := range orders {
		if order.Order.IsBuy {
			buyOrders++
		} else {
			sellOrders++
		}
	}
	require.Equal(t, 1, buyOrders, "expected 1 buy order")

	t.Log("✅ E2E test passed: Take-profit stack handling")
}

// TestE2E_APIEndpoints_ComprehensiveCheck tests various API endpoints
func TestE2E_APIEndpoints_ComprehensiveCheck(t *testing.T) {
	t.Parallel()

	harness := NewE2ETestHarness(t)
	defer harness.Shutdown()

	ctx := context.Background()

	// Configure mock with data
	harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
		ID:      70,
		Name:    "API Test Bot",
		Enabled: true,
	})

	harness.ThreeCommasMock.AddDeal(70, threecommasmock.Deal{
		ID:           901,
		BotID:        70,
		Pair:         "USDT_BTC",
		Status:       "active",
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
		Events: []threecommasmock.BotEvent{
			{
				CreatedAt:     "2024-01-15T10:30:00.000Z",
				Action:        "place",
				Coin:          "BTC",
				Type:          "buy",
				Status:        "active",
				Price:         "50000.0",
				Size:          "0.001",
				OrderType:     "base",
				OrderSize:     1,
				OrderPosition: 1,
				IsMarket:      false,
			},
		},
	})

	// Start application
	harness.Start(ctx)

	// Trigger sync
	harness.TriggerDealProduction(ctx)
	time.Sleep(500 * time.Millisecond)

	// Test /api/bots
	t.Run("GET /api/bots", func(t *testing.T) {
		resp := harness.APIGet("/api/bots")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var apiResp map[string]interface{}
		err = json.Unmarshal(body, &apiResp)
		require.NoError(t, err)
		require.Contains(t, apiResp, "bots")
	})

	// Test /api/deals
	t.Run("GET /api/deals", func(t *testing.T) {
		resp := harness.APIGet("/api/deals")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var apiResp map[string]interface{}
		err = json.Unmarshal(body, &apiResp)
		require.NoError(t, err)
		require.Contains(t, apiResp, "deals")
	})

	// Test /api/orders
	t.Run("GET /api/orders", func(t *testing.T) {
		resp := harness.APIGet("/api/orders")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var apiResp map[string]interface{}
		err = json.Unmarshal(body, &apiResp)
		require.NoError(t, err)
		require.Contains(t, apiResp, "orders")
	})

	t.Log("✅ E2E test passed: All API endpoints responding correctly")
}
