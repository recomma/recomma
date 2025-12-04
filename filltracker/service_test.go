package filltracker

import (
	"context"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/adapter"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"

	"log/slog"
)

func TestServiceRebuildAggregatesExecutedOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)
	tracker.RequireStatusHydration()

	const (
		dealID = uint32(9001)
		botID  = uint32(42)
		coin   = "ETH"
	)

	createPrimaryVenue(t, store)

	// mark it as primary so the alias resolver prefers it
	require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	takeProfitOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	takeProfitIdent := defaultIdentifier(t, store, ctx, botID, takeProfitOid)

	baseEvent := tc.BotEvent{
		CreatedAt:   time.Now().Add(-5 * time.Minute),
		Action:      tc.BotEventActionExecute,
		Coin:        coin,
		Type:        tc.BUY,
		Status:      tc.Filled,
		Price:       10,
		Size:        100,
		OrderType:   tc.MarketOrderDealOrderTypeBase,
		QuoteVolume: 1000,
		IsMarket:    true,
		Text:        "base order filled",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 100, 0, 10, baseEvent.CreatedAt.Add(time.Second))))

	takeProfitEvent := tc.BotEvent{
		CreatedAt:   time.Now().Add(-4 * time.Minute),
		Action:      tc.BotEventActionPlace,
		Coin:        coin,
		Type:        tc.SELL,
		Status:      tc.Active,
		Price:       10.5,
		Size:        100,
		OrderType:   tc.MarketOrderDealOrderTypeTakeProfit,
		QuoteVolume: 1050,
		IsMarket:    false,
		Text:        "tp placed",
	}
	require.NoError(t, recordEvent(store, takeProfitOid, takeProfitEvent))
	require.NoError(t, recordStatus(store, takeProfitIdent, makeStatus(takeProfitOid, coin, "S", hyperliquid.OrderStatusValueOpen, 100, 100, 10.5, takeProfitEvent.CreatedAt.Add(time.Second))))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok, "expected snapshot")
	require.Len(t, snapshot.Orders, 2)
	require.InDelta(t, 100, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, 1000, snapshot.Position.TotalBuyValue, 1e-6)
	require.InDelta(t, 100, snapshot.Position.NetQty, 1e-6)
	require.InDelta(t, 10, snapshot.Position.AverageEntry, 1e-6)
	require.True(t, snapshot.AllBuysFilled, "all buys should be filled")
	require.Len(t, snapshot.ActiveTakeProfits, 1, "should have one active take-profit")
	require.InDelta(t, 100, snapshot.ActiveTakeProfits[0].RemainingQty, 1e-6)
}

func TestServiceUpdateStatusAdjustsPosition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9002)
		botID  = uint32(52)
		coin   = "DOGE"
	)

	createPrimaryVenue(t, store)

	// mark it as primary so the alias resolver prefers it
	require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	now := time.Now()

	require.NoError(t, recordEvent(store, baseOid, tc.BotEvent{
		CreatedAt: now.Add(-6 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     0.2,
		Size:      200,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 200, 0, 0.2, now.Add(-5*time.Minute))))

	require.NoError(t, recordEvent(store, tpOid, tc.BotEvent{
		CreatedAt: now.Add(-4 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     0.205,
		Size:      200,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}))
	initialStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 200, 200, 0.205, now.Add(-4*time.Minute+time.Second))
	require.NoError(t, recordStatus(store, tpIdent, initialStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	// Partial fill: remaining 80 of 200.
	partialStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 200, 80, 0.205, now)
	require.NoError(t, recordStatus(store, tpIdent, partialStatus))
	require.NoError(t, tracker.UpdateStatus(ctx, tpIdent, partialStatus))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 200, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, 200-80, snapshot.Position.TotalSellQty, 1e-6)
	require.InDelta(t, 80, snapshot.Position.NetQty, 1e-6)
	require.Len(t, snapshot.ActiveTakeProfits, 1, "should have one active take-profit")
	require.InDelta(t, 80, snapshot.ActiveTakeProfits[0].RemainingQty, 1e-6)
	require.True(t, snapshot.AllBuysFilled, "all buy orders still filled")
}

func TestSnapshotIgnoresFilledEventsWithoutHyperliquidStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9105)
		botID  = uint32(42)
		coin   = "DOGE"
		size   = 200.0
		price  = 0.15
	)

	createPrimaryVenue(t, store)
	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))
	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)

	baseEvent := tc.BotEvent{
		CreatedAt: time.Now().Add(-time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     price,
		Size:      size,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "3Commas marked order as filled",
	}
	baseRowID, err := store.RecordThreeCommasBotEvent(ctx, baseOid, baseEvent)
	require.NoError(t, err)

	createReq := adapter.ToCreateOrderRequest(coin, recomma.BotEvent{BotEvent: baseEvent}, baseOid)
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, baseIdent, createReq, baseRowID))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 0, snapshot.Position.TotalBuyQty, 1e-6, "should ignore 3Commas event without matching Hyperliquid status")
	require.InDelta(t, 0, snapshot.Position.NetQty, 1e-6, "net position must remain flat until Hyperliquid reports a fill")
	require.Falsef(t, snapshot.AllBuysFilled, "buys cannot be treated as filled without an exchange status (snapshot=%+v)", snapshot)
}

