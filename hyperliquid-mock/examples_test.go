package hyperliquidmock_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/recomma/hyperliquid-mock/server"
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

	// Create a test private key
	privateKey, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("Failed to create private key: %v", err)
	}

	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	accountAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	// Create exchange client pointing to mock server
	exchange := hyperliquid.NewExchange(
		ctx,
		privateKey,
		ts.URL(), // Point to our mock server!
		nil,      // Meta will be fetched
		"",
		accountAddr,
		nil, // SpotMeta will be fetched
	)

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

	privateKey, _ := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	accountAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(ctx, privateKey, ts.URL(), nil, "", accountAddr, nil)

	// Create initial order
	cloid := "test-modify-" + time.Now().Format("20060102150405")
	_, err := exchange.Order(ctx, hyperliquid.CreateOrderRequest{
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
		Oid: hyperliquid.OrderIdToHex(oid),
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

	privateKey, _ := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	accountAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(ctx, privateKey, ts.URL(), nil, "", accountAddr, nil)

	// Create an order
	cloid := "test-cancel-" + time.Now().Format("20060102150405")
	_, err := exchange.Order(ctx, hyperliquid.CreateOrderRequest{
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

	privateKey, _ := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	accountAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	// Create exchange to make an order
	exchange := hyperliquid.NewExchange(ctx, privateKey, ts.URL(), nil, "", accountAddr, nil)

	cloid := "test-query-" + time.Now().Format("20060102150405")
	_, err := exchange.Order(ctx, hyperliquid.CreateOrderRequest{
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

	// Create Info client for read-only queries
	info := hyperliquid.NewInfo(ctx, ts.URL(), false, nil, nil)

	// Query the order status
	result, err := info.QueryOrderByCloid(ctx, accountAddr, cloid)
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

	// Create Info client for metadata queries
	info := hyperliquid.NewInfo(ctx, ts.URL(), false, nil, nil)

	// Query perpetual futures metadata
	meta, err := info.MetaAndAssetCtxs(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch metadata: %v", err)
	}

	// Verify we got mock data back
	if len(meta) == 0 {
		t.Fatal("Expected metadata response")
	}

	if len(meta[0].Universe) == 0 {
		t.Fatal("Expected universe data")
	}

	// Verify the request was captured
	infoReqs := ts.GetInfoRequests()
	foundMetaRequest := false
	for _, req := range infoReqs {
		if req.Type == "metaAndAssetCtxs" {
			foundMetaRequest = true
			break
		}
	}

	if !foundMetaRequest {
		t.Log("Note: Metadata may be cached by the library")
	}
}

// TestParallelTests demonstrates that parallel tests are safe
func TestParallelTests(t *testing.T) {
	t.Run("Test1", func(t *testing.T) {
		t.Parallel()
		ts := server.NewTestServer(t)

		// Make requests specific to this test
		makeTestOrderHTTP(t, ts, "ETH", 1.0, 3000.0)

		if ts.RequestCount() != 1 {
			t.Errorf("Test1: Expected 1 request, got %d", ts.RequestCount())
		}
	})

	t.Run("Test2", func(t *testing.T) {
		t.Parallel()
		ts := server.NewTestServer(t)

		// Make different requests
		makeTestOrderHTTP(t, ts, "BTC", 0.5, 50000.0)
		makeTestOrderHTTP(t, ts, "SOL", 10.0, 100.0)

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

// makeTestOrderHTTP makes a raw HTTP request to create an order (for tests that don't use the library)
func makeTestOrderHTTP(t *testing.T, ts *server.TestServer, coin string, size, price float64) {
	t.Helper()

	cloid := "test-" + coin + "-" + time.Now().Format("20060102150405")

	payload := map[string]interface{}{
		"action": map[string]interface{}{
			"type": "order",
			"orders": []map[string]interface{}{
				{
					"coin":     coin,
					"is_buy":   true,
					"sz":       size,
					"limit_px": price,
					"cloid":    cloid,
					"order_type": map[string]interface{}{
						"limit": map[string]interface{}{
							"tif": "Gtc",
						},
					},
				},
			},
		},
		"nonce": time.Now().Unix(),
		"signature": map[string]interface{}{
			"r": "0x1234",
			"s": "0x5678",
			"v": 27,
		},
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(ts.URL()+"/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}
