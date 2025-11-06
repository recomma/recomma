package server_test

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/recomma/hyperliquid-mock/server"
	"github.com/sonirico/go-hyperliquid"
)

// TestIntegrationWithGoHyperliquid demonstrates using the mock server with the real go-hyperliquid library
func TestIntegrationWithGoHyperliquid(t *testing.T) {
	// Create isolated test server
	ts := server.NewTestServer(t)
	ctx := context.Background()

	// Create a test private key
	privateKey, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("Failed to create private key: %v", err)
	}

	// Get wallet address from private key
	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	walletAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	// Create exchange client pointing to mock server
	exchange := hyperliquid.NewExchange(
		ctx,
		privateKey,
		ts.URL(), // Point to our mock server!
		nil,      // Meta will be fetched from mock
		"",       // vaultAddr (empty for non-vault accounts)
		walletAddr,
		nil, // SpotMeta will be fetched from mock
	)

	// Create an order using the real go-hyperliquid library
	cloid := "test-order-1"
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
		ClientOrderID: &cloid,
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
	order, exists := ts.GetOrder(cloid)
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

// TestOrderModificationWithGoHyperliquid demonstrates testing order modifications
func TestOrderModificationWithGoHyperliquid(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	privateKey, _ := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	walletAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(ctx, privateKey, ts.URL(), nil, "", walletAddr, nil)

	// Create initial order
	cloid := "test-modify-order"
	_, err := exchange.Order(ctx, hyperliquid.CreateOrderRequest{
		Coin:  "BTC",
		IsBuy: true,
		Size:  0.5,
		Price: 50000.0,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		ClientOrderID: &cloid,
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

	// Modify the order (use OID as hex string)
	oidHex := fmt.Sprintf("0x%x", oid)
	_, err = exchange.ModifyOrder(ctx, hyperliquid.ModifyOrderRequest{
		Oid: oidHex,
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

// TestOrderCancellationWithGoHyperliquid demonstrates testing order cancellations
func TestOrderCancellationWithGoHyperliquid(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	privateKey, _ := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	walletAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(ctx, privateKey, ts.URL(), nil, "", walletAddr, nil)

	// Create an order
	cloid := "test-cancel-order"
	_, err := exchange.Order(ctx, hyperliquid.CreateOrderRequest{
		Coin:  "SOL",
		IsBuy: false,
		Size:  10.0,
		Price: 100.0,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		ClientOrderID: &cloid,
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

// TestQueryOrderStatusWithGoHyperliquid demonstrates testing order status queries
func TestQueryOrderStatusWithGoHyperliquid(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	privateKey, _ := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	pub := privateKey.Public()
	pubECDSA, _ := pub.(*ecdsa.PublicKey)
	walletAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	// Create exchange to make an order
	exchange := hyperliquid.NewExchange(ctx, privateKey, ts.URL(), nil, "", walletAddr, nil)

	cloid := "test-query-order"
	_, err := exchange.Order(ctx, hyperliquid.CreateOrderRequest{
		Coin:  "ARB",
		IsBuy: true,
		Size:  100.0,
		Price: 1.5,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		ClientOrderID: &cloid,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Clear exchange request
	ts.ClearRequests()

	// Create Info client for read-only queries
	info := hyperliquid.NewInfo(ctx, ts.URL(), false, nil, nil)

	// Query the order status
	result, err := info.QueryOrderByCloid(ctx, walletAddr, cloid)
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

// TestMetadataFetchingWithGoHyperliquid demonstrates testing metadata queries
func TestMetadataFetchingWithGoHyperliquid(t *testing.T) {
	ts := server.NewTestServer(t)
	ctx := context.Background()

	// Create Info client for metadata queries
	info := hyperliquid.NewInfo(ctx, ts.URL(), false, nil, nil)

	// Query perpetual futures metadata
	meta, err := info.MetaAndAssetCtxs(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch metadata: %v", err)
	}

	// Verify we got mock data back (meta is a pointer)
	if meta == nil {
		t.Fatal("Expected metadata response")
	}

	if len(meta.Universe) == 0 {
		t.Fatal("Expected universe data")
	}

	// Verify BTC is in the mock universe
	foundBTC := false
	for _, asset := range meta.Universe {
		if asset.Name == "BTC" {
			foundBTC = true
			if asset.SzDecimals != 5 {
				t.Errorf("Expected BTC SzDecimals to be 5, got %d", asset.SzDecimals)
			}
			break
		}
	}

	if !foundBTC {
		t.Error("Expected to find BTC in universe")
	}
}