func TestUpdateStatusAutoCreatesTakeProfitAfterBaseFill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)
	emitter := &stubEmitter{}
	tracker.SetEmitter(emitter)

	const (
		dealID = uint32(9010)
		botID  = uint32(77)
		coin   = "DOGE"
		size   = 131.0
	)

	createPrimaryVenue(t, store)
	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))
	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	now := time.Now()

	require.NoError(t, recordEvent(store, baseOid, tc.BotEvent{
		CreatedAt: now.Add(-2 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Active,
		Price:     0.15,
		Size:      size,
		OrderType: tc.MarketOrderDealOrderTypeBase,
	}))

	require.NoError(t, recordEvent(store, tpOid, tc.BotEvent{
		CreatedAt: now.Add(-time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     0.15196,
		Size:      size,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "Placing TakeProfit trade",
	}))

	fillStatus := makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, size, 0, 0.1503, now)
	require.NoError(t, recordStatus(store, baseIdent, fillStatus))
	require.NoError(t, tracker.UpdateStatus(ctx, baseIdent, fillStatus))

	actions := emitter.Actions()
	require.Len(t, actions, 1, "expected take-profit creation")

	work := actions[0]
	require.Equal(t, recomma.ActionCreate, work.Action.Type)
	require.True(t, work.Action.Create.ReduceOnly, "take-profit must be reduce-only")
	require.InDelta(t, size, work.Action.Create.Size, 1e-6)
}

func TestReconcileTakeProfits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9003)
		botID  = uint32(62)
		coin   = "ARB"
	)

	createPrimaryVenue(t, store)

	// mark it as primary so the alias resolver prefers it
	require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	now := time.Now()

	require.NoError(t, recordEvent(store, tpOid, tc.BotEvent{
		CreatedAt: now.Add(-5 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     1.2,
		Size:      150,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}))

	initialStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 150, 150, 1.2, now.Add(-4*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, initialStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	fresher := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 150, 40, 1.2, now.Add(-2*time.Minute))
	require.NoError(t, tracker.UpdateStatus(ctx, tpIdent, fresher))

	// Older timestamp that reports a larger remaining size should be ignored.
	stale := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 150, 120, 1.2, now.Add(-3*time.Minute))
	require.NoError(t, tracker.UpdateStatus(ctx, tpIdent, stale))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Len(t, snapshot.ActiveTakeProfits, 1, "should have one active take-profit")
	require.InDelta(t, 40, snapshot.ActiveTakeProfits[0].RemainingQty, 1e-6)
	require.WithinDuration(t, now.Add(-2*time.Minute), snapshot.ActiveTakeProfits[0].StatusTime, time.Second)
}

func TestApplyScaledOrderUpdatesSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9100)
		botID  = uint32(71)
		coin   = "SOL"
	)

	createPrimaryVenue(t, store)

	// mark it as primary so the alias resolver prefers it
	require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	now := time.Now().UTC()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-2 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Active,
		Price:     24.5,
		Size:      100,
		OrderType: tc.MarketOrderDealOrderTypeBase,
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))

	initialStatus := makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueOpen, 100, 100, 24.5, now.Add(-90*time.Second))
	require.NoError(t, recordStatus(store, baseIdent, initialStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	tracker.ApplyScaledOrder(baseIdent, 40, 24.25)

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	var order *OrderSnapshot
	for i := range snapshot.Orders {
		if snapshot.Orders[i].OrderId.Hex() == baseOid.Hex() {
			order = &snapshot.Orders[i]
			break
		}
	}
	require.NotNil(t, order)
	require.InDelta(t, 40, order.OriginalQty, 1e-6)
	require.InDelta(t, 40, order.RemainingQty, 1e-6)
	require.InDelta(t, 24.25, order.ReferencePrice, 1e-6)
}

func TestReconcileTakeProfitsCancelsWhenFlat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9050)
		botID  = uint32(68)
		coin   = "APT"
	)

	flags := map[string]interface{}{"is_primary": true}
	_, err := store.UpsertVenue(ctx, "hyperliquid:test", api.VenueUpsertRequest{
		Type:        "hyperliquid",
		DisplayName: "Test Venue",
		Wallet:      "0xfeed",
		Flags:       &flags,
	})
	require.NoError(t, err)

	// mark it as primary so the alias resolver prefers it
	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	closeOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 3}
	closeIdent := defaultIdentifier(t, store, ctx, botID, closeOid)
	now := time.Now()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-10 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     8,
		Size:      5,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 5, 0, 8, now.Add(-9*time.Minute))))

	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-8 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     8.5,
		Size:      5,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}
	require.NoError(t, recordEvent(store, tpOid, tpEvent))
	tpStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 5, 5, 8.5, now.Add(-7*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, tpStatus))

	closeEvent := tc.BotEvent{
		CreatedAt: now.Add(-6 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Filled,
		Price:     8.3,
		Size:      5,
		OrderType: tc.MarketOrderDealOrderTypeManualSafety,
		Text:      "manual exit",
	}
	require.NoError(t, recordEvent(store, closeOid, closeEvent))
	closeStatus := makeStatus(closeOid, coin, "S", hyperliquid.OrderStatusValueFilled, 5, 0, 8.3, now.Add(-5*time.Minute))
	require.NoError(t, recordStatus(store, closeIdent, closeStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Len(t, snapshot.ActiveTakeProfits, 1, "should have one active take-profit")
	require.InDelta(t, 0, snapshot.Position.NetQty, 1e-6)
	require.True(t, snapshot.AllBuysFilled)

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)

	actions := emitter.Actions()
	require.Len(t, actions, 1)
	work := actions[0]
	require.Equal(t, recomma.ActionCancel, work.Action.Type)
	require.Equal(t, tpOid.Hex(), work.Action.Cancel.Cloid)
	require.Equal(t, tpIdent, work.Identifier)
}

func TestReconcileTakeProfitsDropsCancelledTakeProfits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9051)
		botID  = uint32(69)
		coin   = "OP"
	)

	flags := map[string]interface{}{"is_primary": true}
	_, err := store.UpsertVenue(ctx, "hyperliquid:test", api.VenueUpsertRequest{
		Type:        "hyperliquid",
		DisplayName: "Test Venue",
		Wallet:      "0xdeadbeef",
		Flags:       &flags,
	})
	require.NoError(t, err)

	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	closeOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 3}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	closeIdent := defaultIdentifier(t, store, ctx, botID, closeOid)
	now := time.Now()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-12 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     2.1,
		Size:      8,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 8, 0, 2.1, now.Add(-11*time.Minute))))

	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-10 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     2.3,
		Size:      8,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}
	require.NoError(t, recordEvent(store, tpOid, tpEvent))
	tpStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 8, 8, 2.3, now.Add(-9*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, tpStatus))

	closeEvent := tc.BotEvent{
		CreatedAt: now.Add(-8 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Filled,
		Price:     2.15,
		Size:      8,
		OrderType: tc.MarketOrderDealOrderTypeManualSafety,
		Text:      "manual exit",
	}
	require.NoError(t, recordEvent(store, closeOid, closeEvent))
	closeStatus := makeStatus(closeOid, coin, "S", hyperliquid.OrderStatusValueFilled, 8, 0, 2.15, now.Add(-7*time.Minute))
	require.NoError(t, recordStatus(store, closeIdent, closeStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Len(t, snapshot.ActiveTakeProfits, 1, "should have one active take-profit before reconciliation")
	require.InDelta(t, 0, snapshot.Position.NetQty, 1e-6, "deal should already be flat")

	firstEmitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, firstEmitter)

	firstActions := firstEmitter.Actions()
	require.Len(t, firstActions, 1, "expected a single cancel action")
	require.Equal(t, recomma.ActionCancel, firstActions[0].Action.Type)

	snapshotAfterCancel, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Empty(t, snapshotAfterCancel.ActiveTakeProfits, "cancelled take-profit should be pruned to avoid repeat cancels")

	secondEmitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, secondEmitter)
	require.Empty(t, secondEmitter.Actions(), "take-profit cancels should not be re-enqueued once pruned")
}

