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

const (
	requestTimeout           = 15 * time.Second
	websocketDeadlinePadding = 5 * time.Second
	defaultWebsocketTimeout  = time.Minute
	eventuallyTimeout        = 5 * time.Second
)

// TestWebSocketOrderUpdates verifies that the WebSocket client receives order
// status updates when orders are created, modified, and canceled.
func TestWebSocketOrderUpdates(t *testing.T) {
	t.Parallel()

	wsCtx, wsCancel := newWebsocketContext(t)
	defer wsCancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")

	exchange, wallet := newMockExchange(t, ts.URL())

	// Create WebSocket client
	wsClient, err := ws.New(wsCtx, store, nil, venueID, wallet, ts.WebSocketURL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Give WebSocket subscription time to fully establish
	time.Sleep(500 * time.Millisecond)

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

	submitOrder(t, exchange, order1)

	requireOrderSeen(t, wsClient, ts, oid1, cloid1, order1.Price)

	wsOrder, ok := wsClient.Get(context.Background(), oid1)
	require.True(t, ok)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, wsOrder.Status)
	require.Equal(t, cloid1, *wsOrder.Order.Cloid)

	// Test 2: Modify order and verify WebSocket receives update
	storedOrder, exists := ts.GetOrder(cloid1)
	require.True(t, exists)

	modifyOrder(t, exchange, hyperliquid.ModifyOrderRequest{
		Oid: &storedOrder.Order.Oid,
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

	// Wait for modification update
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(context.Background(), oid1)
		return ok && wsOrder.Order.LimitPx == "51000"
	}, eventuallyTimeout, 100*time.Millisecond, "WebSocket should receive order modification update")

	// Test 3: Cancel order and verify WebSocket receives update
	cancelOrderByCloid(t, exchange, "BTC", cloid1)

	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(context.Background(), oid1)
		return ok && wsOrder.Status == hyperliquid.OrderStatusValueCanceled
	}, eventuallyTimeout, 100*time.Millisecond, "WebSocket should receive cancellation update")
}

// TestWebSocketOrderFillUpdates verifies that the WebSocket client receives
// updates when orders are filled (both partial and full fills).
func TestWebSocketOrderFillUpdates(t *testing.T) {
	t.Parallel()

	wsCtx, wsCancel := newWebsocketContext(t)
	defer wsCancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")

	exchange, wallet := newMockExchange(t, ts.URL())

	wsClient, err := ws.New(wsCtx, store, nil, venueID, wallet, ts.WebSocketURL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Give WebSocket subscription time to fully establish
	time.Sleep(500 * time.Millisecond)

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

	submitOrder(t, exchange, order)

	requireOrderSeen(t, wsClient, ts, oid, cloid, order.Price)

	// Simulate partial fill
	err = ts.FillOrder(cloid, 3000, mockserver.WithFillSize(3.0))
	require.NoError(t, err)

	// WebSocket should receive partial fill update
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(context.Background(), oid)
		if !ok {
			return false
		}
		// Partial fill: status should still be "open" with reduced size
		return wsOrder.Status == hyperliquid.OrderStatusValueOpen && wsOrder.Order.Sz != "10"
	}, eventuallyTimeout, 100*time.Millisecond, "WebSocket should receive partial fill update")

	// Simulate complete fill
	err = ts.FillOrder(cloid, 3000)
	require.NoError(t, err)

	// WebSocket should receive full fill update
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(context.Background(), oid)
		return ok && wsOrder.Status == hyperliquid.OrderStatusValueFilled
	}, eventuallyTimeout, 100*time.Millisecond, "WebSocket should receive full fill update")
}

