package filltracker

import (
	"context"
	"io"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/recomma/recomma/metadata"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
	tc "github.com/terwey/3commas-sdk-go/threecommas"

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

func TestCancelCompletedTakeProfits(t *testing.T) {
	t.Parallel()

	const (
		dealID = uint32(777)
		botID  = uint32(88)
		coin   = "SOL"
	)

	t.Run("emits cancel when all buys filled", func(t *testing.T) {
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

		require.NoError(t, recordEvent(store, tpMD, tc.BotEvent{
			CreatedAt: now.Add(-8 * time.Minute),
			Action:    tc.BotEventActionPlace,
			Coin:      coin,
			Type:      tc.SELL,
			Status:    tc.Active,
			Price:     37,
			Size:      10,
			OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		}))
		tpStatus := makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 10, 10, 37, now.Add(-7*time.Minute))
		require.NoError(t, recordStatus(store, tpMD, tpStatus))

		require.NoError(t, tracker.Rebuild(ctx))

		emitter := &stubEmitter{}
		tracker.CancelCompletedTakeProfits(ctx, emitter)

		actions := emitter.Actions()
		require.Len(t, actions, 1)
		require.Equal(t, recomma.ActionCancel, actions[0].Action.Type)
		require.NotNil(t, actions[0].Action.Cancel)
		require.Equal(t, tpMD.Hex(), actions[0].Action.Cancel.Cloid)
		require.Equal(t, coin, actions[0].Action.Cancel.Coin)
	})

	t.Run("skip cancel when buys outstanding", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		store := newTestStore(t)
		logger := newTestLogger()
		tracker := New(store, logger)

		recordDeal(t, store, dealID+1, botID, coin)

		baseMD := metadata.Metadata{BotID: botID, DealID: dealID + 1, BotEventID: 1}
		safetyMD := metadata.Metadata{BotID: botID, DealID: dealID + 1, BotEventID: 2}
		tpMD := metadata.Metadata{BotID: botID, DealID: dealID + 1, BotEventID: 3}
		now := time.Now()

		// Base filled.
		require.NoError(t, recordEvent(store, baseMD, tc.BotEvent{
			CreatedAt: now.Add(-10 * time.Minute),
			Action:    tc.BotEventActionExecute,
			Coin:      coin,
			Type:      tc.BUY,
			Status:    tc.Filled,
			Price:     30,
			Size:      5,
			OrderType: tc.MarketOrderDealOrderTypeBase,
		}))
		require.NoError(t, recordStatus(store, baseMD, makeStatus(baseMD, coin, "B", hyperliquid.OrderStatusValueFilled, 5, 0, 30, now.Add(-9*time.Minute))))

		// Safety still open.
		require.NoError(t, recordEvent(store, safetyMD, tc.BotEvent{
			CreatedAt:     now.Add(-8 * time.Minute),
			Action:        tc.BotEventActionPlace,
			Coin:          coin,
			Type:          tc.BUY,
			Status:        tc.Active,
			Price:         28,
			Size:          5,
			OrderType:     tc.MarketOrderDealOrderTypeSafety,
			OrderPosition: 1,
			OrderSize:     2,
		}))
		require.NoError(t, recordStatus(store, safetyMD, makeStatus(safetyMD, coin, "B", hyperliquid.OrderStatusValueOpen, 5, 5, 28, now.Add(-7*time.Minute))))

		// TP active.
		require.NoError(t, recordEvent(store, tpMD, tc.BotEvent{
			CreatedAt: now.Add(-6 * time.Minute),
			Action:    tc.BotEventActionPlace,
			Coin:      coin,
			Type:      tc.SELL,
			Status:    tc.Active,
			Price:     32,
			Size:      10,
			OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
		}))
		require.NoError(t, recordStatus(store, tpMD, makeStatus(tpMD, coin, "S", hyperliquid.OrderStatusValueOpen, 10, 10, 32, now.Add(-5*time.Minute))))

		require.NoError(t, tracker.Rebuild(ctx))

		emitter := &stubEmitter{}
		tracker.CancelCompletedTakeProfits(ctx, emitter)

		require.Empty(t, emitter.Actions(), "no cancel expected while buys outstanding")
	})
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
