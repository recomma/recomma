package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/emitter"
	"github.com/terwey/recomma/metadata"
	"github.com/terwey/recomma/storage"
)

// fakeDealClient implements ThreeCommasAPI for HandleDeal tests.
type fakeDealClient struct {
	ordersByDeal map[int][]tc.MarketOrder
	errByDeal    map[int]error
}

func (f *fakeDealClient) ListBots(ctx context.Context, _ ...tc.ListBotsParamsOption) ([]tc.Bot, error) {
	return nil, nil
}
func (f *fakeDealClient) GetListOfDeals(ctx context.Context, _ ...tc.ListDealsParamsOption) ([]tc.Deal, error) {
	return nil, nil
}
func (f *fakeDealClient) GetMarketOrdersForDeal(ctx context.Context, id tc.DealPathId) ([]tc.MarketOrder, error) {
	dealID := int(id)
	if err := f.errByDeal[dealID]; err != nil {
		return nil, err
	}
	src := f.ordersByDeal[dealID]
	cp := make([]tc.MarketOrder, len(src))
	copy(cp, src)
	return cp, nil
}

type fakeEmitter struct{}

func (e *fakeEmitter) Emit(ctx context.Context, w emitter.OrderWork) error {
	return nil
}

func TestHandleDeal_TableDriven(t *testing.T) {
	t.Parallel()

	base := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name                string
		seedDeal            *tc.Deal // if nil, don't seed cache (to force cache-miss)
		key                 WorkKey
		orders              []tc.MarketOrder
		ordersErr           error
		wantErrIs           error
		wantSizeAfterFirst  int
		wantSizeAfterSecond int // if 0, assume equal to wantSizeAfterFirst
		verifySeenForOrders bool
	}{
		{
			name:     "cache miss returns ErrDealNotCached and persists nothing",
			seedDeal: nil,
			key:      WorkKey{DealID: 123, BotID: 9},
			orders: []tc.MarketOrder{
				{OrderId: "A1", CreatedAt: base, StatusString: tc.Active, OrderType: tc.BUY},
			},
			wantErrIs:           ErrDealNotCached,
			wantSizeAfterFirst:  0,
			wantSizeAfterSecond: 0,
		},
		{
			name:     "idempotent persistence with mixed orders",
			seedDeal: &tc.Deal{Id: 777, BotId: 42},
			key:      WorkKey{DealID: 777, BotID: 42},
			orders: []tc.MarketOrder{
				{OrderId: "1", CreatedAt: base, StatusString: tc.Active, OrderType: tc.BUY},
				{OrderId: "2", CreatedAt: base.Add(1 * time.Second), StatusString: tc.Active, OrderType: tc.SELL},
				{OrderId: "3", CreatedAt: base.Add(2 * time.Second), StatusString: tc.Filled, OrderType: tc.BUY},
				{OrderId: "4", CreatedAt: base.Add(3 * time.Second), StatusString: tc.Active, OrderType: tc.BUY},
			},
			wantSizeAfterFirst:  4, // all raw orders are persisted once
			wantSizeAfterSecond: 4, // idempotent on rerun
			verifySeenForOrders: true,
		},
		{
			name:                "no orders -> no persistence, no error",
			seedDeal:            &tc.Deal{Id: 9001, BotId: 1},
			key:                 WorkKey{DealID: 9001, BotID: 1},
			orders:              []tc.MarketOrder{},
			wantSizeAfterFirst:  0,
			wantSizeAfterSecond: 0,
		},
		{
			name:                "upstream GetMarketOrders error is surfaced",
			seedDeal:            &tc.Deal{Id: 314, BotId: 3},
			key:                 WorkKey{DealID: 314, BotID: 3},
			ordersErr:           errors.New("rate limited"),
			wantErrIs:           errors.New("rate limited"),
			wantSizeAfterFirst:  0,
			wantSizeAfterSecond: 0,
		},
		{
			name:                "zero DealID is a no-op",
			seedDeal:            nil,
			key:                 WorkKey{DealID: 0, BotID: 99},
			orders:              nil, // won't be called
			wantSizeAfterFirst:  0,
			wantSizeAfterSecond: 0,
		},
	}

	for _, tcse := range tests {
		tcse := tcse
		t.Run(tcse.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			store, err := storage.NewMemory()
			require.NoError(t, err)
			defer store.Close()

			client := &fakeDealClient{
				ordersByDeal: map[int][]tc.MarketOrder{},
				errByDeal:    map[int]error{},
			}
			if tcse.orders != nil {
				client.ordersByDeal[tcse.key.DealID] = tcse.orders
			}
			if tcse.ordersErr != nil {
				client.errByDeal[tcse.key.DealID] = tcse.ordersErr
			}
			em := &fakeEmitter{}
			e := NewEngine(client, store, em)
			if tcse.seedDeal != nil {
				e.dealCache.Store(tcse.seedDeal.Id, *tcse.seedDeal)
			}

			// First run
			err = e.HandleDeal(ctx, tcse.key)
			if tcse.wantErrIs != nil {
				require.Error(t, err)
				// For upstream errors, we can't guarantees errors.Is unless the same instance.
				// If wantErrIs is ErrDealNotCached sentinel, errors.Is will be true even when wrapped.
				if errors.Is(tcse.wantErrIs, ErrDealNotCached) {
					require.ErrorIs(t, err, ErrDealNotCached)
				} else {
					require.Contains(t, err.Error(), tcse.wantErrIs.Error())
				}
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tcse.wantSizeAfterFirst, store.Size(), "unexpected store size after first run")

			// Optional verification of seen keys (only when first run succeeded)
			if tcse.verifySeenForOrders && tcse.wantErrIs == nil {
				for _, o := range tcse.orders {
					md := metadata.Metadata{
						BotID:     tcse.key.BotID,
						DealID:    tcse.key.DealID,
						CreatedAt: o.CreatedAt,
					}
					err = md.SetOrderIDFromString(o.OrderId)
					require.NoError(t, err)
					require.Truef(t, store.SeenKey([]byte(md.String())), "expected seen key for order %q", o.OrderId)
				}
			}

			// Second run (idempotence)
			err = e.HandleDeal(ctx, tcse.key)
			if tcse.wantErrIs != nil {
				// If the first run errored, second should also error similarly (no state change).
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			wantSecond := tcse.wantSizeAfterSecond
			if wantSecond == 0 {
				wantSecond = tcse.wantSizeAfterFirst
			}
			require.Equal(t, wantSecond, store.Size(), "unexpected store size after second run")
		})
	}
}
