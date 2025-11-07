package hl_test

import (
	"context"
	"testing"
	"time"

	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/recomma"
	"github.com/stretchr/testify/require"
)

// TestBBOSubscription verifies that the WebSocket client can subscribe to BBO
// updates and receive market data.
func TestBBOSubscription(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Ensure BBO subscription for BTC
	wsClient.EnsureBBO("BTC")

	// Wait for initial BBO
	bbo := wsClient.WaitForBestBidOffer(ctx, "BTC")
	require.NotNil(t, bbo, "Should receive BBO for BTC")
	require.Equal(t, "BTC", bbo.Coin)
	require.Greater(t, bbo.Bid.Price, 0.0, "Bid price should be positive")
	require.Greater(t, bbo.Ask.Price, 0.0, "Ask price should be positive")
	require.Greater(t, bbo.Ask.Price, bbo.Bid.Price, "Ask should be higher than Bid")
}

// TestBBOMultipleCoins verifies that the WebSocket client can subscribe to
// BBO updates for multiple coins simultaneously.
func TestBBOMultipleCoins(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	coins := []string{"BTC", "ETH", "SOL", "ARB"}

	// Subscribe to all coins
	for _, coin := range coins {
		wsClient.EnsureBBO(coin)
	}

	// Verify we receive BBO for all coins
	for _, coin := range coins {
		coin := coin // capture
		bbo := wsClient.WaitForBestBidOffer(ctx, coin)
		require.NotNil(t, bbo, "Should receive BBO for %s", coin)
		require.Equal(t, coin, bbo.Coin)
		require.Greater(t, bbo.Bid.Price, 0.0)
		require.Greater(t, bbo.Ask.Price, 0.0)
	}
}

// TestBBOSubscriptionChannel verifies that the BBO subscription channel
// receives updates and properly broadcasts to multiple subscribers.
func TestBBOSubscriptionChannel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Subscribe to BBO channel
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	bboChan, err := wsClient.SubscribeBBO(subCtx, "ETH")
	require.NoError(t, err)
	require.NotNil(t, bboChan)

	// Should receive at least one BBO update
	select {
	case bbo := <-bboChan:
		require.Equal(t, "ETH", bbo.Coin)
		require.Greater(t, bbo.Bid.Price, 0.0)
		require.Greater(t, bbo.Ask.Price, 0.0)
	case <-time.After(5 * time.Second):
		t.Fatal("Should receive BBO update within 5 seconds")
	}
}

// TestBBOMultipleSubscribers verifies that multiple subscribers can receive
// the same BBO updates simultaneously.
func TestBBOMultipleSubscribers(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Create 3 subscribers to the same coin
	subCtx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	subCtx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	subCtx3, cancel3 := context.WithCancel(ctx)
	defer cancel3()

	bboChan1, err := wsClient.SubscribeBBO(subCtx1, "BTC")
	require.NoError(t, err)
	bboChan2, err := wsClient.SubscribeBBO(subCtx2, "BTC")
	require.NoError(t, err)
	bboChan3, err := wsClient.SubscribeBBO(subCtx3, "BTC")
	require.NoError(t, err)

	// All subscribers should receive BBO updates
	timeout := time.After(5 * time.Second)

	// Subscriber 1
	select {
	case bbo := <-bboChan1:
		require.Equal(t, "BTC", bbo.Coin)
	case <-timeout:
		t.Fatal("Subscriber 1 should receive BBO")
	}

	// Subscriber 2
	select {
	case bbo := <-bboChan2:
		require.Equal(t, "BTC", bbo.Coin)
	case <-timeout:
		t.Fatal("Subscriber 2 should receive BBO")
	}

	// Subscriber 3
	select {
	case bbo := <-bboChan3:
		require.Equal(t, "BTC", bbo.Coin)
	case <-timeout:
		t.Fatal("Subscriber 3 should receive BBO")
	}
}

// TestBBOCaseInsensitive verifies that BBO subscriptions are case-insensitive
// for coin symbols.
func TestBBOCaseInsensitive(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Subscribe with lowercase
	wsClient.EnsureBBO("btc")

	// Query with uppercase
	bbo := wsClient.WaitForBestBidOffer(ctx, "BTC")
	require.NotNil(t, bbo, "Should receive BBO regardless of case")
	require.Equal(t, "BTC", bbo.Coin, "Coin should be normalized to uppercase")

	// Subscribe with mixed case
	wsClient.EnsureBBO("EtH")
	bbo = wsClient.WaitForBestBidOffer(ctx, "eth")
	require.NotNil(t, bbo)
	require.Equal(t, "ETH", bbo.Coin)
}

