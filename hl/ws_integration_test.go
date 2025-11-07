package hl_test

import (
	"context"
	"testing"
	"time"

	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/filltracker"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

// TestWebSocketOrderUpdates verifies that the WebSocket client receives order
// status updates when orders are created, modified, and canceled.
func TestWebSocketOrderUpdates(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID(1)
	wallet := "0xtest"

	// Create WebSocket client
	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	exchange := newMockExchange(t, ts.URL())

	// Test 1: Create order and verify WebSocket receives "open" status
	oid1 := orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 1}
	cloid1 := oid1.Hex()
	order1 := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &cloid1,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err = exchange.Order(ctx, order1, nil)
	require.NoError(t, err)

	// Wait for WebSocket to receive and store the update
	require.Eventually(t, func() bool {
		return wsClient.Exists(ctx, oid1)
	}, 5*time.Second, 100*time.Millisecond, "WebSocket should receive order creation update")

	wsOrder, ok := wsClient.Get(ctx, oid1)
	require.True(t, ok)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, wsOrder.Status)
	require.Equal(t, cloid1, *wsOrder.Order.Cloid)

	// Test 2: Modify order and verify WebSocket receives update
	storedOrder, exists := ts.GetOrder(cloid1)
	require.True(t, exists)

	_, err = exchange.ModifyOrder(ctx, hyperliquid.ModifyOrderRequest{
		Oid: storedOrder.Order.Oid,
		Order: hyperliquid.CreateOrderRequest{
			Coin:          "BTC",
			IsBuy:         true,
			Price:         51000,
			Size:          0.5,
			ClientOrderID: &cloid1,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		},
	})
	require.NoError(t, err)

	// Wait for modification update
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(ctx, oid1)
		return ok && wsOrder.Order.LimitPx == "51000"
	}, 5*time.Second, 100*time.Millisecond, "WebSocket should receive order modification update")

	// Test 3: Cancel order and verify WebSocket receives update
	_, err = exchange.CancelByCloid(ctx, "BTC", cloid1)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(ctx, oid1)
		return ok && wsOrder.Status == hyperliquid.OrderStatusValueCanceled
	}, 5*time.Second, 100*time.Millisecond, "WebSocket should receive cancellation update")
}

// TestWebSocketOrderFillUpdates verifies that the WebSocket client receives
// updates when orders are filled (both partial and full fills).
func TestWebSocketOrderFillUpdates(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID(1)
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	exchange := newMockExchange(t, ts.URL())

	// Create order
	oid := orderid.OrderId{BotID: 2, DealID: 2, BotEventID: 2}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         3000,
		Size:          10.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err = exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Wait for order creation
	require.Eventually(t, func() bool {
		return wsClient.Exists(ctx, oid)
	}, 5*time.Second, 100*time.Millisecond)

	// Simulate partial fill
	err = ts.FillOrder(cloid, 3000, mockserver.WithFillSize(3.0))
	require.NoError(t, err)

	// WebSocket should receive partial fill update
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(ctx, oid)
		if !ok {
			return false
		}
		// Partial fill: status should still be "open" with reduced size
		return wsOrder.Status == hyperliquid.OrderStatusValueOpen && wsOrder.Order.Sz != "10"
	}, 5*time.Second, 100*time.Millisecond, "WebSocket should receive partial fill update")

	// Simulate complete fill
	err = ts.FillOrder(cloid, 3000)
	require.NoError(t, err)

	// WebSocket should receive full fill update
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(ctx, oid)
		return ok && wsOrder.Status == hyperliquid.OrderStatusValueFilled
	}, 5*time.Second, 100*time.Millisecond, "WebSocket should receive full fill update")
}

// TestWebSocketWithFillTracker verifies that the WebSocket client properly
// integrates with the fill tracker service.
func TestWebSocketWithFillTracker(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID(1)
	wallet := "0xtest"

	// Create fill tracker
	tracker, err := filltracker.NewService(ctx, store, store)
	require.NoError(t, err)

	// Create WebSocket client with fill tracker
	wsClient, err := ws.New(ctx, store, tracker, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	exchange := newMockExchange(t, ts.URL())

	// Create a base order
	oid := orderid.OrderId{BotID: 3, DealID: 3, BotEventID: 1}
	cloid := oid.Hex()
	ident := recomma.NewOrderIdentifier(venueID, wallet, oid)

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

	// Submit as base order via fill tracker
	err = tracker.SubmitBaseOrder(ctx, ident, "SOL", 100, 10.0, false)
	require.NoError(t, err)

	_, err = exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Wait for WebSocket to receive order
	require.Eventually(t, func() bool {
		return wsClient.Exists(ctx, oid)
	}, 5*time.Second, 100*time.Millisecond)

	// Fill the order
	err = ts.FillOrder(cloid, 100, mockserver.WithFillSize(10.0))
	require.NoError(t, err)

	// WebSocket should receive fill and update fill tracker
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(ctx, oid)
		if !ok {
			return false
		}
		return wsOrder.Status == hyperliquid.OrderStatusValueFilled
	}, 5*time.Second, 100*time.Millisecond)

	// Verify fill tracker was updated
	position, err := tracker.CurrentPosition(ctx, ident)
	require.NoError(t, err)
	require.Greater(t, position, 0.0, "Fill tracker should reflect filled position")
}

