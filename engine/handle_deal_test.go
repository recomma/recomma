package engine

import (
	"context"
	"strconv"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/adapter"
	"github.com/recomma/recomma/engine/orderscaler"
	"github.com/recomma/recomma/filltracker"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/internal/testutil"
	"github.com/recomma/recomma/metadata"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

type capturingEmitter struct {
	items []recomma.OrderWork
}

func (c *capturingEmitter) Emit(_ context.Context, w recomma.OrderWork) error {
	c.items = append(c.items, w)
	return nil
}

type staticConstraints struct {
	constraint hl.CoinConstraints
}

func (s staticConstraints) Resolve(_ context.Context, coin string) (hl.CoinConstraints, error) {
	out := s.constraint
	out.Coin = coin
	return out, nil
}

type harness struct {
	ctx     context.Context
	store   *storage.Storage
	engine  *Engine
	emitter *capturingEmitter
	deal    *tc.Deal
	key     WorkKey
}

func newHarness(t *testing.T, botID, dealID uint32, opts ...EngineOption) *harness {
	t.Helper()

	// storage.WithLogger(slog.Default())
	store, err := storage.New(":memory:")
	require.NoError(t, err)

	em := &capturingEmitter{}
	engineOpts := []EngineOption{WithStorage(store), WithEmitter(em)}
	engineOpts = append(engineOpts, opts...)
	engine := NewEngine(nil, engineOpts...)

	deal := &tc.Deal{
		Id:           int(dealID),
		BotId:        int(botID),
		ToCurrency:   "BTC",
		FromCurrency: "USDT",
	}

	return &harness{
		ctx:     context.Background(),
		store:   store,
		engine:  engine,
		emitter: em,
		deal:    deal,
		key:     WorkKey{DealID: dealID, BotID: botID},
	}
}

func TestProcessDeal_TableDriven(t *testing.T) {

	const (
		botID  = uint32(42)
		dealID = uint32(777)
	)
	base := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	activeEvent, activeMD := testutil.NewBotEvent(t, base, botID, dealID)
	modifyEvent, _ := testutil.NewBotEvent(
		t,
		base.Add(30*time.Second),
		botID,
		dealID,
		testutil.WithType(tc.MarketOrderOrderType(tc.SELL)),
	)

	cancelEvent := func(ts time.Time) tc.BotEvent {
		evt, _ := testutil.NewBotEvent(
			t,
			ts,
			botID,
			dealID,
			testutil.WithAction(tc.BotEventActionCancel),
			testutil.WithStatus(tc.MarketOrderStatusString(tc.Cancelled)),
		)
		return evt
	}

	type scenario struct {
		name         string
		events       []tc.BotEvent
		prepare      func(t *testing.T, h *harness)
		wantActions  []recomma.ActionType
		wantStatuses []tc.MarketOrderStatusString
	}

	cases := []scenario{
		{
			name:        "fresh create",
			events:      []tc.BotEvent{activeEvent},
			wantActions: []recomma.ActionType{recomma.ActionCreate},
			wantStatuses: []tc.MarketOrderStatusString{
				tc.MarketOrderStatusString(tc.Active),
			},
		},
		{
			name:        "cancel ignored when never created",
			events:      []tc.BotEvent{cancelEvent(base.Add(time.Minute))},
			wantActions: nil,
			wantStatuses: []tc.MarketOrderStatusString{
				tc.MarketOrderStatusString(tc.Cancelled),
			},
		},
		{
			name:   "cancel emitted after local create",
			events: []tc.BotEvent{cancelEvent(base.Add(2 * time.Minute))},
			prepare: func(t *testing.T, h *harness) {
				inserted, err := h.store.RecordThreeCommasBotEvent(h.ctx, activeMD, activeEvent)
				require.NoError(t, err)
				require.NotZero(t, inserted)

				be := recomma.BotEvent{
					RowID:    inserted,
					BotEvent: activeEvent,
				}

				createReq := adapter.ToCreateOrderRequest(h.deal.ToCurrency, be, activeMD)
				require.NoError(t, h.store.RecordHyperliquidOrderRequest(h.ctx, activeMD, createReq, inserted))
			},
			wantActions: []recomma.ActionType{recomma.ActionCancel},
			wantStatuses: []tc.MarketOrderStatusString{
				tc.MarketOrderStatusString(tc.Active),
				tc.MarketOrderStatusString(tc.Cancelled),
			},
		},
		{
			name:   "modify emitted after local create",
			events: []tc.BotEvent{modifyEvent},
			prepare: func(t *testing.T, h *harness) {
				inserted, err := h.store.RecordThreeCommasBotEvent(h.ctx, activeMD, activeEvent)
				require.NoError(t, err)
				require.NotZero(t, inserted)

				be := recomma.BotEvent{
					RowID:    inserted,
					BotEvent: activeEvent,
				}

				createReq := adapter.ToCreateOrderRequest(h.deal.ToCurrency, be, activeMD)
				require.NoError(t, h.store.RecordHyperliquidOrderRequest(h.ctx, activeMD, createReq, inserted))
			},
			wantActions: []recomma.ActionType{recomma.ActionModify},
			wantStatuses: []tc.MarketOrderStatusString{
				tc.MarketOrderStatusString(tc.Active),
				tc.MarketOrderStatusString(tc.Active),
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, botID, dealID)
			defer h.store.Close()

			if tc.prepare != nil {
				tc.prepare(t, h)
			}

			err := h.engine.processDeal(h.ctx, h.key, h.deal.ToCurrency, tc.events)
			require.NoError(t, err)

			require.Len(t, h.emitter.items, len(tc.wantActions))
			for i, want := range tc.wantActions {
				require.Equal(t, want, h.emitter.items[i].Action.Type)
				require.NotNil(t, h.emitter.items[i].BotEvent.CreatedAt)
				require.NotEmpty(t, h.emitter.items[i].BotEvent.Coin)
			}

			// For history assertions, use the fingerprint of the last event we fed.
			fp := tc.events[len(tc.events)-1].FingerprintAsID()
			history, err := h.store.ListEventsForOrder(h.ctx, h.key.BotID, h.key.DealID, fp)
			require.NoError(t, err)
			require.Len(t, history, len(tc.wantStatuses))
			for i, want := range tc.wantStatuses {
				require.Equal(t, want, history[i].Status)
			}
		})
	}
}