func TestMarkOrderCancelledDoesNotFabricateFilledQty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9052)
		botID  = uint32(70)
		coin   = "SEI"
	)

	createPrimaryVenue(t, store)

	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))
	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	now := time.Now()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-8 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     1.05,
		Size:      4,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 4, 0, 1.05, now.Add(-7*time.Minute))))

	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-6 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     1.15,
		Size:      4,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}
	require.NoError(t, recordEvent(store, tpOid, tpEvent))
	tpStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 4, 4, 1.15, now.Add(-5*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, tpStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 0, snapshot.Position.TotalSellQty, 1e-6, "unfilled take profit should not contribute to sells")
	require.InDelta(t, baseEvent.Size, snapshot.Position.NetQty, 1e-6, "base position should remain long before cancel")

	tracker.markOrderCancelled(tpIdent)

	cancelledSnapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 0, cancelledSnapshot.Position.TotalSellQty, 1e-6, "cancelling should not fabricate fills")
	require.InDelta(t, baseEvent.Size, cancelledSnapshot.Position.NetQty, 1e-6, "net position should remain long after cancel")

	var tpSnapshot *OrderSnapshot
	for i := range cancelledSnapshot.Orders {
		if cancelledSnapshot.Orders[i].OrderId.Hex() == tpOid.Hex() {
			tpSnapshot = &cancelledSnapshot.Orders[i]
			break
		}
	}
	require.NotNil(t, tpSnapshot, "expected to track cancelled take-profit")
	require.Equal(t, hyperliquid.OrderStatusValueCanceled, tpSnapshot.Status)
	require.InDelta(t, 0, tpSnapshot.FilledQty, 1e-6, "filled quantity must stay at actual fill size")
	require.InDelta(t, 0, tpSnapshot.RemainingQty, 1e-6, "cancelled orders should have no remaining qty")
}

func TestUpdateStatusIgnoresOlderTimestamps(t *testing.T) {
	t.Parallel()

	const (
		dealID = uint32(777)
		botID  = uint32(88)
		coin   = "SOL"
	)

	t.Run("recreates missing take profit", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		store := newTestStore(t)
		logger := newTestLogger()
		tracker := New(store, logger)

		createPrimaryVenue(t, store)

		// mark it as primary so the alias resolver prefers it
		require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

		recordDeal(t, store, dealID, botID, coin)

		baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
		tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
		baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
		tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
		now := time.Now()

		require.NoError(t, recordEvent(store, baseOid, tc.BotEvent{
			CreatedAt: now.Add(-10 * time.Minute),
			Action:    tc.BotEventActionExecute,
			Coin:      coin,
			Type:      tc.BUY,
			Status:    tc.Filled,
			Price:     35,
			Size:      10,
			OrderType: tc.MarketOrderDealOrderTypeBase,
		}))
		require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 10, 0, 35, now.Add(-9*time.Minute))))

		tpEvent := tc.BotEvent{
			CreatedAt: now.Add(-8 * time.Minute),
			Action:    tc.BotEventActionPlace,
			Coin:      coin,
			Type:      tc.SELL,
			Status:    tc.Active,
			Price:     37,
			Size:      10,
			OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		}
		require.NoError(t, recordEvent(store, tpOid, tpEvent))

		// Tracker sees the order as cancelled before reconciliation.
		tpStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueCanceled, 10, 10, 37, now.Add(-7*time.Minute))
		require.NoError(t, recordStatus(store, tpIdent, tpStatus))

		require.NoError(t, tracker.Rebuild(ctx))

		snapshot, ok := tracker.Snapshot(dealID)
		require.True(t, ok)
		require.Empty(t, snapshot.ActiveTakeProfits, "should have no active take-profits")
		require.NotNil(t, snapshot.LastTakeProfitEvent)
		require.InDelta(t, 10, snapshot.Position.NetQty, 1e-6)

		emitter := &stubEmitter{}
		tracker.ReconcileTakeProfits(ctx, emitter)

		actions := emitter.Actions()
		require.Len(t, actions, 1)
		work := actions[0]
		require.Equal(t, recomma.ActionCreate, work.Action.Type)
		require.InDelta(t, 10, work.Action.Create.Size, 1e-6)
		require.True(t, work.Action.Create.ReduceOnly)
		require.Equal(t, tpOid.Hex(), work.OrderId.Hex())
		require.Equal(t, tpIdent, work.Identifier)
		cloid := work.Action.Create.ClientOrderID
		require.NotNil(t, cloid)
		require.Equal(t, tpOid.Hex(), *cloid)
	})

	t.Run("modifies mismatched take profit", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		store := newTestStore(t)
		logger := newTestLogger()
		tracker := New(store, logger)

		createPrimaryVenue(t, store)

		// mark it as primary so the alias resolver prefers it
		require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

		recordDeal(t, store, dealID+1, botID, coin)

		baseOid := orderid.OrderId{BotID: botID, DealID: dealID + 1, BotEventID: 1}
		tpOid := orderid.OrderId{BotID: botID, DealID: dealID + 1, BotEventID: 2}
		baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
		tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
		now := time.Now()

		// Base fill establishes net qty 15.
		require.NoError(t, recordEvent(store, baseOid, tc.BotEvent{
			CreatedAt: now.Add(-10 * time.Minute),
			Action:    tc.BotEventActionExecute,
			Coin:      coin,
			Type:      tc.BUY,
			Status:    tc.Filled,
			Price:     30,
			Size:      15,
			OrderType: tc.MarketOrderDealOrderTypeBase,
		}))
		require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 15, 0, 30, now.Add(-9*time.Minute))))

		tpEvent := tc.BotEvent{
			CreatedAt: now.Add(-8 * time.Minute),
			Action:    tc.BotEventActionPlace,
			Coin:      coin,
			Type:      tc.SELL,
			Status:    tc.Active,
			Price:     32,
			Size:      10,
			OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		}
		require.NoError(t, recordEvent(store, tpOid, tpEvent))

		// Active order is smaller than the net qty, so reconciliation should resize it.
		tpStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 10, 10, 32, now.Add(-7*time.Minute))
		require.NoError(t, recordStatus(store, tpIdent, tpStatus))

		require.NoError(t, tracker.Rebuild(ctx))

		snapshot, ok := tracker.Snapshot(dealID + 1)
		require.True(t, ok)
		require.Len(t, snapshot.ActiveTakeProfits, 1, "should have one active take-profit")
		require.InDelta(t, 15, snapshot.Position.NetQty, 1e-6)

		emitter := &stubEmitter{}
		tracker.ReconcileTakeProfits(ctx, emitter)

		actions := emitter.Actions()
		require.Len(t, actions, 1)
		work := actions[0]
		require.Equal(t, recomma.ActionModify, work.Action.Type)
		require.InDelta(t, 15, work.Action.Modify.Order.Size, 1e-6)
		require.True(t, work.Action.Modify.Order.ReduceOnly)
		oid := work.Action.Modify.Cloid.Value
		require.Equal(t, tpOid.Hex(), oid)
		require.Equal(t, tpIdent, work.Identifier)
	})
}

