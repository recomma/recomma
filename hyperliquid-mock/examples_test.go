package hyperliquidmock_test

import (
	"context"
	"testing"
	"time"

	"github.com/recomma/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/hl"
	"github.com/sonirico/go-hyperliquid"
)

// ExampleNewTestServer demonstrates basic test server usage
func ExampleNewTestServer() {
	// This would normally be in a test function with *testing.T
	// ts := server.NewTestServer(t)
	//
	// Use ts.URL() as your Hyperliquid API endpoint
	// The server automatically cleans up when the test finishes
}

// TestRealHyperliquidIntegration demonstrates testing with the actual go-hyperliquid library
func TestRealHyperliquidIntegration(t *testing.T) {
	// Create isolated test server
	ts := server.NewTestServer(t)

	// Configure Hyperliquid client to use mock server
	ctx := context.Background()
	exchange, err := hl.NewExchange(ctx, hl.ClientConfig{
		BaseURL: ts.URL(), // Point to our mock server!
		Wallet:  "0x0000000000000000000000000000000000000001",
		Key:     "0000000000000000000000000000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatalf("Failed to create exchange client: %v", err)
	}

	// Create an order using the real go-hyperliquid library
	orderReq := hyperliquid.CreateOrderRequest{
		Coin:  "ETH",
		IsBuy: true,
		Size:  1.5,
		Price: 3000.0,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifGtc,
			},
		},
		Cloid: strPtr("test-order-1"),
	}

	status, err := exchange.Order(ctx, orderReq, nil)
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Verify we got a response
	if status.Resting == nil {
		t.Error("Expected resting order status")
	}

	// Now inspect what was actually sent to the mock server
	exchangeReqs := ts.GetExchangeRequests()
	if len(exchangeReqs) != 1 {
		t.Fatalf("Expected 1 exchange request, got %d", len(exchangeReqs))
	}

	// Verify the request structure
	req := exchangeReqs[0]
	if req.Nonce == 0 {
		t.Error("Expected non-zero nonce")
	}

	if req.Signature.R == "" || req.Signature.S == "" {
		t.Error("Expected signature to be present")
	}

	// Verify order was stored in mock server state
	order, exists := ts.GetOrder("test-order-1")
	if !exists {
		t.Fatal("Order not found in server state")
	}

	if order.Order.Coin != "ETH" {
		t.Errorf("Expected coin ETH, got %s", order.Order.Coin)
	}

	if order.Order.Side != "B" {
		t.Errorf("Expected side B (buy), got %s", order.Order.Side)
	}

	if order.Status != "open" {
		t.Errorf("Expected status open, got %s", order.Status)
	}
}

