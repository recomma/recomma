package filltracker

import (
	"context"
	"io"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/adapter"
	"github.com/recomma/recomma/metadata"
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

	const (
		dealID = uint32(9001)
		botID  = uint32(42)
		coin   = "ETH"
	)

	recordDeal(t, store, dealID, botID, coin)

	baseMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	takeProfitMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 2}

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
	require.NoError(t, recordEvent(store, baseMD, baseEvent))
	require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 100, 0, 10, baseEvent.CreatedAt.Add(time.Second))))

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
	require.NoError(t, recordEvent(store, takeProfitMD, takeProfitEvent))
	require.NoError(t, recordStatus(store, takeProfitMD, makeStatus(takeProfitMD, coin, "S", hyperliquid.OrderStatusValueOpen, 100, 100, 10.5, takeProfitEvent.CreatedAt.Add(time.Second))))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok, "expected snapshot")
	require.Len(t, snapshot.Orders, 2)
	require.InDelta(t, 100, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, 1000, snapshot.Position.TotalBuyValue, 1e-6)
	require.InDelta(t, 100, snapshot.Position.NetQty, 1e-6)
	require.InDelta(t, 10, snapshot.Position.AverageEntry, 1e-6)
	require.True(t, snapshot.AllBuysFilled, "all buys should be filled")
	require.NotNil(t, snapshot.ActiveTakeProfit)
	require.InDelta(t, 100, snapshot.ActiveTakeProfit.RemainingQty, 1e-6)
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

	recordDeal(t, store, dealID, botID, coin)

	baseMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	tpMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 2}
	now := time.Now()

	require.NoError(t, recordEvent(store, baseMD, tc.BotEvent{
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
	require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 200, 0, 0.2, now.Add(-5*time.Minute))))

	require.NoError(t, recordEvent(store, tpMD, tc.BotEvent{
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
	initialStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 200, 200, 0.205, now.Add(-4*time.Minute+time.Second))
	require.NoError(t, recordStatus(store, tpMD, initialStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	// Partial fill: remaining 80 of 200.
	partialStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 200, 80, 0.205, now)
	require.NoError(t, recordStatus(store, tpMD, partialStatus))
	require.NoError(t, tracker.UpdateStatus(ctx, tpMD, partialStatus))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 200, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, 200-80, snapshot.Position.TotalSellQty, 1e-6)
	require.InDelta(t, 80, snapshot.Position.NetQty, 1e-6)
	require.NotNil(t, snapshot.ActiveTakeProfit)
	require.InDelta(t, 80, snapshot.ActiveTakeProfit.RemainingQty, 1e-6)
	require.True(t, snapshot.AllBuysFilled, "all buy orders still filled")
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

	recordDeal(t, store, dealID, botID, coin)

	tpMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	now := time.Now()

	require.NoError(t, recordEvent(store, tpMD, tc.BotEvent{
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

	initialStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 150, 150, 1.2, now.Add(-4*time.Minute))
	require.NoError(t, recordStatus(store, tpMD, initialStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	fresher := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 150, 40, 1.2, now.Add(-2*time.Minute))
	require.NoError(t, tracker.UpdateStatus(ctx, tpMD, fresher))

	// Older timestamp that reports a larger remaining size should be ignored.
	stale := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 150, 120, 1.2, now.Add(-3*time.Minute))
	require.NoError(t, tracker.UpdateStatus(ctx, tpMD, stale))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.NotNil(t, snapshot.ActiveTakeProfit)
	require.InDelta(t, 40, snapshot.ActiveTakeProfit.RemainingQty, 1e-6)
	require.WithinDuration(t, now.Add(-2*time.Minute), snapshot.ActiveTakeProfit.StatusTime, time.Second)
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

	recordDeal(t, store, dealID, botID, coin)

	baseMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	tpMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 2}
	closeMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 3}
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
	require.NoError(t, recordEvent(store, baseMD, baseEvent))
	require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 5, 0, 8, now.Add(-9*time.Minute))))

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
	require.NoError(t, recordEvent(store, tpMD, tpEvent))
	tpStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 5, 5, 8.5, now.Add(-7*time.Minute))
	require.NoError(t, recordStatus(store, tpMD, tpStatus))

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
	require.NoError(t, recordEvent(store, closeMD, closeEvent))
	closeStatus := makeStatus(closeMD, coin, "S", hyperliquid.OrderStatusValueFilled, 5, 0, 8.3, now.Add(-5*time.Minute))
	require.NoError(t, recordStatus(store, closeMD, closeStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.NotNil(t, snapshot.ActiveTakeProfit)
	require.InDelta(t, 0, snapshot.Position.NetQty, 1e-6)
	require.True(t, snapshot.AllBuysFilled)

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)

	actions := emitter.Actions()
	require.Len(t, actions, 1)
	work := actions[0]
	require.Equal(t, recomma.ActionCancel, work.Action.Type)
	require.NotNil(t, work.Action.Cancel)
	require.Equal(t, tpMD.Hex(), work.Action.Cancel.Cloid)
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

		recordDeal(t, store, dealID, botID, coin)

		baseMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
		tpMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 2}
		now := time.Now()

		require.NoError(t, recordEvent(store, baseMD, tc.BotEvent{
			CreatedAt: now.Add(-10 * time.Minute),
			Action:    tc.BotEventActionExecute,
			Coin:      coin,
			Type:      tc.BUY,
			Status:    tc.Filled,
			Price:     35,
			Size:      10,
			OrderType: tc.MarketOrderDealOrderTypeBase,
		}))
		require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 10, 0, 35, now.Add(-9*time.Minute))))

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
		require.NoError(t, recordEvent(store, tpMD, tpEvent))

		// Tracker sees the order as cancelled before reconciliation.
		tpStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueCanceled, 10, 10, 37, now.Add(-7*time.Minute))
		require.NoError(t, recordStatus(store, tpMD, tpStatus))

		require.NoError(t, tracker.Rebuild(ctx))

		snapshot, ok := tracker.Snapshot(dealID)
		require.True(t, ok)
		require.Nil(t, snapshot.ActiveTakeProfit)
		require.NotNil(t, snapshot.LastTakeProfitEvent)
		require.InDelta(t, 10, snapshot.Position.NetQty, 1e-6)

		emitter := &stubEmitter{}
		tracker.ReconcileTakeProfits(ctx, emitter)

		actions := emitter.Actions()
		require.Len(t, actions, 1)
		work := actions[0]
		require.Equal(t, recomma.ActionCreate, work.Action.Type)
		require.NotNil(t, work.Action.Create)
		require.InDelta(t, 10, work.Action.Create.Size, 1e-6)
		require.True(t, work.Action.Create.ReduceOnly)
		require.Equal(t, tpMD.Hex(), work.MD.Hex())
		cloid := work.Action.Create.ClientOrderID
		require.NotNil(t, cloid)
		require.Equal(t, tpMD.Hex(), *cloid)
	})

	t.Run("modifies mismatched take profit", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		store := newTestStore(t)
		logger := newTestLogger()
		tracker := New(store, logger)

		recordDeal(t, store, dealID+1, botID, coin)

		baseMD := metadata.Metadata{BotID: botID, DealID: dealID + 1, BotEventID: 1}
		tpMD := metadata.Metadata{BotID: botID, DealID: dealID + 1, BotEventID: 2}
		now := time.Now()

		// Base fill establishes net qty 15.
		require.NoError(t, recordEvent(store, baseMD, tc.BotEvent{
			CreatedAt: now.Add(-10 * time.Minute),
			Action:    tc.BotEventActionExecute,
			Coin:      coin,
			Type:      tc.BUY,
			Status:    tc.Filled,
			Price:     30,
			Size:      15,
			OrderType: tc.MarketOrderDealOrderTypeBase,
		}))
		require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 15, 0, 30, now.Add(-9*time.Minute))))

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
		require.NoError(t, recordEvent(store, tpMD, tpEvent))

		// Active order is smaller than the net qty, so reconciliation should resize it.
		tpStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 10, 10, 32, now.Add(-7*time.Minute))
		require.NoError(t, recordStatus(store, tpMD, tpStatus))

		require.NoError(t, tracker.Rebuild(ctx))

		snapshot, ok := tracker.Snapshot(dealID + 1)
		require.True(t, ok)
		require.NotNil(t, snapshot.ActiveTakeProfit)
		require.InDelta(t, 15, snapshot.Position.NetQty, 1e-6)

		emitter := &stubEmitter{}
		tracker.ReconcileTakeProfits(ctx, emitter)

		actions := emitter.Actions()
		require.Len(t, actions, 1)
		work := actions[0]
		require.Equal(t, recomma.ActionModify, work.Action.Type)
		require.NotNil(t, work.Action.Modify)
		require.InDelta(t, 15, work.Action.Modify.Order.Size, 1e-6)
		require.True(t, work.Action.Modify.Order.ReduceOnly)
		oid, ok := work.Action.Modify.Oid.(string)
		require.True(t, ok, "expected string OID")
		require.Equal(t, tpMD.Hex(), oid)
	})
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

	recordDeal(t, store, dealID, botID, coin)

	baseMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	tpMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 2}
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
	require.NoError(t, recordEvent(store, baseMD, baseEvent))
	require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 10, 0, 35, now.Add(-9*time.Minute))))

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
	tpRowID, err := store.RecordThreeCommasBotEvent(ctx, tpMD, tpEvent)
	require.NoError(t, err)

	create := adapter.ToCreateOrderRequest(coin, recomma.BotEvent{BotEvent: tpEvent}, tpMD)
	require.True(t, create.ReduceOnly)
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, tpMD, create, tpRowID))

	canceled := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueCanceled, 10, 10, 37, now.Add(-7*time.Minute))
	require.NoError(t, recordStatus(store, tpMD, canceled))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Nil(t, snapshot.ActiveTakeProfit)
	require.InDelta(t, 10, snapshot.Position.NetQty, 1e-6)

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)

	actions := emitter.Actions()
	require.Len(t, actions, 1)
	work := actions[0]
	require.Equal(t, recomma.ActionCreate, work.Action.Type)
	require.NotNil(t, work.Action.Create)
	require.InDelta(t, snapshot.Position.NetQty, work.Action.Create.Size, 1e-6)
	require.True(t, work.Action.Create.ReduceOnly)
	require.Equal(t, tpMD.Hex(), work.MD.Hex())
}

