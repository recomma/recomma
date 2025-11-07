package hl_test

import (
	"context"
	"fmt"
	"testing"

	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestOrderLifecycleCreateAndQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

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

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)
	require.NotNil(t, storedOrder.Order.Cloid)
	require.Equal(t, cloid, *storedOrder.Order.Cloid)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, result.Order.Status)
	require.Equal(t, "BTC", result.Order.Order.Coin)
}

func TestOrderLifecycleCreateFillCancel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid := orderid.OrderId{BotID: 5, DealID: 10, BotEventID: 15}
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

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)

	simulateOrderFilled(t, ts, cloid, 3000, 2.0)

	storedOrder, exists = ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "filled", storedOrder.Status)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueFilled, result.Order.Status)

	oid2 := orderid.OrderId{BotID: 5, DealID: 10, BotEventID: 16}
	cloid2 := oid2.Hex()
	order2 := order
	order2.ClientOrderID = &cloid2

	_, err = exchange.Order(ctx, order2, nil)
	require.NoError(t, err)

	_, err = exchange.CancelByCloid(ctx, order2.Coin, cloid2)
	require.NoError(t, err)

	storedOrder2, exists := ts.GetOrder(cloid2)
	require.True(t, exists)
	require.Equal(t, "canceled", storedOrder2.Status)

	result, err = info.QueryOrderByCloid(ctx, cloid2)
	require.NoError(t, err)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueCanceled, result.Order.Status)
}

func TestOrderLifecyclePartialFill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid := orderid.OrderId{BotID: 20, DealID: 30, BotEventID: 40}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "SOL",
		IsBuy:         true,
		Price:         100,
		Size:          10.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	simulateOrderPartiallyFilled(t, ts, cloid, 100, 3.0)

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, result.Order.Status)
}

func TestOrderLifecycleCancelOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid := orderid.OrderId{BotID: 4, DealID: 5, BotEventID: 6}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         false,
		Price:         3000,
		Size:          2.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	_, err = exchange.CancelByCloid(ctx, order.Coin, cloid)
	require.NoError(t, err)

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, string(hyperliquid.OrderStatusValueCanceled), storedOrder.Status)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueCanceled, result.Order.Status)
}

func TestOrderLifecycleModifyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	oid := orderid.OrderId{BotID: 7, DealID: 8, BotEventID: 9}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "SOL",
		IsBuy:         true,
		Price:         100,
		Size:          10.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)

	modifyPrice := 105.5
	modifySize := 12.0

	_, err = exchange.ModifyOrder(ctx, hyperliquid.ModifyOrderRequest{
		Oid: storedOrder.Order.Oid,
		Order: hyperliquid.CreateOrderRequest{
			Coin:          order.Coin,
			IsBuy:         order.IsBuy,
			Price:         modifyPrice,
			Size:          modifySize,
			ClientOrderID: &cloid,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		},
	})
	require.NoError(t, err)

	updatedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", updatedOrder.Status)
	require.Equal(t, fmt.Sprintf("%.8g", modifyPrice), updatedOrder.Order.LimitPx)
	require.Equal(t, fmt.Sprintf("%.8g", modifySize), updatedOrder.Order.Sz)

	result, err := info.QueryOrderByCloid(ctx, cloid)
	require.NoError(t, err)
	require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)
	require.Equal(t, hyperliquid.OrderStatusValueOpen, result.Order.Status)
	require.Equal(t, fmt.Sprintf("%.8g", modifyPrice), result.Order.Order.LimitPx)
	require.Equal(t, fmt.Sprintf("%.8g", modifySize), result.Order.Order.Sz)
}

func TestOrderLifecycleMultipleOrders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	exchange := newMockExchange(t, ts.URL())
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	orders := []struct {
		oid      orderid.OrderId
		coin     string
		price    float64
		size     float64
		isBuy    bool
		action   string
		newPrice float64
	}{
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 1}, "BTC", 50000, 1.0, true, "open", 0},
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 2}, "ETH", 3000, 2.5, false, "cancel", 0},
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 3}, "SOL", 90, 5.0, true, "modify", 95},
		{orderid.OrderId{BotID: 1, DealID: 1, BotEventID: 4}, "ARB", 1.5, 50.0, false, "open", 0},
	}

	for _, tc := range orders {
		cloid := tc.oid.Hex()
		order := hyperliquid.CreateOrderRequest{
			Coin:          tc.coin,
			IsBuy:         tc.isBuy,
			Price:         tc.price,
			Size:          tc.size,
			ClientOrderID: &cloid,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		}

		_, err := exchange.Order(ctx, order, nil)
		require.NoError(t, err)

		switch tc.action {
		case "cancel":
			_, err := exchange.CancelByCloid(ctx, tc.coin, cloid)
			require.NoError(t, err)
		case "modify":
			stored, exists := ts.GetOrder(cloid)
			require.True(t, exists)
			_, err = exchange.ModifyOrder(ctx, hyperliquid.ModifyOrderRequest{
				Oid: stored.Order.Oid,
				Order: hyperliquid.CreateOrderRequest{
					Coin:          tc.coin,
					IsBuy:         tc.isBuy,
					Price:         tc.newPrice,
					Size:          tc.size,
					ClientOrderID: &cloid,
					OrderType: hyperliquid.OrderType{
						Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
					},
				},
			})
			require.NoError(t, err)
		}
	}

	for _, tc := range orders {
		cloid := tc.oid.Hex()

		stored, exists := ts.GetOrder(cloid)
		require.True(t, exists)

		switch tc.action {
		case "cancel":
			require.Equal(t, string(hyperliquid.OrderStatusValueCanceled), stored.Status)
		default:
			require.Equal(t, string(hyperliquid.OrderStatusValueOpen), stored.Status)
		}

		result, err := info.QueryOrderByCloid(ctx, cloid)
		require.NoError(t, err)
		require.Equal(t, hyperliquid.OrderQueryStatusSuccess, result.Status)

		switch tc.action {
		case "cancel":
			require.Equal(t, hyperliquid.OrderStatusValueCanceled, result.Order.Status)
		case "modify":
			require.Equal(t, hyperliquid.OrderStatusValueOpen, result.Order.Status)
			require.Equal(t, fmt.Sprintf("%.8g", tc.newPrice), result.Order.Order.LimitPx)
		default:
			require.Equal(t, hyperliquid.OrderStatusValueOpen, result.Order.Status)
		}
	}
}

func TestOrderLifecycleOrderIdCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	info := hl.NewInfo(ctx, hl.ClientConfig{
		BaseURL: ts.URL(),
		Wallet:  "0xtest",
	})

	cache := hl.NewOrderIdCache(info)

	btcConstraints, err := cache.Resolve(ctx, "BTC")
	require.NoError(t, err)
	require.Equal(t, 5, btcConstraints.SizeDecimals)
	require.InDelta(t, 1e-5, btcConstraints.SizeStep, 1e-12)

	ethConstraints, err := cache.Resolve(ctx, "ETH")
	require.NoError(t, err)
	require.Equal(t, 4, ethConstraints.SizeDecimals)
	require.InDelta(t, 1e-4, ethConstraints.SizeStep, 1e-12)
}
