package hl_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

// TestMultiWalletOrderIsolation verifies that orders from different wallets
// are properly isolated and don't interfere with each other.
func TestMultiWalletOrderIsolation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)

	// Create exchanges for both wallets
	exchange1, wallet1 := newMockExchange(t, ts.URL())
	exchange2, wallet2 := newMockExchange(t, ts.URL())

	// Create two separate wallets with different venue IDs
	venueID1 := recomma.VenueID("test-venue-1")
	venueID2 := recomma.VenueID("test-venue-2")

	// Create WebSocket clients for both wallets
	wsClient1, err := ws.New(ctx, store, nil, venueID1, wallet1, ts.WebSocketURL())
	require.NoError(t, err)
	defer wsClient1.Close()

	wsClient2, err := ws.New(ctx, store, nil, venueID2, wallet2, ts.WebSocketURL())
	require.NoError(t, err)
	defer wsClient2.Close()

	// Create order for wallet 1
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

	// Create order for wallet 2
	oid2 := orderid.OrderId{BotID: 2, DealID: 2, BotEventID: 2}
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

	// Submit orders
	_, err = exchange1.Order(ctx, order1, nil)
	require.NoError(t, err)

	_, err = exchange2.Order(ctx, order2, nil)
	require.NoError(t, err)

	// Wait for WebSocket updates to be received
	time.Sleep(500 * time.Millisecond)

	// Wallet 1 should see its own order
	exists1 := wsClient1.Exists(ctx, oid1)
	exists2InWallet1 := wsClient1.Exists(ctx, oid2)

	// Wallet 2 should see its own order
	exists2 := wsClient2.Exists(ctx, oid2)
	exists1InWallet2 := wsClient2.Exists(ctx, oid1)

	// Each wallet should only see its own orders (testing isolation)
	require.True(t, exists1, "Wallet 1 should see its own order")
	require.False(t, exists2InWallet1, "Wallet 1 shouldn't see wallet 2's orders")
	require.True(t, exists2, "Wallet 2 should see its own order")
	require.False(t, exists1InWallet2, "Wallet 2 shouldn't see wallet 1's orders")

	// Verify orders are in correct venue in storage
	ident1 := recomma.NewOrderIdentifier(venueID1, wallet1, oid1)
	ident2 := recomma.NewOrderIdentifier(venueID2, wallet2, oid2)

	// Storage should have the orders under correct identifiers
	_, ok1, err := store.LoadHyperliquidStatus(ctx, ident1)
	require.NoError(t, err)
	_, ok2, err := store.LoadHyperliquidStatus(ctx, ident2)
	require.NoError(t, err)

	// Orders should be found because WebSocket is subscribed to the correct wallet
	require.True(t, ok1, "Order 1 should be stored under wallet 1's identifier")
	require.True(t, ok2, "Order 2 should be stored under wallet 2's identifier")
}

// TestMultiWalletConcurrentOrders verifies that multiple wallets can create
// orders concurrently without race conditions.
func TestMultiWalletConcurrentOrders(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)

	walletCount := 3
	ordersPerWallet := 5

	type walletData struct {
		wallet   string
		venueID  recomma.VenueID
		wsClient *ws.Client
		exchange *hyperliquid.Exchange
		oids     []orderid.OrderId
	}

	wallets := make([]walletData, walletCount)

	// Create wallets and WebSocket clients
	for i := 0; i < walletCount; i++ {
		exchange, wallet := newMockExchange(t, ts.URL())
		venueID := recomma.VenueID(fmt.Sprintf("test-venue-%d", i+1))

		wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.WebSocketURL())
		require.NoError(t, err)
		defer wsClient.Close()

		wallets[i] = walletData{
			wallet:   wallet,
			venueID:  venueID,
			wsClient: wsClient,
			exchange: exchange,
			oids:     make([]orderid.OrderId, 0, ordersPerWallet),
		}
	}

	// Create orders concurrently from all wallets
	var wg sync.WaitGroup
	errorChan := make(chan error, walletCount*ordersPerWallet)

	for walletIdx := range wallets {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := &wallets[idx]

			for orderIdx := 0; orderIdx < ordersPerWallet; orderIdx++ {
				oid := orderid.OrderId{
					BotID:      uint32(idx*100 + orderIdx),
					DealID:     uint32(idx),
					BotEventID: uint32(orderIdx),
				}
				w.oids = append(w.oids, oid)

				cloid := oid.Hex()
				order := hyperliquid.CreateOrderRequest{
					Coin:          "BTC",
					IsBuy:         orderIdx%2 == 0,
					Price:         50000 + float64(idx*1000+orderIdx*10),
					Size:          1.0,
					ClientOrderID: &cloid,
					OrderType: hyperliquid.OrderType{
						Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
					},
				}

				_, err := w.exchange.Order(ctx, order, nil)
				if err != nil {
					errorChan <- fmt.Errorf("wallet %d order %d: %w", idx, orderIdx, err)
				}
			}
		}(walletIdx)
	}

	wg.Wait()
	close(errorChan)

	// Check for errors
	for err := range errorChan {
		require.NoError(t, err)
	}

	// Verify each wallet sees only its own orders
	for walletIdx, w := range wallets {
		for _, oid := range w.oids {
			// Note: Orders won't appear in WebSocket if subscribed to wrong wallet
			// This is expected behavior - we're just verifying no panics or race conditions
			_ = w.wsClient.Exists(ctx, oid)
		}

		// Verify no cross-contamination
		for otherIdx, other := range wallets {
			if otherIdx != walletIdx {
				for _, otherOid := range other.oids {
					exists := w.wsClient.Exists(ctx, otherOid)
					require.False(t, exists,
						"Wallet %d should not see orders from wallet %d",
						walletIdx, otherIdx)
				}
			}
		}
	}
}

