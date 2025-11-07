package hl_test

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"sync"
	"testing"
	"time"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestStatusRefresherWithMockServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
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
	store.RecordOrder(oid1)
	store.RecordOrder(oid2)

	// Run status refresh
	refresher := hl.NewStatusRefresher(info, store, hl.WithStatusRefresherConcurrency(2))
	require.NoError(t, refresher.Refresh(ctx))

	// Verify statuses were stored
	status1, found := store.Status(oid1)
	require.True(t, found, "expected status1 to be stored")
	require.Equal(t, "BTC", status1.Order.Coin)

	status2, found := store.Status(oid2)
	require.True(t, found, "expected status2 to be stored")
	require.Equal(t, "ETH", status2.Order.Coin)
}

func TestStatusRefresherHandlesFilledOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newIntegrationTestStore(t)

	oid := orderid.OrderId{BotID: 5, DealID: 10, BotEventID: 15}
	cloid := oid.Hex()
	store.RecordOrder(oid)

	filledOrder := hyperliquid.OrderQueryResponse{
		Status: hyperliquid.OrderStatusValueFilled,
		Order: hyperliquid.QueriedOrder{
			Coin:      "SOL",
			Side:      hyperliquid.OrderSideBid,
			LimitPx:   "100",
			Sz:        "10",
			Oid:       12345,
			Timestamp: time.Now().UnixMilli(),
			Cloid:     &cloid,
		},
	}

	info := &stubOrderStatusClient{
		responses: map[string]*hyperliquid.OrderQueryResult{
			cloid: {
				Status: hyperliquid.OrderQueryStatusSuccess,
				Order:  filledOrder,
			},
		},
	}

	refresher := hl.NewStatusRefresher(info, store)
	require.NoError(t, refresher.Refresh(ctx))

	status, found := store.Status(oid)
	require.True(t, found)
	require.Equal(t, hyperliquid.OrderStatusValueFilled, status.Status)
	require.Equal(t, "SOL", status.Order.Coin)
}

func TestStatusRefresherHandlesCanceledOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
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

	store.RecordOrder(oid)

	// Cancel the order
	_, err = exchange.CancelByCloid(ctx, order.Coin, cloid)
	require.NoError(t, err)

	refresher := hl.NewStatusRefresher(info, store)
	require.NoError(t, refresher.Refresh(ctx))

	status, found := store.Status(oid)
	require.True(t, found)
	require.Equal(t, hyperliquid.OrderStatusValueCanceled, status.Status)
}

func TestStatusRefresherWithFillTracker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
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

	store.RecordOrder(oid)

	refresher := hl.NewStatusRefresher(info, store, hl.WithStatusRefresherTracker(tracker))
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

	info := hl.NewInfo(ctx, hl.ClientConfig{
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
		store.RecordOrder(oid)
	}

	// Refresh with concurrency
	refresher := hl.NewStatusRefresher(info, store, hl.WithStatusRefresherConcurrency(4))
	require.NoError(t, refresher.Refresh(ctx))

	// Verify all orders were refreshed
	for _, oid := range oids {
		status, found := store.Status(oid)
		require.True(t, found, "expected status for order %s", oid.Hex())
		require.NotNil(t, status)
	}
}

func TestStatusRefresherTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newIntegrationTestStore(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
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

	store.RecordOrder(oid)

	// Set a very short timeout - might fail but should not panic
	refresher := hl.NewStatusRefresher(info, store,
		hl.WithStatusRefresherTimeout(1*time.Nanosecond),
		hl.WithStatusRefresherConcurrency(1),
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

type integrationStatusStore struct {
	mu       sync.Mutex
	oids     []orderid.OrderId
	statuses map[string]hyperliquid.WsOrder
}

func newIntegrationTestStore(t *testing.T) *integrationStatusStore {
	t.Helper()
	return &integrationStatusStore{
		statuses: make(map[string]hyperliquid.WsOrder),
	}
}

func (s *integrationStatusStore) RecordOrder(oid orderid.OrderId) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oids = append(s.oids, oid)
}

func (s *integrationStatusStore) ListHyperliquidOrderIds(context.Context) ([]orderid.OrderId, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oids := make([]orderid.OrderId, len(s.oids))
	copy(oids, s.oids)
	return oids, nil
}

func (s *integrationStatusStore) RecordHyperliquidStatus(_ context.Context, oid orderid.OrderId, status hyperliquid.WsOrder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[oid.Hex()] = status
	return nil
}

func (s *integrationStatusStore) Status(oid orderid.OrderId) (hyperliquid.WsOrder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, ok := s.statuses[oid.Hex()]
	return status, ok
}

type stubOrderStatusClient struct {
	responses map[string]*hyperliquid.OrderQueryResult
}

func (s *stubOrderStatusClient) QueryOrderByCloid(_ context.Context, cloid string) (*hyperliquid.OrderQueryResult, error) {
	if s == nil {
		return nil, errors.New("stub not initialized")
	}
	if res, ok := s.responses[cloid]; ok {
		return res, nil
	}
	return &hyperliquid.OrderQueryResult{Status: hyperliquid.OrderQueryStatusError}, nil
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
