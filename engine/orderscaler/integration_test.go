package orderscaler

import (
	"context"
	"crypto/ecdsa"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestOrderScalerEmitsScaledOrderThroughEmitter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newTestStore(t)

	botID := uint32(5150)
	dealID := uint32(4242)
	now := time.Now()

	require.NoError(t, store.RecordBot(ctx, tc.Bot{Id: int(botID)}, now))
	require.NoError(t, store.RecordThreeCommasDeal(ctx, tc.Deal{
		Id:        int(dealID),
		BotId:     int(botID),
		CreatedAt: now,
		UpdatedAt: now,
	}))

	_, err := store.UpsertOrderScaler(ctx, 0.5, "integration-test", nil)
	require.NoError(t, err)

	oid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 777}
	event := tc.BotEvent{
		CreatedAt: now,
		Action:    tc.BotEventActionPlace,
		Coin:      "BTC",
		Price:     20000,
		Size:      4,
	}
	rowID, err := store.RecordThreeCommasBotEvent(ctx, oid, event)
	require.NoError(t, err)
	require.NotZero(t, rowID)

	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         event.Price,
		Size:          event.Size,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	info := hl.NewInfo(ctx, hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)

	scaler := New(store, cache, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := BuildRequest(oid, event, order)

	result, err := scaler.Scale(ctx, req, &order)
	require.NoError(t, err)
	require.InDelta(t, 2.0, result.Size, 1e-9, "multiplier 0.5 should halve the size")

	exchange := newMockExchangeForScaler(t, ts.URL())
	hlEmitter := emitter.NewHyperLiquidEmitter(exchange, nil, store,
		emitter.WithHyperLiquidEmitterLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	work := recomma.OrderWork{
		OrderId: oid,
		Action:  recomma.Action{Type: recomma.ActionCreate, Create: &order},
		BotEvent: recomma.BotEvent{
			RowID:    rowID,
			BotEvent: event,
		},
	}

	require.NoError(t, hlEmitter.Emit(ctx, work))

	createReq, found, err := store.LoadHyperliquidRequest(ctx, oid)
	require.NoError(t, err)
	require.True(t, found, "scaled order create should be persisted")
	require.NotNil(t, createReq)
	require.InDelta(t, result.Size, createReq.Size, 1e-9)
	require.InDelta(t, result.Price, createReq.Price, 1e-9)

	audits, err := store.ListScaledOrdersByOrderId(ctx, oid)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.InDelta(t, result.Size, audits[0].ScaledSize, 1e-9)
	require.False(t, audits[0].Skipped)

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists, "mock exchange should track the emitted order")
	sz, err := strconv.ParseFloat(storedOrder.Order.Sz, 64)
	require.NoError(t, err)
	require.InDelta(t, result.Size, sz, 1e-9)
}

func newMockExchangeForScaler(t *testing.T, baseURL string) *hyperliquid.Exchange {
	t.Helper()

	ctx := context.Background()

	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("unexpected public key type %T", pub)
	}

	walletAddr := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(
		ctx,
		privateKey,
		baseURL,
		nil,
		"",
		walletAddr,
		nil,
	)

	return exchange
}
