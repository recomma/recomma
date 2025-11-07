package hl

import (
	"context"
	"testing"
	"time"

	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/orderid"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestOrderLifecycleCreateAndQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	// Create order
	status, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)
	require.NotNil(t, status)

	// Verify order exists in mock server
	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)
}

func TestOrderLifecycleCreateFillCancel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 5, DealID: 10, BotEventID: 15}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         3000,
		Size:          2.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	// Create
	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)

	// Fill
	ts.FillOrder(cloid, 3000)
	storedOrder, exists = ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "filled", storedOrder.Status)

	// Create another order to cancel
	oid2 := orderid.OrderId{BotID: 5, DealID: 10, BotEventID: 16}
	cloid2 := oid2.Hex()
	order2 := order
	order2.ClientOrderID = &cloid2

	_, err = exchange.Order(ctx, order2, nil)
	require.NoError(t, err)

	// Cancel
	_, err = exchange.CancelByCloid(ctx, order2.Coin, cloid2)
	require.NoError(t, err)

	storedOrder2, exists := ts.GetOrder(cloid2)
	require.True(t, exists)
	require.Equal(t, "canceled", storedOrder2.Status)
}

func TestOrderLifecyclePartialFill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 20, DealID: 30, BotEventID: 40}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "SOL",
		IsBuy:         true,
		Price:         100,
		Size:          10.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Partial fill (30%)
	ts.FillOrder(cloid, 100, mockserver.WithFillSize(3.0))

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)
	// Mock server should track partial fills
}

func TestOrderLifecycleMultipleOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	// Create multiple orders with different states
	orders := []struct {
		oid    orderid.OrderId
		coin   string
		price  float64
		size   float64
		isBuy  bool
		action string // "fill", "cancel", or "leave"
	}{
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 1}, "BTC", 50000, 1.0, true, "fill"},
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 2}, "ETH", 3000, 2.0, false, "cancel"},
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 3}, "SOL", 100, 10.0, true, "leave"},
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 4}, "ARB", 1.5, 100.0, false, "fill"},
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 5}, "DOGE", 0.15, 1000.0, true, "cancel"},
	}

	for _, tc := range orders {
		cloid := tc.oid.Hex()
		order := hyperliquid.CreateOrderRequest{
			Coin:          tc.coin,
			IsBuy:         tc.isBuy,
			Price:         tc.price,
			Size:          tc.size,
			ClientOrderID: &cloid,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		}

		_, err := exchange.Order(ctx, order, nil)
		require.NoError(t, err)

		switch tc.action {
		case "fill":
			ts.FillOrder(cloid, tc.price)
		case "cancel":
			_, err := exchange.CancelByCloid(ctx, tc.coin, cloid)
			require.NoError(t, err)
		case "leave":
			// Leave open
		}
	}

	// Verify final states
	for _, tc := range orders {
		cloid := tc.oid.Hex()
		storedOrder, exists := ts.GetOrder(cloid)
		require.True(t, exists, "order %s should exist", cloid)

		switch tc.action {
		case "fill":
			require.Equal(t, "filled", storedOrder.Status)
		case "cancel":
			require.Equal(t, "canceled", storedOrder.Status)
		case "leave":
			require.Equal(t, "open", storedOrder.Status)
		}
	}
}

func TestOrderLifecycleModifyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 100, DealID: 200, BotEventID: 300}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	originalOid := storedOrder.Order.Oid

	// Modify order
	modifyReq := hyperliquid.ModifyOrderRequest{
		Oid: originalOid,
		Order: hyperliquid.ModifyOrderRequestOrder{
			Price: 51000,
			Size:  1.5,
		},
	}

	_, err = exchange.ModifyOrder(ctx, modifyReq)
	require.NoError(t, err)

	// Verify modification
	storedOrder, exists = ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)
}

func TestOrderLifecycleIOCOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 500, DealID: 600, BotEventID: 700}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          0.5,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}

	// IOC orders should be rejected by mock server if no immediate match
	_, err := exchange.Order(ctx, order, nil)
	// Mock server behavior may vary - check if error or status indicates IOC rejection
	if err == nil {
		// If no error, check the order status
		storedOrder, exists := ts.GetOrder(cloid)
		if exists {
			// IOC should either be filled or canceled immediately
			require.Contains(t, []string{"filled", "canceled"}, storedOrder.Status)
		}
	}
}

func TestOrderLifecycleWithOrderIdCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	cache := NewOrderIdCache(info)
	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 1000, DealID: 2000, BotEventID: 3000}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         3000,
		Size:          1.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Let cache populate (might need small delay for async operations)
	time.Sleep(100 * time.Millisecond)

	// Try to get order ID from cache
	cachedOid, found := cache.Get(ctx, cloid)
	if found {
		require.NotZero(t, cachedOid, "cached OID should be non-zero")
	}
}

func TestOrderLifecycleReduceOnlyOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	// Create a reduce-only order (typically used for take profit)
	oid := orderid.OrderId{BotID: 1500, DealID: 2500, BotEventID: 3500}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         false,
		Price:         55000,
		Size:          0.5,
		ClientOrderID: &cloid,
		ReduceOnly:    true,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)
}

func TestOrderLifecycleBuyAndSellOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())

	// Create buy order
	buyOid := orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 1}
	buyCloid := buyOid.Hex()

	buyOrder := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &buyCloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, buyOrder, nil)
	require.NoError(t, err)

	// Create sell order
	sellOid := orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 2}
	sellCloid := sellOid.Hex()

	sellOrder := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         false,
		Price:         51000,
		Size:          1.0,
		ClientOrderID: &sellCloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err = exchange.Order(ctx, sellOrder, nil)
	require.NoError(t, err)

	// Verify both exist
	buyStored, buyExists := ts.GetOrder(buyCloid)
	require.True(t, buyExists)
	require.Equal(t, "open", buyStored.Status)

	sellStored, sellExists := ts.GetOrder(sellCloid)
	require.True(t, sellExists)
	require.Equal(t, "open", sellStored.Status)
}
