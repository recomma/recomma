package hl

import (
	"context"
	"encoding/hex"
	"log/slog"
	"os"
	"testing"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/stretchr/testify/require"
)

func TestNewExchangeWithMockServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ts := mockserver.NewTestServer(t, mockserver.WithLogger(logger))

	exchange, err := NewExchange(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Key:     mustHexPrivateKey(t),
	})
	require.NoError(t, err)
	require.NotNil(t, exchange)

	infoReqs := ts.GetInfoRequests()
	require.NotEmpty(t, infoReqs, "exchange creation should fetch metadata from mock server")

	var sawPerpMeta, sawSpotMeta bool
	for _, req := range infoReqs {
		switch req.Type {
		case "meta", "metaAndAssetCtxs":
			sawPerpMeta = true
		case "spotMeta", "spotMetaAndAssetCtxs":
			sawSpotMeta = true
		}
	}

	require.True(t, sawPerpMeta, "expected Hyperliquid meta fetch during exchange init")
	require.True(t, sawSpotMeta, "expected Hyperliquid spot meta fetch during exchange init")
}

func TestOrderIdCacheResolvesConstraintsFromMockInfo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := NewInfo(ctx, ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xabc123",
	})
	cache := NewOrderIdCache(info)

	initialRequests := len(ts.GetInfoRequests())

	btc, err := cache.Resolve(ctx, "BTC")
	require.NoError(t, err)
	require.Equal(t, "BTC", btc.Coin)
	require.Equal(t, 5, btc.SizeDecimals)
	require.InDelta(t, 0.00001, btc.SizeStep, 1e-12)
	require.Equal(t, 5, btc.PriceSigFigs)

	afterFirstResolve := len(ts.GetInfoRequests())
	require.Greater(t, afterFirstResolve, initialRequests, "first resolve should fetch metadata")

	eth, err := cache.Resolve(ctx, "ETH")
	require.NoError(t, err)
	require.Equal(t, "ETH", eth.Coin)
	require.Equal(t, 4, eth.SizeDecimals)
	require.InDelta(t, 0.0001, eth.SizeStep, 1e-12)
	require.Equal(t, 5, eth.PriceSigFigs)

	afterSecondResolve := len(ts.GetInfoRequests())
	require.Equal(t, afterFirstResolve, afterSecondResolve, "metadata must be cached between resolves")
}

func mustHexPrivateKey(t *testing.T) string {
	t.Helper()

	priv, err := gethCrypto.GenerateKey()
	require.NoError(t, err)
	return hex.EncodeToString(gethCrypto.FromECDSA(priv))
}
