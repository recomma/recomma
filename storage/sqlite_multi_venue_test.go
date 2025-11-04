package storage

import (
	"context"
	"encoding/json"
	"testing"

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
