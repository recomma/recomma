package hl

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/recomma/recomma/orderid"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

type fakeInfo struct {
	results map[string]*hyperliquid.OrderQueryResult
	err     error
}

func (f *fakeInfo) QueryOrderByCloid(_ context.Context, cloid string) (*hyperliquid.OrderQueryResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if res, ok := f.results[cloid]; ok {
		return res, nil
	}
	return &hyperliquid.OrderQueryResult{Status: hyperliquid.OrderQueryStatusError}, nil
}

type fakeStatusStore struct {
	mu      sync.Mutex
	mds     []orderid.OrderId
	records map[string]hyperliquid.WsOrder
}

func (s *fakeStatusStore) ListHyperliquidOrderIds(context.Context) ([]orderid.OrderId, error) {
	return append([]orderid.OrderId(nil), s.mds...), nil
}

func (s *fakeStatusStore) RecordHyperliquidStatus(_ context.Context, oid orderid.OrderId, status hyperliquid.WsOrder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[string]hyperliquid.WsOrder)
	}
	s.records[oid.Hex()] = status
	return nil
}

func TestStatusRefresherRefresh(t *testing.T) {
	ctx := context.Background()

	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	cloid := oid.Hex()

	fillResult := &hyperliquid.OrderQueryResult{
		Status: hyperliquid.OrderQueryStatusSuccess,
		Order: hyperliquid.OrderQueryResponse{
			Order: hyperliquid.QueriedOrder{
				Coin:      "ETH",
				Side:      hyperliquid.OrderSideBid,
				LimitPx:   "2000",
				Sz:        "1",
				Oid:       123,
				Timestamp: time.Now().UnixMilli(),
				OrigSz:    "1",
				Cloid:     &cloid,
			},
			Status:          hyperliquid.OrderStatusValueFilled,
			StatusTimestamp: time.Now().UnixMilli(),
		},
	}

	info := &fakeInfo{
		results: map[string]*hyperliquid.OrderQueryResult{
			cloid: fillResult,
		},
	}

	store := &fakeStatusStore{mds: []orderid.OrderId{oid}}

	refresher := NewStatusRefresher(info, store, WithStatusRefresherConcurrency(1))

	require.NoError(t, refresher.Refresh(ctx))

	store.mu.Lock()
	defer store.mu.Unlock()

	recorded, ok := store.records[oid.Hex()]
	require.True(t, ok)
	require.Equal(t, hyperliquid.OrderStatusValueFilled, recorded.Status)
	require.Equal(t, cloid, *recorded.Order.Cloid)
}
