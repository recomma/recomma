package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/adapter"
	"github.com/terwey/recomma/internal/testutil"
	"github.com/terwey/recomma/recomma"
	"github.com/terwey/recomma/storage"
)

type capturingEmitter struct {
	items []recomma.OrderWork
}

func (c *capturingEmitter) Emit(_ context.Context, w recomma.OrderWork) error {
	c.items = append(c.items, w)
	return nil
}

type harness struct {
	ctx     context.Context
	store   *storage.Storage
	engine  *Engine
	emitter *capturingEmitter
	deal    *tc.Deal
	key     WorkKey
}

func newHarness(t *testing.T, botID, dealID uint32) *harness {
	t.Helper()

	store, err := storage.New(":memory:", nil)
	require.NoError(t, err)

	em := &capturingEmitter{}
	engine := NewEngine(nil, store, em)

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
				inserted, err := h.store.RecordThreeCommasBotEvent(activeMD, activeEvent)
				require.NoError(t, err)
				require.NotZero(t, inserted)

				be := recomma.BotEvent{
					RowID:    inserted,
					BotEvent: activeEvent,
				}

				createReq := adapter.ToCreateOrderRequest(h.deal, be, activeMD)
				require.NoError(t, h.store.RecordHyperliquidOrderRequest(activeMD, createReq, inserted))
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
				inserted, err := h.store.RecordThreeCommasBotEvent(activeMD, activeEvent)
				require.NoError(t, err)
				require.NotZero(t, inserted)

				be := recomma.BotEvent{
					RowID:    inserted,
					BotEvent: activeEvent,
				}

				createReq := adapter.ToCreateOrderRequest(h.deal, be, activeMD)
				require.NoError(t, h.store.RecordHyperliquidOrderRequest(activeMD, createReq, inserted))
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

			err := h.engine.processDeal(h.ctx, h.key, h.deal, tc.events)
			require.NoError(t, err)

			require.Len(t, h.emitter.items, len(tc.wantActions))
			for i, want := range tc.wantActions {
				require.Equal(t, want, h.emitter.items[i].Action.Type)
				require.NotNil(t, h.emitter.items[i].BotEvent.CreatedAt)
				require.NotEmpty(t, h.emitter.items[i].BotEvent.Coin)
			}

			// For history assertions, use the fingerprint of the last event we fed.
			fp := tc.events[len(tc.events)-1].FingerprintAsID()
			history, err := h.store.ListEventsForOrder(h.key.BotID, h.key.DealID, fp)
			require.NoError(t, err)
			require.Len(t, history, len(tc.wantStatuses))
			for i, want := range tc.wantStatuses {
				require.Equal(t, want, history[i].Status)
			}
		})
	}
}