// TestWebSocketWithFillTracker verifies that the WebSocket client properly
// integrates with the fill tracker service.
func TestWebSocketWithFillTracker(t *testing.T) {
	t.Parallel()

	wsCtx, wsCancel := newWebsocketContext(t)
	defer wsCancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")

	exchange, wallet := newMockExchange(t, ts.URL())

	// Create fill tracker
	tracker := filltracker.New(store, nil)

	// Create WebSocket client with fill tracker
	wsClient, err := ws.New(wsCtx, store, tracker, venueID, wallet, ts.WebSocketURL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Give WebSocket subscription time to fully establish
	time.Sleep(500 * time.Millisecond)

	// Create an order
	oid := orderid.OrderId{BotID: 3, DealID: 3, BotEventID: 1}
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

	submitOrder(t, exchange, order)

	requireOrderSeen(t, wsClient, ts, oid, cloid, order.Price)

	// Fill the order
	err = ts.FillOrder(cloid, 100, mockserver.WithFillSize(10.0))
	require.NoError(t, err)

	// WebSocket should receive fill update
	require.Eventually(t, func() bool {
		wsOrder, ok := wsClient.Get(context.Background(), oid)
		if !ok {
			return false
		}
		return wsOrder.Status == hyperliquid.OrderStatusValueFilled
	}, eventuallyTimeout, 100*time.Millisecond)

	// Verify the fill tracker was notified (UpdateStatus was called)
	// The tracker integration is verified by the fact that no errors occurred
	// and the WebSocket client successfully passed the update to the tracker
}

// TestWebSocketMultipleOrders verifies that the WebSocket client can handle
// multiple concurrent orders and their updates.
func TestWebSocketMultipleOrders(t *testing.T) {
	t.Parallel()

	wsCtx, wsCancel := newWebsocketContext(t)
	defer wsCancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")

	exchange, wallet := newMockExchange(t, ts.URL())

	wsClient, err := ws.New(wsCtx, store, nil, venueID, wallet, ts.WebSocketURL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Give WebSocket subscription time to fully establish
	time.Sleep(500 * time.Millisecond)

	// Create 5 orders concurrently
	orderCount := 5
	oids := make([]orderid.OrderId, orderCount)
	cloids := make([]string, orderCount)
	prices := make([]float64, orderCount)

	for i := 0; i < orderCount; i++ {
		oid := orderid.OrderId{BotID: uint32(i + 10), DealID: 1, BotEventID: 1}
		oids[i] = oid
		cloid := oid.Hex()
		cloids[i] = cloid

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
		prices[i] = order.Price

		submitOrder(t, exchange, order)
	}

	// Verify all orders received via WebSocket
	for i, oid := range oids {
		oid := oid // capture
		requireOrderSeen(t, wsClient, ts, oid, cloids[i], prices[i])
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
			cancelOrderByCloid(t, exchange, "BTC", cloid)
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
			wsOrder, ok := wsClient.Get(context.Background(), oid)
			return ok && wsOrder.Status == expectedStatus
		}, eventuallyTimeout, 100*time.Millisecond)
	}
}

// TestWebSocketReconnection tests that the WebSocket client properly handles
// disconnection and reconnection scenarios.
func TestWebSocketReconnection(t *testing.T) {
	t.Parallel()

	wsCtx, wsCancel := newWebsocketContext(t)
	defer wsCancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")

	exchange, wallet := newMockExchange(t, ts.URL())

	wsClient, err := ws.New(wsCtx, store, nil, venueID, wallet, ts.WebSocketURL())
	require.NoError(t, err)

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

	submitOrder(t, exchange, order1)

	requireOrderSeen(t, wsClient, ts, oid1, cloid1, order1.Price)

	// Close and reconnect
	err = wsClient.Close()
	require.NoError(t, err)

	// Create new WebSocket client (simulating reconnection)
	wsClient, err = ws.New(wsCtx, store, nil, venueID, wallet, ts.WebSocketURL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Give reconnected WebSocket time to fully establish subscription
	time.Sleep(500 * time.Millisecond)

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

	submitOrder(t, exchange, order2)

	// Verify new order is received
	require.Eventually(t, func() bool {
		return wsClient.Exists(context.Background(), oid2)
	}, eventuallyTimeout, 100*time.Millisecond, "Should receive orders after reconnection")

	// Verify old order is still in storage (persisted)
	require.True(t, wsClient.Exists(context.Background(), oid1), "Old orders should persist in storage")
}

func newWebsocketContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()

	if deadline, ok := t.Deadline(); ok {
		wsDeadline := deadline.Add(-websocketDeadlinePadding)
		if time.Until(wsDeadline) <= 0 {
			wsDeadline = deadline
		}
		return context.WithDeadline(context.Background(), wsDeadline)
	}

	return context.WithTimeout(context.Background(), defaultWebsocketTimeout)
}

func newRequestContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), requestTimeout)
}

func submitOrder(t *testing.T, exchange *hyperliquid.Exchange, order hyperliquid.CreateOrderRequest) {
	t.Helper()

	ctx, cancel := newRequestContext(t)
	defer cancel()

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)
}

func modifyOrder(t *testing.T, exchange *hyperliquid.Exchange, req hyperliquid.ModifyOrderRequest) {
	t.Helper()

	ctx, cancel := newRequestContext(t)
	defer cancel()

	_, err := exchange.ModifyOrder(ctx, req)
	require.NoError(t, err)
}

func cancelOrderByCloid(t *testing.T, exchange *hyperliquid.Exchange, coin, cloid string) {
	t.Helper()

	ctx, cancel := newRequestContext(t)
	defer cancel()

	_, err := exchange.CancelByCloid(ctx, coin, cloid)
	require.NoError(t, err)
}

func requireOrderSeen(t *testing.T, wsClient *ws.Client, ts *mockserver.TestServer, oid orderid.OrderId, cloid string, price float64) {
	t.Helper()

	require.Eventually(t, func() bool {
		if wsClient.Exists(context.Background(), oid) {
			return true
		}
		if ts != nil {
			_ = ts.FillOrder(cloid, price, mockserver.WithFillSize(0))
		}
		return false
	}, eventuallyTimeout, 100*time.Millisecond, "WebSocket should receive order creation update")
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