func TestUpdateStatus_RecreatesTakeProfitBeforeExistingStatusArrives(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9102)
		botID  = uint32(79)
		coin   = "DOGE"
		size   = 1955.0
	)

	createPrimaryVenue(t, store)
	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))
	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	now := time.Now()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-5 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     0.15,
		Size:      size,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	baseStatus := makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, size, 0, baseEvent.Price, now.Add(-4*time.Minute))
	require.NoError(t, recordStatus(store, baseIdent, baseStatus))

	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-3 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     0.15212,
		Size:      size,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "existing tp",
	}
	require.NoError(t, recordEvent(store, tpOid, tpEvent))
	tpStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, size, size, tpEvent.Price, now.Add(-2*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, tpStatus))

	emitter := &stubEmitter{}
	tracker.SetEmitter(emitter)

	// Status refresher replays the base fill before the TP status, so ideally the
	// tracker should wait for the TP replay instead of emitting duplicate work.
	require.NoError(t, tracker.UpdateStatus(ctx, baseIdent, baseStatus))

	actions := emitter.Actions()
	require.Len(t, actions, 0, "replaying the base fill alone should not enqueue a duplicate take-profit create")

	// Once the actual TP status arrives the tracker should remain idle.
	require.NoError(t, tracker.UpdateStatus(ctx, tpIdent, tpStatus))
	require.Len(t, emitter.Actions(), 0, "take-profit replay should not emit any additional work")
}

func TestSnapshotClearsStaleOpenOrdersAfterStopLoss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9300)
		botID  = uint32(55)
		coin   = "DOGE"
		size   = 160.0
		price  = 0.14
	)

	createPrimaryVenue(t, store)
	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))
	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	now := time.Now().Add(-48 * time.Hour)

	baseEvent := tc.BotEvent{
		CreatedAt: now,
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     price,
		Size:      size,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		Text:      "base order placed long ago",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))

	// Hyperliquid never produced a terminal status; the last thing we saw was "open".
	staleStatus := makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueOpen, size, size, price, now)
	require.NoError(t, recordStatus(store, baseIdent, staleStatus))

	// 3Commas reported a stop-loss completion, so the mirrored deal should be closed.
	closeOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	closeEvent := tc.BotEvent{
		CreatedAt: time.Now().Add(-47 * time.Hour),
		Action:    tc.BotEventActionCancel,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Finished,
		Price:     price * 0.98,
		Size:      size,
		OrderType: tc.MarketOrderDealOrderTypeStopLoss,
		Text:      "stop loss finished deal",
	}
	require.NoError(t, recordEvent(store, closeOid, closeEvent))

	require.NoError(t, store.RecordThreeCommasDeal(ctx, tc.Deal{
		Id:         int(dealID),
		BotId:      int(botID),
		ToCurrency: coin,
		Status:     tc.DealStatus("stop_loss_finished"),
		Finished:   true,
	}))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.True(t, snapshot.AllBuysFilled, "stale open buy should be cleared once stop loss finishes")
	require.Empty(t, snapshot.OpenBuys, "no live buy orders should remain after deal closure")
}

