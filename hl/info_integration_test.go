package hl_test

import (
	"context"
	"testing"

	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestInfoQueryOrderByCloid(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Query the order
	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, "BTC", result.Order.Order.Coin)
	require.NotNil(t, result.Order.Order.Cloid)
	require.Equal(t, cloid, *result.Order.Order.Cloid)
}

func TestInfoQueryNonExistentOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid := orderid.OrderId{BotID: 999, DealID: 888, BotEventID: 777}
	cloid := oid.Hex()

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Mock server should return error status for non-existent orders
	require.NotEqual(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
}

func TestInfoQueryCanceledOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 5, DealID: 6, BotEventID: 7}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "SOL",
		IsBuy:         false,
		Price:         100,
		Size:          10.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Cancel the order
	_, err = exchange.CancelByCloid(ctx, order.Coin, cloid)
	require.NoError(t, err)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueCanceled, result.Order.Status)
}

func TestInfoQueryFilledOrder(t *testing.T) {
	t.Parallel()

	t.Skip("filled order simulation not supported by mock server")

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 10, DealID: 20, BotEventID: 30}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         3000,
		Size:          2.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	simulateOrderFilled(t, ts, cloid, 3000, 2.0)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueFilled, result.Order.Status)
	require.Equal(t, "ETH", result.Order.Order.Coin)
}

func TestInfoQueryPartiallyFilledOrder(t *testing.T) {
	t.Parallel()

	t.Skip("partial fill simulation not supported by mock server")

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 15, DealID: 25, BotEventID: 35}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "DOGE",
		IsBuy:         true,
		Price:         0.15,
		Size:          1000.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	simulateOrderPartiallyFilled(t, ts, cloid, 0.15, 500.0)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, result.Order.Status)
}

func TestInfoQueryMultipleOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	exchange := newMockExchange(t, ts.URL())

	// Create multiple orders
	const orderCount = 5
	oids := make([]orderid.OrderId, orderCount)
	for i := 0; i < orderCount; i++ {
		oid := orderid.OrderId{BotID: uint32(i + 1), DealID: 100, BotEventID: uint32(i + 1)}
		oids[i] = oid
		cloid := oid.Hex()

		order := hyperliquid.CreateOrderRequest{
			Coin:          "BTC",
			IsBuy:         i%2 == 0, // Alternate buy/sell
			Price:         50000 + float64(i*100),
			Size:          0.1,
			ClientOrderID: &cloid,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		}

		_, err := exchange.Order(ctx, order, nil)
		require.NoError(t, err)
	}

	// Query all orders
	for _, oid := range oids {
		result, err := info.QueryOrderByCloid(ctx, oid.Hex())
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
		require.Equal(t, "BTC", result.Order.Order.Coin)
	}
}

func TestInfoQueryOrderConversionToWsOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	exchange := newMockExchange(t, ts.URL())

	oid := orderid.OrderId{BotID: 42, DealID: 84, BotEventID: 126}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ARB",
		IsBuy:         true,
		Price:         1.5,
		Size:          100.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	store := newIntegrationTestStore(t)
	ident := testIdentifier(oid)
	store.RecordOrder(ident)

	refresher := hl.NewStatusRefresher(
		hl.StatusClientRegistry{hyperliquidTestVenue: info},
		store,
	)
	require.NoError(t, refresher.Refresh(ctx))

	status, found := store.Status(ident)
	require.True(t, found)
	require.Equal(t, "ARB", status.Order.Coin)
	require.NotNil(t, status.Order.Cloid)
	require.Equal(t, cloid, *status.Order.Cloid)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, status.Status)
}

func simulateOrderFilled(t *testing.T, ts *mockserver.TestServer, cloid string, price float64, size float64) {
	t.Helper()
	t.Fatalf("mock server fill simulation not implemented: update to call TestServer.FillOrder when available (cloid=%s price=%.8f size=%.8f)", cloid, price, size)
}

func simulateOrderPartiallyFilled(t *testing.T, ts *mockserver.TestServer, cloid string, price float64, filledSize float64) {
	t.Helper()
	t.Fatalf("mock server partial fill simulation not implemented: update to call TestServer.FillOrder when available (cloid=%s price=%.8f filledSize=%.8f)", cloid, price, filledSize)
}