// TestWebSocketMultipleOrders verifies that the WebSocket client can handle
// multiple concurrent orders and their updates.
func TestWebSocketMultipleOrders(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID(1)
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	exchange := newMockExchange(t, ts.URL())

	// Create 5 orders concurrently
	orderCount := 5
	oids := make([]orderid.OrderId, orderCount)

	for i := 0; i < orderCount; i++ {
		oid := orderid.OrderId{BotID: uint32(i + 10), DealID: 1, BotEventID: 1}
		oids[i] = oid
		cloid := oid.Hex()

		order := hyperliquid.CreateOrderRequest{
			Coin:          "BTC",
			IsBuy:         true,
			Price:         50000 + float64(i*100),
			Size:          1.0,
			ClientOrderID: &cloid,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		}

		_, err := exchange.Order(ctx, order, nil)
		require.NoError(t, err)
	}

	// Verify all orders received via WebSocket
	for _, oid := range oids {
		oid := oid // capture
		require.Eventually(t, func() bool {
			return wsClient.Exists(ctx, oid)
		}, 5*time.Second, 100*time.Millisecond, "All orders should be received via WebSocket")
	}

	// Fill some orders, cancel others
	for i, oid := range oids {
		cloid := oid.Hex()
		if i%2 == 0 {
			// Fill even-indexed orders
			err := ts.FillOrder(cloid, 50000+float64(i*100))
			require.NoError(t, err)
		} else {
			// Cancel odd-indexed orders
			_, err := exchange.CancelByCloid(ctx, "BTC", cloid)
			require.NoError(t, err)
		}
	}

	// Verify final states
	for i, oid := range oids {
		oid := oid // capture
		expectedStatus := hyperliquid.OrderStatusValueFilled
		if i%2 != 0 {
			expectedStatus = hyperliquid.OrderStatusValueCanceled
		}

		require.Eventually(t, func() bool {
			wsOrder, ok := wsClient.Get(ctx, oid)
			return ok && wsOrder.Status == expectedStatus
		}, 5*time.Second, 100*time.Millisecond)
	}
}

// TestWebSocketReconnection tests that the WebSocket client properly handles
// disconnection and reconnection scenarios.
func TestWebSocketReconnection(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID(1)
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)

	exchange := newMockExchange(t, ts.URL())

	// Create an order before disconnect
	oid1 := orderid.OrderId{BotID: 5, DealID: 5, BotEventID: 1}
	cloid1 := oid1.Hex()
	order1 := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &cloid1,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err = exchange.Order(ctx, order1, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return wsClient.Exists(ctx, oid1)
	}, 5*time.Second, 100*time.Millisecond)

	// Close and reconnect
	err = wsClient.Close()
	require.NoError(t, err)

	// Create new WebSocket client (simulating reconnection)
	wsClient, err = ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Create another order after reconnect
	oid2 := orderid.OrderId{BotID: 5, DealID: 5, BotEventID: 2}
	cloid2 := oid2.Hex()
	order2 := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         false,
		Price:         3000,
		Size:          2.0,
		ClientOrderID: &cloid2,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err = exchange.Order(ctx, order2, nil)
	require.NoError(t, err)

	// Verify new order is received
	require.Eventually(t, func() bool {
		return wsClient.Exists(ctx, oid2)
	}, 5*time.Second, 100*time.Millisecond, "Should receive orders after reconnection")

	// Verify old order is still in storage (persisted)
	require.True(t, wsClient.Exists(ctx, oid1), "Old orders should persist in storage")
}

// Helper function to create a test storage instance
func newTestStore(t *testing.T) *storage.Storage {
	t.Helper()
	store, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() {
		store.Close()
	})
	return store
}
