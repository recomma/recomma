//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	"github.com/recomma/recomma/storage"
	"github.com/stretchr/testify/require"
)

// TestE2E_OrderScaling_WithMultiplier tests the full E2E flow:
// 3commas mock (deal with small size) -> app (scales 5x) -> hyperliquid mock
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

	// Trigger deal production (3commas -> app)
	harness.TriggerDealProduction(ctx)

	// Wait for order submission (app -> hyperliquid)
	harness.WaitForOrderSubmission(5 * time.Second)

	// Verify E2E: order was scaled and submitted to Hyperliquid mock
	orders := harness.HyperliquidMock.GetAllOrders()
	require.NotEmpty(t, orders, "expected at least one order submitted to Hyperliquid")

	order := orders[0]
	require.Equal(t, "BTC", order.Order.Coin)
	require.True(t, order.Order.IsBuy)
	require.InDelta(t, 0.005, order.Order.Size, 0.0001, "expected order size to be scaled to 0.005 (0.001 * 5.0)")

	t.Log("✅ E2E test passed: 3commas -> app (5x scaling) -> hyperliquid")
}

// TestE2E_OrderScaling_MaxMultiplierClamp tests the full E2E flow with max multiplier clamping:
// 3commas mock (deal) -> app (clamps 20x to 10x max) -> hyperliquid mock
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

	// Trigger E2E flow
	harness.TriggerDealProduction(ctx)
	harness.WaitForOrderSubmission(5 * time.Second)

	// Verify E2E: order was clamped and submitted
	orders := harness.HyperliquidMock.GetAllOrders()
	require.NotEmpty(t, orders, "expected at least one order submitted to Hyperliquid")

	order := orders[0]
	require.InDelta(t, 0.1, order.Order.Size, 0.01, "expected order size to be clamped to 0.1 (0.01 * 10.0 max)")

	t.Log("✅ E2E test passed: 3commas -> app (max clamp) -> hyperliquid")
}

// TestE2E_MultiDeal_ConcurrentProcessing tests multiple deals flowing through the system:
// 3commas mock (3 bots, 3 deals) -> app -> hyperliquid mock (3 orders)
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

	// Trigger E2E flow (should process all 3 deals)
	harness.TriggerDealProduction(ctx)

	// Wait for all deals to be processed
	harness.WaitForDealProcessing(301, 5*time.Second)
	harness.WaitForDealProcessing(302, 5*time.Second)
	harness.WaitForDealProcessing(401, 5*time.Second)

	// Wait for all orders to be submitted
	time.Sleep(2 * time.Second)

	// Verify E2E: all 3 orders were submitted to Hyperliquid mock
	orders := harness.HyperliquidMock.GetAllOrders()
	require.Len(t, orders, 3, "expected 3 orders to be submitted to Hyperliquid")

	// Verify coins match
	coins := make(map[string]int)
	for _, order := range orders {
		coins[order.Order.Coin]++
	}
	require.Equal(t, 1, coins["BTC"], "expected 1 BTC order")
	require.Equal(t, 1, coins["ETH"], "expected 1 ETH order")
	require.Equal(t, 1, coins["SOL"], "expected 1 SOL order")

	t.Log("✅ E2E test passed: 3commas (3 deals) -> app -> hyperliquid (3 orders)")
}

// TestE2E_TakeProfitStack tests the full E2E flow with stacked take-profit orders:
// 3commas mock (1 base + 3 TPs) -> app -> hyperliquid mock (base order + TPs)
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

	// Trigger E2E flow
	harness.TriggerDealProduction(ctx)

	// Wait for orders to be submitted
	time.Sleep(3 * time.Second)

	// Verify E2E: base order submitted (TPs managed separately by fill tracker)
	orders := harness.HyperliquidMock.GetAllOrders()
	require.GreaterOrEqual(t, len(orders), 1, "expected at least the base order to be submitted to Hyperliquid")

	// Count buy vs sell orders
	var buyOrders, sellOrders int
	for _, order := range orders {
		if order.Order.IsBuy {
			buyOrders++
		} else {
			sellOrders++
		}
	}
	require.Equal(t, 1, buyOrders, "expected 1 buy order (base)")

	t.Log("✅ E2E test passed: 3commas (TP stack) -> app -> hyperliquid")
}
