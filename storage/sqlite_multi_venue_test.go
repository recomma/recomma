package storage

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	api "github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage/sqlcgen"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestStorageMultiVenueIsolation(t *testing.T) {
	stream := &captureStream{}
	store := newTestStorageWithStream(t, stream)
	ctx := context.Background()

	oid := orderid.OrderId{BotID: 7, DealID: 21, BotEventID: 9}
	alphaIdent := recomma.NewOrderIdentifier("hyperliquid:alpha", "alpha-wallet", oid)
	betaIdent := recomma.NewOrderIdentifier("hyperliquid:beta", "beta-wallet", oid)

	emptyFlags := json.RawMessage(`{}`)
	require.NoError(t, store.queries.UpsertVenue(ctx, sqlcgen.UpsertVenueParams{
		ID:          alphaIdent.Venue(),
		Type:        "hyperliquid",
		DisplayName: "Alpha Venue",
		Wallet:      alphaIdent.Wallet,
		Flags:       emptyFlags,
	}))
	require.NoError(t, store.queries.UpsertVenue(ctx, sqlcgen.UpsertVenueParams{
		ID:          betaIdent.Venue(),
		Type:        "hyperliquid",
		DisplayName: "Beta Venue",
		Wallet:      betaIdent.Wallet,
		Flags:       emptyFlags,
	}))

	alphaReq := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         2500,
		Size:          1,
		ClientOrderID: alphaIdent.OrderId.HexAsPointer(),
	}
	betaReq := alphaReq
	betaReq.ClientOrderID = betaIdent.OrderId.HexAsPointer()

	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, alphaIdent, alphaReq, 0))
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, betaIdent, betaReq, 0))

	alphaStatus := hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:    "ETH",
			Cloid:   alphaIdent.OrderId.HexAsPointer(),
			LimitPx: "2500",
			Sz:      "1",
		},
		Status:          hyperliquid.OrderStatusValueOpen,
		StatusTimestamp: 1,
	}
	betaStatus := alphaStatus
	betaStatus.Order.Cloid = betaIdent.OrderId.HexAsPointer()
	betaStatus.Status = hyperliquid.OrderStatusValueFilled

	require.NoError(t, store.RecordHyperliquidStatus(ctx, alphaIdent, alphaStatus))
	require.NoError(t, store.RecordHyperliquidStatus(ctx, betaIdent, betaStatus))

	alphaHistory, err := store.ListHyperliquidStatuses(ctx, alphaIdent)
	require.NoError(t, err)
	require.Len(t, alphaHistory, 1)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, alphaHistory[0].Status)

	betaHistory, err := store.ListHyperliquidStatuses(ctx, betaIdent)
	require.NoError(t, err)
	require.Len(t, betaHistory, 1)
	require.Equal(t, hyperliquid.OrderStatusValueFilled, betaHistory[0].Status)

	identifiers, err := store.ListHyperliquidOrderIds(ctx)
	require.NoError(t, err)
	require.Len(t, identifiers, 2)

	var alphaCount, betaCount int
	for _, ident := range identifiers {
		switch ident.Venue() {
		case alphaIdent.Venue():
			alphaCount++
			require.Equal(t, alphaIdent.Hex(), ident.Hex())
		case betaIdent.Venue():
			betaCount++
			require.Equal(t, betaIdent.Hex(), ident.Hex())
		}
	}
	require.Equal(t, 1, alphaCount)
	require.Equal(t, 1, betaCount)

	events := stream.all()
	var alphaEvent, betaEvent *api.StreamEvent
	for i := range events {
		evt := events[i]
		if evt.Identifier != nil && evt.Identifier.Venue() == alphaIdent.Venue() && evt.Type == api.HyperliquidSubmission {
			alphaEvent = &evt
		}
		if evt.Identifier != nil && evt.Identifier.Venue() == betaIdent.Venue() && evt.Type == api.HyperliquidSubmission {
			betaEvent = &evt
		}
	}
	require.NotNil(t, alphaEvent, "expected alpha submission event")
	require.NotNil(t, betaEvent, "expected beta submission event")
}

// TODO: unskip this
func TestStorageRecordsStatusForAliasWithoutSubmission(t *testing.T) {
	t.Skip()
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	store := newTestStorageWithOptions(t, WithLogger(logger))
	ctx := context.Background()

	oid := orderid.OrderId{BotID: 11, DealID: 22, BotEventID: 33}

	assignment, err := store.ResolveDefaultAlias(ctx)
	require.NoError(t, err)

	wallet := "alias-wallet"

	primaryIdent := recomma.NewOrderIdentifier("hyperliquid:testing", wallet, oid)
	aliasIdent := recomma.NewOrderIdentifier(assignment.VenueID, wallet, oid)

	emptyFlags := json.RawMessage(`{}`)
	require.NoError(t, store.queries.UpsertVenue(ctx, sqlcgen.UpsertVenueParams{
		ID:          primaryIdent.Venue(),
		Type:        "hyperliquid",
		DisplayName: "Primary Venue",
		Wallet:      wallet,
		Flags:       emptyFlags,
	}))
	createReq := hyperliquid.CreateOrderRequest{
		Coin:          "DOGE",
		IsBuy:         false,
		Price:         0.165,
		Size:          155,
		ClientOrderID: primaryIdent.OrderId.HexAsPointer(),
		ReduceOnly:    true,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, primaryIdent, createReq, 0))

	now := time.Now().UnixMilli()
	status := hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      "DOGE",
			Side:      "A",
			LimitPx:   "0.170",
			Sz:        "310",
			Oid:       now,
			Cloid:     aliasIdent.OrderId.HexAsPointer(),
			OrigSz:    "310",
			Timestamp: now,
		},
		Status:          hyperliquid.OrderStatusValueOpen,
		StatusTimestamp: now,
	}

	require.NoError(t, store.RecordHyperliquidStatus(ctx, aliasIdent, status))

	latestStatus, foundStatus, err := store.LoadHyperliquidStatus(ctx, aliasIdent)
	require.NoError(t, err)
	require.True(t, foundStatus, "expected alias status to be recorded")
	require.NotNil(t, latestStatus)

	_, foundReq, err := store.LoadHyperliquidRequest(ctx, aliasIdent)
	require.NoError(t, err)
	require.True(t, foundReq, "alias status should imply a stored submission for that venue")
}

func TestHasPrimaryVenueAssignment(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	has, err := store.HasPrimaryVenueAssignment(ctx, 101)
	require.NoError(t, err)
	require.False(t, has)

	_, err = store.UpsertVenue(ctx, "hyperliquid:alpha", api.VenueUpsertRequest{
		Type:        "hyperliquid",
		DisplayName: "Alpha",
		Wallet:      "alpha-wallet",
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.RecordBot(ctx, tc.Bot{Id: 101}, now))

	_, err = store.UpsertVenueAssignment(ctx, "hyperliquid:alpha", 101, true)
	require.NoError(t, err)

	has, err = store.HasPrimaryVenueAssignment(ctx, 101)
	require.NoError(t, err)
	require.True(t, has)
}