func TestProcessDeal_TakeProfitSizedFromTracker(t *testing.T) {
	t.Parallel()

	const (
		botID  = uint32(99)
		dealID = uint32(1001)
		coin   = "BTC"
	)

	base := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)

	h := newHarness(t, botID, dealID)
	defer h.store.Close()

	require.NoError(t, h.store.RecordThreeCommasDeal(h.ctx, tc.Deal{
		Id:         int(dealID),
		BotId:      int(botID),
		CreatedAt:  base,
		UpdatedAt:  base,
		ToCurrency: coin,
	}))

	baseEvent, baseMD := testutil.NewBotEvent(t, base, botID, dealID,
		testutil.WithAction(tc.BotEventActionExecute),
		testutil.WithStatus(tc.MarketOrderStatusString(tc.Filled)),
		testutil.WithPrice(10),
		testutil.WithSize(5),
		testutil.WithOrderType(tc.MarketOrderDealOrderTypeBase),
	)
	_, err := h.store.RecordThreeCommasBotEvent(h.ctx, baseMD, baseEvent)
	require.NoError(t, err)
	require.NoError(t, h.store.RecordHyperliquidStatus(h.ctx, baseMD, makeWsStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 5, 0, 10, base.Add(time.Second))))

	tracker := filltracker.New(h.store, nil)
	require.NoError(t, tracker.Rebuild(h.ctx))

	h.engine = NewEngine(nil, WithStorage(h.store), WithEmitter(h.emitter), WithFillTracker(tracker))

	tpEvent, _ := testutil.NewBotEvent(t, base.Add(2*time.Minute), botID, dealID,
		testutil.WithOrderType(tc.MarketOrderDealOrderTypeTakeProfit),
		testutil.WithType(tc.MarketOrderOrderType(tc.SELL)),
		testutil.WithPrice(12.5),
		testutil.WithSize(9.5),
	)

	err = h.engine.processDeal(h.ctx, h.key, coin, []tc.BotEvent{tpEvent})
	require.NoError(t, err)

	require.Len(t, h.emitter.items, 1)
	got := h.emitter.items[0].Action
	require.Equal(t, recomma.ActionCreate, got.Type)
	require.NotNil(t, got.Create)
	require.InDelta(t, 5.0, got.Create.Size, 1e-6)
	require.True(t, got.Create.ReduceOnly)
	require.InDelta(t, 12.5, got.Create.Price, 1e-6)
}