// TestBBOTimeout verifies that WaitForBestBidOffer properly handles context
// timeout when BBO is not available.
func TestBBOTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Don't subscribe to BBO for this coin
	// Create a very short timeout context
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer timeoutCancel()

	// This should timeout because we haven't subscribed to FAKE coin
	bbo := wsClient.WaitForBestBidOffer(timeoutCtx, "FAKECOIN")
	require.Nil(t, bbo, "Should return nil when context times out")
}

// TestBBOSubscriptionCleanup verifies that BBO subscriptions are properly
// cleaned up when the context is canceled.
func TestBBOSubscriptionCleanup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Create subscription with cancelable context
	subCtx, subCancel := context.WithCancel(ctx)

	bboChan, err := wsClient.SubscribeBBO(subCtx, "SOL")
	require.NoError(t, err)

	// Receive first update
	select {
	case <-bboChan:
		// Got first update
	case <-time.After(5 * time.Second):
		t.Fatal("Should receive initial BBO")
	}

	// Cancel the subscription
	subCancel()

	// Channel should be closed
	select {
	case _, ok := <-bboChan:
		require.False(t, ok, "Channel should be closed after context cancel")
	case <-time.After(2 * time.Second):
		t.Fatal("Channel should close after context cancel")
	}
}

// TestBBOForIOCOrder verifies that BBO data can be used to price IOC
// (Immediate-or-Cancel) orders when no price is specified.
func TestBBOForIOCOrder(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	// Ensure BBO subscription
	wsClient.EnsureBBO("BTC")

	// Wait for BBO
	bboCtx, bboCancel := context.WithTimeout(ctx, 5*time.Second)
	defer bboCancel()

	bbo := wsClient.WaitForBestBidOffer(bboCtx, "BTC")
	require.NotNil(t, bbo, "Should receive BBO for pricing IOC order")

	// Verify BBO has valid bid and ask for IOC pricing
	require.Greater(t, bbo.Bid.Price, 0.0, "Bid price needed for sell IOC")
	require.Greater(t, bbo.Ask.Price, 0.0, "Ask price needed for buy IOC")
	require.Greater(t, bbo.Bid.Size, 0.0, "Bid size should be available")
	require.Greater(t, bbo.Ask.Size, 0.0, "Ask size should be available")

	// Verify spread is reasonable (ask > bid)
	spread := bbo.Ask.Price - bbo.Bid.Price
	require.Greater(t, spread, 0.0, "Spread should be positive")
}

// TestBBOConcurrentCoins verifies that BBO subscriptions for different coins
// don't interfere with each other.
func TestBBOConcurrentCoins(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)
	venueID := recomma.VenueID("test-venue-1")
	wallet := "0xtest"

	wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.URL())
	require.NoError(t, err)
	defer wsClient.Close()

	coins := []string{"BTC", "ETH", "SOL", "ARB", "DOGE"}

	// Subscribe to all coins concurrently
	type result struct {
		coin string
		bbo  *hl.BestBidOffer
	}

	results := make(chan result, len(coins))

	for _, coin := range coins {
		coin := coin // capture
		go func() {
			wsClient.EnsureBBO(coin)
			bboCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			bbo := wsClient.WaitForBestBidOffer(bboCtx, coin)
			results <- result{coin: coin, bbo: bbo}
		}()
	}

	// Collect results
	received := make(map[string]*hl.BestBidOffer)
	for i := 0; i < len(coins); i++ {
		select {
		case r := <-results:
			require.NotNil(t, r.bbo, "Should receive BBO for %s", r.coin)
			received[r.coin] = r.bbo
		case <-time.After(10 * time.Second):
			t.Fatal("Should receive all BBOs within timeout")
		}
	}

	// Verify all coins received distinct BBOs
	require.Len(t, received, len(coins), "Should receive BBO for all coins")
	for coin, bbo := range received {
		require.Equal(t, coin, bbo.Coin, "BBO coin should match subscription")
		require.Greater(t, bbo.Bid.Price, 0.0)
		require.Greater(t, bbo.Ask.Price, 0.0)
	}
}
