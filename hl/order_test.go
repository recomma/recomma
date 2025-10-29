package hl

import (
	"testing"

	"github.com/recomma/recomma/metadata"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestOrderResultToWsOrder(t *testing.T) {
	md := metadata.Metadata{BotID: 1, DealID: 2, BotEventID: 3}
	cloid := md.Hex()

	result := &hyperliquid.OrderQueryResult{
		Status: hyperliquid.OrderQueryStatusSuccess,
		Order: hyperliquid.OrderQueryResponse{
			Order: hyperliquid.QueriedOrder{
				Coin:      "ETH",
				Side:      hyperliquid.OrderSideAsk,
				LimitPx:   "2000",
				Sz:        "1.5",
				Oid:       123,
				Timestamp: 456,
				OrigSz:    "1.5",
				Cloid:     nil,
			},
			Status:          hyperliquid.OrderStatusValueFilled,
			StatusTimestamp: 789,
		},
	}

	wsOrder, err := orderResultToWsOrder(md, result)
	require.NoError(t, err)
	require.NotNil(t, wsOrder)
	require.Equal(t, hyperliquid.OrderStatusValueFilled, wsOrder.Status)
	require.NotNil(t, wsOrder.Order.Cloid)
	require.Equal(t, cloid, *wsOrder.Order.Cloid)
	require.Equal(t, "ETH", wsOrder.Order.Coin)
	require.Equal(t, "1.5", wsOrder.Order.Sz)
	require.Equal(t, int64(123), wsOrder.Order.Oid)
	require.Equal(t, int64(789), wsOrder.StatusTimestamp)
}

func TestOrderResultToWsOrderUnknown(t *testing.T) {
	md := metadata.Metadata{BotID: 1, DealID: 2, BotEventID: 3}
	result := &hyperliquid.OrderQueryResult{
		Status: hyperliquid.OrderQueryStatusError,
	}

	wsOrder, err := orderResultToWsOrder(md, result)
	require.NoError(t, err)
	require.Nil(t, wsOrder)
}