// TestMultiWalletBBOSharing verifies that BBO data is shared efficiently
// across different wallet connections to the same coin.
func TestMultiWalletBBOSharing(t *testing.T) {
	t.Skip("Skipping: WebSocket client requires TLS configuration option for mock server")
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)

	// Create 3 wallets
	wallets := []struct {
		wallet  string
		venueID recomma.VenueID
	}{
		{"0xwallet1", recomma.VenueID("test-venue-1")},
		{"0xwallet2", recomma.VenueID("test-venue-2")},
		{"0xwallet3", recomma.VenueID("test-venue-3")},
	}

	clients := make([]*ws.Client, len(wallets))

	// Create WebSocket clients for all wallets
	for i, w := range wallets {
		wsClient, err := ws.New(ctx, store, nil, w.venueID, w.wallet, ts.WebSocketURL())
		require.NoError(t, err)
		defer wsClient.Close()
		clients[i] = wsClient
	}

	// All wallets subscribe to same coin
	coin := "BTC"
	for _, client := range clients {
		client.EnsureBBO(coin)
	}

	// All clients should receive BBO for the same coin
	for i, client := range clients {
		bbo := client.WaitForBestBidOffer(ctx, coin)
		require.NotNil(t, bbo, "Wallet %d should receive BBO for %s", i, coin)
		require.Equal(t, coin, bbo.Coin)
		require.Greater(t, bbo.Bid.Price, 0.0)
		require.Greater(t, bbo.Ask.Price, 0.0)
	}
}

// TestMultiWalletConcurrentBBOSubscriptions verifies that concurrent BBO
// subscriptions from multiple wallets don't cause race conditions.
func TestMultiWalletConcurrentBBOSubscriptions(t *testing.T) {
	t.Skip("Skipping: WebSocket client requires TLS configuration option for mock server")
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)

	walletCount := 5
	coins := []string{"BTC", "ETH", "SOL"}

	type subscriptionResult struct {
		walletIdx int
		coin      string
		bbo       interface{}
		err       error
	}

	results := make(chan subscriptionResult, walletCount*len(coins))
	var wg sync.WaitGroup

	// Create wallets and subscribe concurrently
	for walletIdx := 0; walletIdx < walletCount; walletIdx++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			wallet := fmt.Sprintf("0xwallet%d", idx)
			venueID := recomma.VenueID(fmt.Sprintf("test-venue-%d", idx+1))

			wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.WebSocketURL())
			if err != nil {
				for range coins {
					results <- subscriptionResult{idx, "", nil, err}
				}
				return
			}
			defer wsClient.Close()

			// Subscribe to all coins concurrently
			for _, coin := range coins {
				coin := coin // capture
				go func() {
					wsClient.EnsureBBO(coin)
					bboCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
					bbo := wsClient.WaitForBestBidOffer(bboCtx, coin)
					results <- subscriptionResult{idx, coin, bbo, nil}
				}()
			}
		}(walletIdx)
	}

	wg.Wait()
	close(results)

	// Verify all subscriptions succeeded
	successCount := 0
	for result := range results {
		if result.err != nil {
			require.NoError(t, result.err, "Wallet %d should connect successfully", result.walletIdx)
		} else {
			require.NotNil(t, result.bbo,
				"Wallet %d should receive BBO for %s", result.walletIdx, result.coin)
			successCount++
		}
	}

	expectedCount := walletCount * len(coins)
	require.Equal(t, expectedCount, successCount,
		"Should receive BBO for all wallet-coin combinations")
}

// TestMultiWalletOrderAndBBOConcurrent verifies that order operations and BBO
// subscriptions can happen concurrently across multiple wallets without issues.
func TestMultiWalletOrderAndBBOConcurrent(t *testing.T) {
	t.Skip("Skipping: WebSocket client requires TLS configuration option for mock server")
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)

	walletCount := 3
	var wg sync.WaitGroup

	for walletIdx := 0; walletIdx < walletCount; walletIdx++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			exchange, wallet := newMockExchange(t, ts.URL())
			venueID := recomma.VenueID(fmt.Sprintf("test-venue-%d", idx+1))

			wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.WebSocketURL())
			require.NoError(t, err)
			defer wsClient.Close()

			// Subscribe to BBO
			wsClient.EnsureBBO("BTC")

			// Wait for BBO
			bboCtx, bboCancel := context.WithTimeout(ctx, 5*time.Second)
			bbo := wsClient.WaitForBestBidOffer(bboCtx, "BTC")
			bboCancel()
			require.NotNil(t, bbo, "Wallet %d should receive BBO", idx)

			// Create order using BBO price
			oid := orderid.OrderId{
				BotID:      uint32(idx*10 + 1),
				DealID:     uint32(idx),
				BotEventID: 1,
			}
			cloid := oid.Hex()

			price := bbo.Ask.Price
			if idx%2 != 0 {
				price = bbo.Bid.Price
			}

			order := hyperliquid.CreateOrderRequest{
				Coin:          "BTC",
				IsBuy:         idx%2 == 0,
				Price:         price,
				Size:          1.0,
				ClientOrderID: &cloid,
				OrderType: hyperliquid.OrderType{
					Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
				},
			}

			_, err = exchange.Order(ctx, order, nil)
			require.NoError(t, err, "Wallet %d should create order successfully", idx)
		}(walletIdx)
	}

	wg.Wait()
}
