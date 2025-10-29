package hl

import (
	"fmt"

	"github.com/recomma/recomma/metadata"
	"github.com/sonirico/go-hyperliquid"
)

// orderResultToWsOrder converts an order status query result into the WsOrder
// format used by websocket updates. Returns nil when the result does not
// contain a valid order payload (e.g. unknown CLOID).
func orderResultToWsOrder(md metadata.Metadata, result *hyperliquid.OrderQueryResult) (*hyperliquid.WsOrder, error) {
	if result == nil {
		return nil, fmt.Errorf("order query result is nil")
	}
	if result.Status != hyperliquid.OrderQueryStatusSuccess {
		return nil, nil
	}

	order := result.Order
	wsOrder := hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      order.Order.Coin,
			Side:      string(order.Order.Side),
			LimitPx:   order.Order.LimitPx,
			Sz:        order.Order.Sz,
			Oid:       order.Order.Oid,
			Timestamp: order.Order.Timestamp,
			OrigSz:    order.Order.OrigSz,
			Cloid:     order.Order.Cloid,
		},
		Status:          order.Status,
		StatusTimestamp: order.StatusTimestamp,
	}

	if wsOrder.Order.Cloid == nil {
		cloid := md.Hex()
		wsOrder.Order.Cloid = &cloid
	}

	return &wsOrder, nil
}
