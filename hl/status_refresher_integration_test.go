package hl

import (
	"context"
	"crypto/ecdsa"
	"path/filepath"
	"testing"
	"time"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestStatusRefresherWithMockServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid1 := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	oid2 := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 4}

	// Create orders in mock server
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

	exchange := newMockExchange(t, ts.URL())
	_, err := exchange.Order(ctx, order1, nil)
	require.NoError(t, err)
	_, err = exchange.Order(ctx, order2, nil)
	require.NoError(t, err)

	// Record orders in storage
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, oid1, order1, 1))
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, oid2, order2, 2))

	// Run status refresh
	refresher := NewStatusRefresher(info, store, WithStatusRefresherConcurrency(2))
	require.NoError(t, refresher.Refresh(ctx))

	// Verify statuses were stored
	status1, found, err := store.LoadHyperliquidStatus(ctx, oid1)
	require.NoError(t, err)
	require.True(t, found, "expected status1 to be stored")
	require.Equal(t, "BTC", status1.Order.Coin)

	status2, found, err := store.LoadHyperliquidStatus(ctx, oid2)
	require.NoError(t, err)
	require.True(t, found, "expected status2 to be stored")
	require.Equal(t, "ETH", status2.Order.Coin)
}

func TestStatusRefresherHandlesFilledOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid := orderid.OrderId{BotID: 5, DealID: 10, BotEventID: 15}
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

	exchange := newMockExchange(t, ts.URL())
	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, oid, order, 1))

	// Simulate order fill by updating mock server state
	ts.FillOrder(cloid, 100)

	refresher := NewStatusRefresher(info, store)
	require.NoError(t, refresher.Refresh(ctx))

	status, found, err := store.LoadHyperliquidStatus(ctx, oid)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, hyperliquid.OrderStatusValueFilled, status.Status)
}

func TestStatusRefresherHandlesCanceledOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid := orderid.OrderId{BotID: 20, DealID: 30, BotEventID: 40}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "DOGE",
		IsBuy:         true,
		Price:         0.15,
		Size:          1000.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	exchange := newMockExchange(t, ts.URL())
	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, oid, order, 1))

	// Cancel the order
	_, err = exchange.CancelByCloid(ctx, order.Coin, cloid)
	require.NoError(t, err)

	refresher := NewStatusRefresher(info, store)
	require.NoError(t, refresher.Refresh(ctx))

	status, found, err := store.LoadHyperliquidStatus(ctx, oid)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, hyperliquid.OrderStatusValueCanceled, status.Status)
}

func TestStatusRefresherWithFillTracker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	tracker := &mockStatusTracker{updates: make(map[string]hyperliquid.WsOrder)}

	oid := orderid.OrderId{BotID: 100, DealID: 200, BotEventID: 300}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ARB",
		IsBuy:         true,
		Price:         1.5,
		Size:          100.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	exchange := newMockExchange(t, ts.URL())
	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, oid, order, 1))

	refresher := NewStatusRefresher(info, store, WithStatusRefresherTracker(tracker))
	require.NoError(t, refresher.Refresh(ctx))

	// Verify tracker was updated
	status, found := tracker.updates[oid.Hex()]
	require.True(t, found, "expected tracker to receive status update")
	require.Equal(t, "ARB", status.Order.Coin)
}

func TestStatusRefresherConcurrentRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	exchange := newMockExchange(t, ts.URL())

	// Create 10 orders
	const orderCount = 10
	oids := make([]orderid.OrderId, orderCount)
	for i := 0; i < orderCount; i++ {
		oid := orderid.OrderId{BotID: uint32(i + 1), DealID: 1, BotEventID: 1}
		oids[i] = oid
		cloid := oid.Hex()

		order := hyperliquid.CreateOrderRequest{
			Coin:          "BTC",
			IsBuy:         true,
			Price:         50000 + float64(i*100),
			Size:          0.1,
			ClientOrderID: &cloid,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		}

		_, err := exchange.Order(ctx, order, nil)
		require.NoError(t, err)
		require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, oid, order, int64(i+1)))
	}

	// Refresh with concurrency
	refresher := NewStatusRefresher(info, store, WithStatusRefresherConcurrency(4))
	require.NoError(t, refresher.Refresh(ctx))

	// Verify all orders were refreshed
	for _, oid := range oids {
		status, found, err := store.LoadHyperliquidStatus(ctx, oid)
		require.NoError(t, err)
		require.True(t, found, "expected status for order %s", oid.Hex())
		require.NotNil(t, status)
	}
}

func TestStatusRefresherTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

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

	exchange := newMockExchange(t, ts.URL())
	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, oid, order, 1))

	// Set a very short timeout - might fail but should not panic
	refresher := NewStatusRefresher(info, store,
		WithStatusRefresherTimeout(1*time.Nanosecond),
		WithStatusRefresherConcurrency(1),
	)
	_ = refresher.Refresh(ctx) // May error due to timeout, that's expected
}

// Helper types and functions

type mockStatusTracker struct {
	updates map[string]hyperliquid.WsOrder
}

func (m *mockStatusTracker) UpdateStatus(_ context.Context, oid orderid.OrderId, status hyperliquid.WsOrder) error {
	m.updates[oid.Hex()] = status
	return nil
}

func newIntegrationTestStore(t *testing.T) *storage.Storage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "integration.db")
	store, err := storage.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newMockExchange(t *testing.T, baseURL string) *hyperliquid.Exchange {
	t.Helper()

	ctx := context.Background()

	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	require.True(t, ok, "expected ECDSA public key")

	walletAddr := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(
		ctx,
		privateKey,
		baseURL,
		nil,
		"",
		walletAddr,
		nil,
	)

	return exchange
}