func TestReconcileTakeProfitsRecreatesAfterCancelWithMissingMetadata(t *testing.T) {
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

	recordDeal(t, store, dealID, botID, coin)

	baseMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	tpMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 2}
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
	require.NoError(t, recordEvent(store, baseMD, baseEvent))
	require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 8, 0, 12.5, now.Add(-14*time.Minute))))

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
	tpRowID, err := store.RecordThreeCommasBotEvent(ctx, tpMD, tpEvent)
	require.NoError(t, err)

	create := adapter.ToCreateOrderRequest(coin, recomma.BotEvent{BotEvent: tpEvent}, tpMD)
	require.True(t, create.ReduceOnly)
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, tpMD, create, tpRowID))

	tpStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 8, 8, 13.2, now.Add(-12*time.Minute))
	require.NoError(t, recordStatus(store, tpMD, tpStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	tracker.mu.Lock()
	state, ok := tracker.orders[tpMD.Hex()]
	require.True(t, ok, "expected tracked order state")
	state.event = nil
	state.originalQty = 8
	state.remainingQty = 6
	state.filledQty = 0
	deal := tracker.deals[dealID]
	require.NotNil(t, deal)
	deal.orders[tpMD.Hex()] = state
	deal.recompute()
	tracker.mu.Unlock()

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.NotNil(t, snapshot.ActiveTakeProfit)
	require.Nil(t, snapshot.ActiveTakeProfit.Event)
	require.Nil(t, snapshot.LastTakeProfitEvent)
	require.InDelta(t, 8, snapshot.Position.NetQty, 1e-6)

	emitter := &stubEmitter{}
	tracker.ReconcileTakeProfits(ctx, emitter)

	actions := emitter.Actions()
	require.Len(t, actions, 2)

	cancelWork := actions[0]
	require.Equal(t, recomma.ActionCancel, cancelWork.Action.Type)
	require.NotNil(t, cancelWork.Action.Cancel)
	require.Equal(t, tpMD.Hex(), cancelWork.Action.Cancel.Cloid)

	createWork := actions[1]
	require.Equal(t, recomma.ActionCreate, createWork.Action.Type)
	require.NotNil(t, createWork.Action.Create)
	require.True(t, createWork.Action.Create.ReduceOnly)
	require.InDelta(t, snapshot.Position.NetQty, createWork.Action.Create.Size, 1e-6)
	require.Equal(t, tpMD.Hex(), createWork.MD.Hex())
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

	path := filepath.Join(t.TempDir(), "tracker.db")
	store, err := storage.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
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

func recordEvent(store *storage.Storage, md metadata.Metadata, evt tc.BotEvent) error {
	_, err := store.RecordThreeCommasBotEvent(context.Background(), md, evt)
	return err
}

func recordStatus(store *storage.Storage, md metadata.Metadata, status hyperliquid.WsOrder) error {
	return store.RecordHyperliquidStatus(context.Background(), md, status)
}

func makeStatus(md metadata.Metadata, coin, side string, status hyperliquid.OrderStatusValue, original, remaining, limit float64, ts time.Time) hyperliquid.WsOrder {
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
