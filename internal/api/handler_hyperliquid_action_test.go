package api

import (
	"testing"

	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestConvertHyperliquidAction_CreateOrder(t *testing.T) {
	cloid := "CLOID-create"
	req := hyperliquid.CreateOrderRequest{
		Coin:       "ARB",
		IsBuy:      true,
		Price:      12.34,
		Size:       4.5,
		ReduceOnly: true,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		ClientOrderID: &cloid,
	}

	action, ok := convertHyperliquidAction(&req)
	require.True(t, ok)

	create, err := action.AsHyperliquidCreateAction()
	require.NoError(t, err)
	require.Equal(t, req.Coin, create.Order.Coin)
	require.Equal(t, req.Price, create.Order.Price)
	require.NotNil(t, create.Order.Cloid)
	require.Equal(t, cloid, *create.Order.Cloid)
	require.NotNil(t, create.Order.OrderType.Limit)
	require.Equal(t, string(hyperliquid.TifGtc), create.Order.OrderType.Limit.Tif)
}

func TestConvertHyperliquidAction_ModifyFallsBackToClientOrderID(t *testing.T) {
	cloid := "0xCLOID"
	req := hyperliquid.ModifyOrderRequest{
		Order: hyperliquid.CreateOrderRequest{
			Coin:       "SOL",
			IsBuy:      false,
			Price:      22.5,
			Size:       1.25,
			ReduceOnly: false,
			OrderType: hyperliquid.OrderType{
				Trigger: &hyperliquid.TriggerOrderType{
					TriggerPx: 21.0,
					IsMarket:  true,
					Tpsl:      hyperliquid.TakeProfit,
				},
			},
			ClientOrderID: &cloid,
		},
	}

	action, ok := convertHyperliquidAction(req)
	require.True(t, ok)

	modify, err := action.AsHyperliquidModifyAction()
	require.NoError(t, err)
	require.Nil(t, modify.Oid)
	require.NotNil(t, modify.Cloid)
	require.Equal(t, cloid, *modify.Cloid)
	require.Equal(t, req.Order.Coin, modify.Order.Coin)
	require.NotNil(t, modify.Order.OrderType.Trigger)
	require.Equal(t, req.Order.OrderType.Trigger.TriggerPx, modify.Order.OrderType.Trigger.TriggerPx)

	_, ok2 := convertHyperliquidAction(&req)
	require.True(t, ok2)
}

func TestConvertHyperliquidAction_CancelOrder(t *testing.T) {
	req := hyperliquid.CancelOrderRequestByCloid{
		Coin:  "BTC",
		Cloid: "CANCEL-CLOID",
	}

	action, ok := convertHyperliquidAction(req)
	require.True(t, ok)

	cancel, err := action.AsHyperliquidCancelAction()
	require.NoError(t, err)
	require.Equal(t, req.Coin, cancel.Cancel.Coin)
	require.Equal(t, req.Cloid, cancel.Cancel.Cloid)

	_, ok2 := convertHyperliquidAction(&req)
	require.True(t, ok2)
}

func TestConvertHyperliquidAction_InvalidPayloads(t *testing.T) {
	var nilCreate *hyperliquid.CreateOrderRequest
	_, ok := convertHyperliquidAction(nilCreate)
	require.False(t, ok)

	_, ok = convertHyperliquidAction(struct{}{})
	require.False(t, ok)
}