func TestEnsureTakeProfitRecreatesAfterStaleSubmission(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9101)
		botID  = uint32(72)
		coin   = "OP"
	)

	createPrimaryVenue(t, store)

	// mark it as primary so the alias resolver prefers it
	require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	now := time.Now()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-10 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     35,
		Size:      10,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 10, 0, 35, now.Add(-9*time.Minute))))

	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-8 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     37,
		Size:      10,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}
	tpRowID, err := store.RecordThreeCommasBotEvent(ctx, tpOid, tpEvent)
	require.NoError(t, err)

	create := adapter.ToCreateOrderRequest(coin, recomma.BotEvent{BotEvent: tpEvent}, tpOid)
	require.True(t, create.ReduceOnly)
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, tpIdent, create, tpRowID))

	canceled := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValue("reduceOnlyCanceled"), 10, 10, 37, now.Add(-7*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, canceled))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Empty(t, snapshot.ActiveTakeProfits, "should have no active take-profits")
	require.InDelta(t, 10, snapshot.Position.NetQty, 1e-6)

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)

	actions := emitter.Actions()
	require.Len(t, actions, 1)
	work := actions[0]
	require.Equal(t, recomma.ActionCreate, work.Action.Type)
	require.InDelta(t, snapshot.Position.NetQty, work.Action.Create.Size, 1e-6)
	require.True(t, work.Action.Create.ReduceOnly)
	require.Equal(t, tpOid.Hex(), work.OrderId.Hex())
}

func TestReconcileTakeProfitsRecreatesAfterCancelWithMissingOrderId(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)

	const (
		dealID = uint32(9302)
		botID  = uint32(73)
		coin   = "ARB"
	)

	createPrimaryVenue(t, store)

	// mark it as primary so the alias resolver prefers it
	require.NoError(t, store.UpsertBotVenueAssignment(context.Background(), botID, "hyperliquid:test", true))

	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	now := time.Now()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-15 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     12.5,
		Size:      8,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, 8, 0, 12.5, now.Add(-14*time.Minute))))

	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-13 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     13.2,
		Size:      8,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}
	tpRowID, err := store.RecordThreeCommasBotEvent(ctx, tpOid, tpEvent)
	require.NoError(t, err)

	create := adapter.ToCreateOrderRequest(coin, recomma.BotEvent{BotEvent: tpEvent}, tpOid)
	require.True(t, create.ReduceOnly)
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, tpIdent, create, tpRowID))

	tpStatus := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValueOpen, 8, 8, 13.2, now.Add(-12*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, tpStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	tracker.mu.Lock()
	state, ok := tracker.orders[tpIdent]
	require.True(t, ok, "expected tracked order state")
	state.event = nil
	state.originalQty = 8
	state.remainingQty = 6
	state.filledQty = 0
	deal := tracker.deals[dealID]
	require.NotNil(t, deal)
	deal.orders[tpIdent] = state
	deal.recompute()
	tracker.mu.Unlock()

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Len(t, snapshot.ActiveTakeProfits, 1, "should have one active take-profit")
	require.Nil(t, snapshot.ActiveTakeProfits[0].Event)
	require.Nil(t, snapshot.LastTakeProfitEvent)
	require.InDelta(t, 8, snapshot.Position.NetQty, 1e-6)

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)

	actions := emitter.Actions()
	require.Len(t, actions, 2)

	cancelWork := actions[0]
	require.Equal(t, recomma.ActionCancel, cancelWork.Action.Type)
	require.Equal(t, tpOid.Hex(), cancelWork.Action.Cancel.Cloid)
	require.Equal(t, tpIdent, cancelWork.Identifier)

	createWork := actions[1]
	require.Equal(t, recomma.ActionCreate, createWork.Action.Type)
	require.True(t, createWork.Action.Create.ReduceOnly)
	require.InDelta(t, snapshot.Position.NetQty, createWork.Action.Create.Size, 1e-6)
	require.Equal(t, tpOid.Hex(), createWork.OrderId.Hex())
	require.Equal(t, tpIdent, createWork.Identifier)
}