func TestProcessDeal_AppliesOrderScaler(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		botID  = uint32(99)
		dealID = uint32(1234)
	)

	h := newHarness(t, botID, dealID)
	scaler := orderscaler.New(h.store, staticConstraints{constraint: hl.CoinConstraints{SizeStep: 0.1, PriceSigFigs: 5}}, nil)
	h.engine = NewEngine(nil, WithStorage(h.store), WithEmitter(h.emitter), WithOrderScaler(scaler))

	_, err := h.store.UpsertOrderScaler(ctx, 0.5, "tester", nil)
	require.NoError(t, err)

	base := time.Now().UTC()
	event, md := testutil.NewBotEvent(t, base, botID, dealID,
		testutil.WithPrice(12.34),
		testutil.WithSize(2.0),
	)

	err = h.engine.processDeal(ctx, h.key, "BTC", []tc.BotEvent{event})
	require.NoError(t, err)

	require.Len(t, h.emitter.items, 1)
	work := h.emitter.items[0]
	require.NotNil(t, work.Action.Create)
	require.InDelta(t, 1.0, work.Action.Create.Size, 1e-6)
	require.InDelta(t, 12.34, work.Action.Create.Price, 1e-6)
	require.InDelta(t, 1.0, work.BotEvent.Size, 1e-6)

	audits, err := h.store.ListScaledOrdersByMetadata(ctx, md)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.InDelta(t, 2.0, audits[0].OriginalSize, 1e-6)
	require.InDelta(t, 1.0, audits[0].ScaledSize, 1e-6)
	require.False(t, audits[0].Skipped)
	require.Nil(t, audits[0].SkipReason)
}

func TestProcessDeal_ScaledOrderBelowMinimumRecordsAudit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		botID  = uint32(55)
		dealID = uint32(990)
	)

	h := newHarness(t, botID, dealID)
	scaler := orderscaler.New(h.store, staticConstraints{constraint: hl.CoinConstraints{SizeStep: 0.01, PriceSigFigs: 5, MinNotional: 50}}, nil)
	h.engine = NewEngine(nil, WithStorage(h.store), WithEmitter(h.emitter), WithOrderScaler(scaler))

	_, err := h.store.UpsertOrderScaler(ctx, 1.0, "tester", nil)
	require.NoError(t, err)

	base := time.Now().UTC()
	event, md := testutil.NewBotEvent(t, base, botID, dealID,
		testutil.WithPrice(10.0),
		testutil.WithSize(1.0),
	)

	err = h.engine.processDeal(ctx, h.key, "BTC", []tc.BotEvent{event})
	require.NoError(t, err)

	require.Empty(t, h.emitter.items)

	audits, listErr := h.store.ListScaledOrdersByMetadata(ctx, md)
	require.NoError(t, listErr)
	require.Len(t, audits, 1)
	require.True(t, audits[0].Skipped)
	require.NotNil(t, audits[0].SkipReason)
	require.Contains(t, *audits[0].SkipReason, "below minimum notional")
}

func makeWsStatus(md metadata.Metadata, coin, side string, status hyperliquid.OrderStatusValue, original, remaining, limit float64, ts time.Time) hyperliquid.WsOrder {
	return hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      coin,
			Side:      side,
			LimitPx:   formatFloat(limit),
			Sz:        formatFloat(remaining),
			Oid:       ts.UnixNano(),
			Timestamp: ts.UnixMilli(),
			OrigSz:    formatFloat(original),
			Cloid:     md.HexAsPointer(),
		},
		Status:          status,
		StatusTimestamp: ts.UnixMilli(),
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