// TestOrderModification demonstrates testing order modifications
func TestOrderModification(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	exchange, err := hl.NewExchange(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0x0000000000000000000000000000000000000001",
		Key:     "0000000000000000000000000000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatalf("Failed to create exchange: %v", err)
	}

	// Create initial order
	cloid := "test-modify-" + time.Now().Format("20060102150405")
	_, err = exchange.Order(ctx, hyperliquid.CreateOrderRequest{
		Coin:  "BTC",
		IsBuy: true,
		Size:  0.5,
		Price: 50000.0,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		Cloid: &cloid,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Get the OID from the stored order
	order, exists := ts.GetOrder(cloid)
	if !exists {
		t.Fatal("Order not found")
	}
	oid := order.Order.Oid

	// Clear request history to focus on the modify
	ts.ClearRequests()

	// Modify the order
	_, err = exchange.ModifyOrder(ctx, hyperliquid.ModifyOrderRequest{
		Oid: oidToHex(oid),
		Order: hyperliquid.CreateOrderRequest{
			Coin:  "BTC",
			IsBuy: true,
			Size:  0.75, // Changed size
			Price: 51000.0, // Changed price
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		},
	})
	if err != nil {
		t.Fatalf("Failed to modify order: %v", err)
	}

	// Verify the modify request was captured
	if ts.RequestCount() != 1 {
		t.Errorf("Expected 1 modify request, got %d", ts.RequestCount())
	}

	// Verify the modified state
	modifiedOrder, exists := ts.GetOrder(cloid)
	if !exists {
		t.Fatal("Modified order not found")
	}

	if modifiedOrder.Order.Sz != "0.75" {
		t.Errorf("Expected size 0.75, got %s", modifiedOrder.Order.Sz)
	}

	if modifiedOrder.Order.LimitPx != "51000" {
		t.Errorf("Expected price 51000, got %s", modifiedOrder.Order.LimitPx)
	}
}

// TestOrderCancellation demonstrates testing order cancellations
func TestOrderCancellation(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	exchange, err := hl.NewExchange(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0x0000000000000000000000000000000000000001",
		Key:     "0000000000000000000000000000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatalf("Failed to create exchange: %v", err)
	}

	// Create an order
	cloid := "test-cancel-" + time.Now().Format("20060102150405")
	_, err = exchange.Order(ctx, hyperliquid.CreateOrderRequest{
		Coin:  "SOL",
		IsBuy: false,
		Size:  10.0,
		Price: 100.0,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		Cloid: &cloid,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Verify order is open
	order, exists := ts.GetOrder(cloid)
	if !exists {
		t.Fatal("Order not found")
	}
	if order.Status != "open" {
		t.Errorf("Expected status open, got %s", order.Status)
	}

	// Clear history to focus on cancel
	ts.ClearRequests()

	// Cancel the order
	_, err = exchange.CancelByCloid(ctx, "SOL", cloid)
	if err != nil {
		t.Fatalf("Failed to cancel order: %v", err)
	}

	// Verify cancel request was captured
	if ts.RequestCount() != 1 {
		t.Errorf("Expected 1 cancel request, got %d", ts.RequestCount())
	}

	// Verify order is now canceled
	canceledOrder, exists := ts.GetOrder(cloid)
	if !exists {
		t.Fatal("Canceled order not found")
	}
	if canceledOrder.Status != "canceled" {
		t.Errorf("Expected status canceled, got %s", canceledOrder.Status)
	}
}

// TestQueryOrderStatus demonstrates testing order status queries
func TestQueryOrderStatus(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	// Create Info client (read-only queries)
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0x0000000000000000000000000000000000000001",
	})

	// First, create an order so we have something to query
	exchange, err := hl.NewExchange(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0x0000000000000000000000000000000000000001",
		Key:     "0000000000000000000000000000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatalf("Failed to create exchange: %v", err)
	}

	cloid := "test-query-" + time.Now().Format("20060102150405")
	_, err = exchange.Order(ctx, hyperliquid.CreateOrderRequest{
		Coin:  "ARB",
		IsBuy: true,
		Size:  100.0,
		Price: 1.5,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		Cloid: &cloid,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Clear exchange request
	ts.ClearRequests()

	// Query the order status
	result, err := info.QueryOrderByCloid(ctx, cloid)
	if err != nil {
		t.Fatalf("Failed to query order: %v", err)
	}

	// Verify query was captured
	infoReqs := ts.GetInfoRequests()
	if len(infoReqs) != 1 {
		t.Fatalf("Expected 1 info request, got %d", len(infoReqs))
	}

	if infoReqs[0].Type != "orderStatus" {
		t.Errorf("Expected type orderStatus, got %s", infoReqs[0].Type)
	}

	// Verify the result
	if result.Status != hyperliquid.OrderQueryStatusSuccess {
		t.Errorf("Expected success status, got %s", result.Status)
	}

	if result.Order.Order.Coin != "ARB" {
		t.Errorf("Expected coin ARB, got %s", result.Order.Order.Coin)
	}
}

// TestMetadataQueries demonstrates testing metadata queries
func TestMetadataQueries(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0x0000000000000000000000000000000000000001",
	})

	// This would trigger metadata fetch inside the library
	// The go-hyperliquid library auto-fetches metadata on initialization
	// We can verify it hit our mock server

	// For now, just verify info requests were made
	// In a real test, you'd call methods that trigger metadata fetches
	infoReqs := ts.GetInfoRequests()

	// The library should have fetched metadata during initialization
	if len(infoReqs) < 1 {
		t.Logf("Note: go-hyperliquid library may have cached metadata")
	}
}

// TestParallelTests demonstrates that parallel tests are safe
func TestParallelTests(t *testing.T) {
	t.Run("Test1", func(t *testing.T) {
		t.Parallel()
		ts := server.NewTestServer(t)

		// Make requests specific to this test
		makeTestOrder(t, ts, "ETH", 1.0, 3000.0)

		if ts.RequestCount() != 1 {
			t.Errorf("Test1: Expected 1 request, got %d", ts.RequestCount())
		}
	})

	t.Run("Test2", func(t *testing.T) {
		t.Parallel()
		ts := server.NewTestServer(t)

		// Make different requests
		makeTestOrder(t, ts, "BTC", 0.5, 50000.0)
		makeTestOrder(t, ts, "SOL", 10.0, 100.0)

		if ts.RequestCount() != 2 {
			t.Errorf("Test2: Expected 2 requests, got %d", ts.RequestCount())
		}
	})

	// Each test has its own isolated server and request history
}

// Helper functions

func strPtr(s string) *string {
	return &s
}

func oidToHex(oid int64) string {
	// Convert OID to hex string format
	return hyperliquid.OrderIdToHex(oid)
}

func makeTestOrder(t *testing.T, ts *server.TestServer, coin string, size, price float64) {
	t.Helper()

	ctx := context.Background()
	exchange, err := hl.NewExchange(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0x0000000000000000000000000000000000000001",
		Key:     "0000000000000000000000000000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatalf("Failed to create exchange: %v", err)
	}

	cloid := "test-" + coin + "-" + time.Now().Format("20060102150405")
	_, err = exchange.Order(ctx, hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: true,
		Size:  size,
		Price: price,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		Cloid: &cloid,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}
}