func TestReconcileTakeProfits_WaitsForStatusRefreshBeforeRecreation(t *testing.T) {
	// t.Skip("BUG: filltracker recreates TP during startup before Hyperliquid statuses are refreshed (see bugs/bug_2025-11-30_startup_tp_race.md)")

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)
	tracker.RequireStatusHydration()

	const (
		dealID = uint32(9401)
		botID  = uint32(74)
		coin   = "DOGE"
	)

	createPrimaryVenue(t, store)
	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))
	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	now := time.Now().UTC()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-2 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     0.15,
		Size:      1955,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, baseEvent.Size, 0, baseEvent.Price, now.Add(-90*time.Second))))

	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-1 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     0.15212,
		Size:      1955,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "take profit placed",
	}
	rowID, err := store.RecordThreeCommasBotEvent(ctx, tpOid, tpEvent)
	require.NoError(t, err)

	create := adapter.ToCreateOrderRequest(coin, recomma.BotEvent{BotEvent: tpEvent}, tpOid)
	create.ReduceOnly = true
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, tpIdent, create, rowID))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.True(t, snapshot.AllBuysFilled, "all safeties/base should be filled at startup")

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)

	require.Len(t, emitter.Actions(), 0, "tracker should wait for Hyperliquid status refresh before recreating the TP")
}

func TestReconcileTakeProfits_RecoversAfterStatusRefreshFailure(t *testing.T) {

	ctx := context.Background()
	store := newTestStore(t)
	logger := newTestLogger()
	tracker := New(store, logger)
	tracker.RequireStatusHydration()

	const (
		dealID = uint32(9502)
		botID  = uint32(81)
		coin   = "DOGE"
	)

	createPrimaryVenue(t, store)
	require.NoError(t, store.UpsertBotVenueAssignment(ctx, botID, "hyperliquid:test", true))
	recordDeal(t, store, dealID, botID, coin)

	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseIdent := defaultIdentifier(t, store, ctx, botID, baseOid)
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	tpIdent := defaultIdentifier(t, store, ctx, botID, tpOid)
	now := time.Now().UTC()

	baseEvent := tc.BotEvent{
		CreatedAt: now.Add(-2 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     0.12,
		Size:      640,
		OrderType: tc.MarketOrderDealOrderTypeBase,
		IsMarket:  true,
		Text:      "base fill",
	}
	require.NoError(t, recordEvent(store, baseOid, baseEvent))
	require.NoError(t, recordStatus(store, baseIdent, makeStatus(baseOid, coin, "B", hyperliquid.OrderStatusValueFilled, baseEvent.Size, 0, baseEvent.Price, now.Add(-90*time.Second))))

	tpEvent := tc.BotEvent{
		CreatedAt: now.Add(-1*time.Minute - 30*time.Second),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     0.1255,
		Size:      640,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		Text:      "tp placed",
	}
	rowID, err := store.RecordThreeCommasBotEvent(ctx, tpOid, tpEvent)
	require.NoError(t, err)

	create := adapter.ToCreateOrderRequest(coin, recomma.BotEvent{BotEvent: tpEvent}, tpOid)
	create.ReduceOnly = true
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, tpIdent, create, rowID))

	canceled := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValue("reduceOnlyCanceled"), tpEvent.Size, tpEvent.Size, tpEvent.Price, now.Add(-1*time.Minute))
	require.NoError(t, recordStatus(store, tpIdent, canceled))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.True(t, snapshot.AllBuysFilled, "all buys should be filled for reconciliation")
	require.Empty(t, snapshot.ActiveTakeProfits, "tp canceled on exchange should leave no active reduce-only orders")

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)
	require.Len(t, emitter.Actions(), 0, "hydration gate blocks reconciliation until Hyperliquid replay succeeds")

	// Simulate Hyperliquid eventually delivering fresh statuses after the initial refresher failure.
	refreshed := makeStatus(tpOid, coin, "S", hyperliquid.OrderStatusValue("reduceOnlyCanceled"), tpEvent.Size, tpEvent.Size, tpEvent.Price, now)
	require.NoError(t, tracker.UpdateStatus(ctx, tpIdent, refreshed))

	tracker.ReconcileTakeProfits(ctx, emitter)
	require.Len(t, emitter.Actions(), 1, "tracker should resume take-profit reconciliation once statuses are refreshed later")
}

