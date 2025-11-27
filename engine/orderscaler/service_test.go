package orderscaler

import (
	"context"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

type stubConstraints struct {
	constraint hl.CoinConstraints
}

func (s stubConstraints) Resolve(_ context.Context, coin string) (hl.CoinConstraints, error) {
	out := s.constraint
	out.Coin = coin
	return out, nil
}

func TestServiceScaleAppliesMultiplierAndRounding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	scaler := New(store, stubConstraints{constraint: hl.CoinConstraints{SizeStep: 0.1, PriceSigFigs: 5}}, nil)

	botID := uint32(77)
	dealID := uint32(88)
	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, time.Now())
	require.NoError(t, err)
	err = store.RecordThreeCommasDeal(ctx, tc.Deal{Id: int(dealID), BotId: int(botID), CreatedAt: time.Now(), UpdatedAt: time.Now()})
	require.NoError(t, err)

	_, err = store.UpsertOrderScaler(ctx, 0.5, "tester", nil)
	require.NoError(t, err)

	oid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	ident := defaultIdentifier(t, store, ctx, oid)
	event := tc.BotEvent{
		CreatedAt: time.Now(),
		Coin:      "BTC",
		Price:     15.0,
		Size:      4.0,
	}
	order := hyperliquid.CreateOrderRequest{
		Coin:  "BTC",
		IsBuy: true,
		Price: 15.0,
		Size:  4.0,
	}

	req := BuildRequest(ident, event, order)
	result, err := scaler.Scale(ctx, req, &order)
	require.NoError(t, err)
	require.InDelta(t, 2.0, result.Size, 1e-6)
	require.InDelta(t, 15.0, result.Price, 1e-6)
	require.False(t, result.Audit.Skipped)

	audits, err := store.ListScaledOrdersByOrderId(ctx, oid)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.InDelta(t, 4.0, audits[0].OriginalSize, 1e-6)
	require.InDelta(t, 2.0, audits[0].ScaledSize, 1e-6)
}

func TestServiceScaleRejectsBelowMinimum(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	scaler := New(store, stubConstraints{constraint: hl.CoinConstraints{SizeStep: 0.1, PriceSigFigs: 5, MinNotional: 10}}, nil)

	botID := uint32(101)
	dealID := uint32(202)
	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, time.Now())
	require.NoError(t, err)
	err = store.RecordThreeCommasDeal(ctx, tc.Deal{Id: int(dealID), BotId: int(botID), CreatedAt: time.Now(), UpdatedAt: time.Now()})
	require.NoError(t, err)

	_, err = store.UpsertOrderScaler(ctx, 0.2, "tester", nil)
	require.NoError(t, err)

	oid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 5}
	ident := defaultIdentifier(t, store, ctx, oid)
	order := hyperliquid.CreateOrderRequest{Coin: "ETH", IsBuy: true, Price: 20.0, Size: 1.0}
	event := tc.BotEvent{CreatedAt: time.Now(), Coin: "ETH", Price: 20.0, Size: 1.0}

	req := BuildRequest(ident, event, order)
	result, err := scaler.Scale(ctx, req, &order)
	require.ErrorIs(t, err, ErrBelowMinimum)

	require.True(t, result.Audit.Skipped)
	require.NotNil(t, result.Audit.SkipReason)
	require.Contains(t, *result.Audit.SkipReason, "below minimum notional")

	audits, listErr := store.ListScaledOrdersByOrderId(ctx, oid)
	require.NoError(t, listErr)
	require.Len(t, audits, 1)
	require.True(t, audits[0].Skipped)
	require.NotNil(t, audits[0].SkipReason)
}

func TestServiceScalePreservesTakeProfitStackRatios(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	scaler := New(store, stubConstraints{constraint: hl.CoinConstraints{SizeStep: 0.1, PriceSigFigs: 5}}, nil)

	botID := uint32(303)
	dealID := uint32(404)
	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, time.Now())
	require.NoError(t, err)
	err = store.RecordThreeCommasDeal(ctx, tc.Deal{Id: int(dealID), BotId: int(botID), CreatedAt: time.Now(), UpdatedAt: time.Now()})
	require.NoError(t, err)

	_, err = store.UpsertOrderScaler(ctx, 0.5, "tester", nil)
	require.NoError(t, err)

	baseTime := time.Now()

	legEvents := []tc.BotEvent{
		{
			CreatedAt:     baseTime,
			Action:        tc.BotEventActionPlace,
			Coin:          "ETH",
			Price:         15.0,
			Size:          1.0,
			OrderType:     tc.MarketOrderDealOrderTypeTakeProfit,
			OrderSize:     2,
			OrderPosition: 1,
		},
		{
			CreatedAt:     baseTime.Add(100 * time.Millisecond),
			Action:        tc.BotEventActionPlace,
			Coin:          "ETH",
			Price:         15.5,
			Size:          1.05,
			OrderType:     tc.MarketOrderDealOrderTypeTakeProfit,
			OrderSize:     2,
			OrderPosition: 2,
		},
	}

	var results []Result
	for _, evt := range legEvents {
		oid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: evt.FingerprintAsID()}
		ident := defaultIdentifier(t, store, ctx, oid)
		_, err := store.RecordThreeCommasBotEvent(ctx, oid, evt)
		require.NoError(t, err)

		order := hyperliquid.CreateOrderRequest{Coin: evt.Coin, IsBuy: false, Price: evt.Price, Size: evt.Size}
		req := BuildRequest(ident, evt, order)
		result, err := scaler.Scale(ctx, req, &order)
		require.NoError(t, err)
		results = append(results, result)
	}

	require.Len(t, results, 2)
	require.InDelta(t, 0.5, results[0].Size, 1e-6)
	require.InDelta(t, 0.6, results[1].Size, 1e-6)
	require.Greater(t, results[1].Size, results[0].Size)
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

func defaultIdentifier(t *testing.T, store *storage.Storage, ctx context.Context, oid orderid.OrderId) recomma.OrderIdentifier {
	t.Helper()
	assignment, err := store.ResolveDefaultAlias(ctx)
	require.NoError(t, err)
	return recomma.NewOrderIdentifier(assignment.VenueID, assignment.Wallet, oid)
}
