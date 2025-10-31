package orderscaler

import (
	"context"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/metadata"
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

	md := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
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

	req := BuildRequest(md, event, order)
	result, err := scaler.Scale(ctx, req, &order)
	require.NoError(t, err)
	require.InDelta(t, 2.0, result.Size, 1e-6)
	require.InDelta(t, 15.0, result.Price, 1e-6)

	audits, err := store.ListScaledOrdersByMetadata(ctx, md)
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

	md := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 5}
	order := hyperliquid.CreateOrderRequest{Coin: "ETH", IsBuy: true, Price: 20.0, Size: 1.0}
	event := tc.BotEvent{CreatedAt: time.Now(), Coin: "ETH", Price: 20.0, Size: 1.0}

	req := BuildRequest(md, event, order)
	_, err = scaler.Scale(ctx, req, &order)
	require.ErrorIs(t, err, ErrBelowMinimum)
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