func TestCleanupStaleDeals_RemovesOnlyInactive(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	service := &Service{
		logger: newTestLogger(),
		orders: make(map[recomma.OrderIdentifier]*orderState),
		deals:  make(map[uint32]*dealState),
	}

	staleOID := orderid.OrderId{BotID: 1, DealID: 100, BotEventID: 1}
	staleIdent := recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "wallet", staleOID)
	staleOrder := &orderState{
		identifier:   staleIdent,
		status:       hyperliquid.OrderStatusValueFilled,
		remainingQty: 0,
		lastUpdate:   now.Add(-2 * time.Hour),
	}
	service.orders[staleIdent] = staleOrder
	service.deals[staleOID.DealID] = &dealState{
		botID:      staleOID.BotID,
		dealID:     staleOID.DealID,
		orders:     map[recomma.OrderIdentifier]*orderState{staleIdent: staleOrder},
		lastUpdate: now.Add(-2 * time.Hour),
	}

	activeOID := orderid.OrderId{BotID: 2, DealID: 200, BotEventID: 1}
	activeIdent := recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "wallet", activeOID)
	activeOrder := &orderState{
		identifier:   activeIdent,
		status:       hyperliquid.OrderStatusValueOpen,
		remainingQty: 5,
		lastUpdate:   now.Add(-2 * time.Hour),
	}
	service.orders[activeIdent] = activeOrder
	service.deals[activeOID.DealID] = &dealState{
		botID:      activeOID.BotID,
		dealID:     activeOID.DealID,
		orders:     map[recomma.OrderIdentifier]*orderState{activeIdent: activeOrder},
		lastUpdate: now.Add(-2 * time.Hour),
	}

	recentOID := orderid.OrderId{BotID: 3, DealID: 300, BotEventID: 1}
	recentIdent := recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "wallet", recentOID)
	recentOrder := &orderState{
		identifier:   recentIdent,
		status:       hyperliquid.OrderStatusValueFilled,
		remainingQty: 0,
		lastUpdate:   now.Add(-10 * time.Minute),
	}
	service.orders[recentIdent] = recentOrder
	service.deals[recentOID.DealID] = &dealState{
		botID:      recentOID.BotID,
		dealID:     recentOID.DealID,
		orders:     map[recomma.OrderIdentifier]*orderState{recentIdent: recentOrder},
		lastUpdate: now.Add(-10 * time.Minute),
	}

	cleaned := service.CleanupStaleDeals(time.Hour)
	require.Equal(t, 1, cleaned)

	_, ok := service.deals[staleOID.DealID]
	require.False(t, ok)
	_, ok = service.orders[staleIdent]
	require.False(t, ok)
	require.Contains(t, service.deals, activeOID.DealID)
	require.Contains(t, service.deals, recentOID.DealID)
}

// Helpers

type stubEmitter struct {
	mu    sync.Mutex
	works []recomma.OrderWork
}

func (s *stubEmitter) Emit(ctx context.Context, w recomma.OrderWork) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	clone := w
	s.works = append(s.works, clone)
	return nil
}

func (s *stubEmitter) Actions() []recomma.OrderWork {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]recomma.OrderWork, len(s.works))
	copy(out, s.works)
	return out
}

func newTestStore(t *testing.T) *storage.Storage {
	t.Helper()
	store, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}

func newTestLogger() *slog.Logger {
	handler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(handler)
}

func recordDeal(t *testing.T, store *storage.Storage, dealID, botID uint32, coin string) {
	t.Helper()

	now := time.Now().UTC()
	err := store.RecordThreeCommasDeal(context.Background(), tc.Deal{
		Id:         int(dealID),
		BotId:      int(botID),
		CreatedAt:  now,
		UpdatedAt:  now,
		ToCurrency: coin,
	})
	require.NoError(t, err)
}

func recordEvent(store *storage.Storage, oid orderid.OrderId, evt tc.BotEvent) error {
	_, err := store.RecordThreeCommasBotEvent(context.Background(), oid, evt)
	return err
}

func recordStatus(store *storage.Storage, ident recomma.OrderIdentifier, status hyperliquid.WsOrder) error {
	return store.RecordHyperliquidStatus(context.Background(), ident, status)
}

func defaultIdentifier(t *testing.T, store *storage.Storage, ctx context.Context, botID uint32, oid orderid.OrderId) recomma.OrderIdentifier {
	t.Helper()

	assignments, err := store.ListVenuesForBot(context.Background(), botID)
	require.NoError(t, err)
	require.NotEmpty(t, assignments)

	venue := assignments[0]
	return recomma.NewOrderIdentifier(venue.VenueID, venue.Wallet, oid)
}

func makeStatus(oid orderid.OrderId, coin, side string, status hyperliquid.OrderStatusValue, original, remaining, limit float64, ts time.Time) hyperliquid.WsOrder {
	return hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      coin,
			Side:      side,
			LimitPx:   formatFloat(limit),
			Sz:        formatFloat(remaining),
			Oid:       ts.UnixNano(),
			Timestamp: ts.UnixMilli(),
			OrigSz:    formatFloat(original),
			Cloid:     oid.HexAsPointer(),
		},
		Status:          status,
		StatusTimestamp: ts.UnixMilli(),
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func createPrimaryVenue(t *testing.T, store *storage.Storage) {
	t.Helper()
	// we need to create an actual test venue
	ctx := context.Background()
	flags := map[string]interface{}{"is_primary": true}
	_, err := store.UpsertVenue(ctx, "hyperliquid:test", api.VenueUpsertRequest{
		Type:        "hyperliquid",
		DisplayName: "Test Venue",
		Wallet:      "0xfeed",
		Flags:       &flags,
	})
	require.NoError(t, err)
}
