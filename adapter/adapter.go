package adapter

import (
	"strconv"
	"strings"

	"github.com/sonirico/go-hyperliquid"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/metadata"
)

// ToCreateOrderRequest converts one 3Commas MarketOrder + its Deal metadata
// into your venue's CreateOrderRequest.
//
// Mapping notes:
// - Coin: Deal.ToCurrency (base coin). If empty, falls back to Deal.Pair's left side.
// - IsBuy: MarketOrder.OrderType == "BUY" (case-insensitive).
// - Price: prefer MarketOrder.Rate, else AveragePrice.
// - Size: if Active and QuantityRemaining>0, use remaining; else Quantity.
// - ReduceOnly: true for SELL + DealOrderType containing "take profit"/"close".
func ToCreateOrderRequest(d tc.Deal, m tc.MarketOrder, md metadata.Metadata) hyperliquid.CreateOrderRequest {
	isBuy := m.OrderType == tc.BUY

	price := firstFloat(m.Rate, m.AveragePrice)
	size := chooseSize(m)

	reduceOnly := false
	if !isBuy {
		if m.DealOrderType == tc.MarketOrderDealOrderTypeTakeProfit {
			reduceOnly = true
		}
	}

	req := hyperliquid.CreateOrderRequest{
		Coin:          d.ToCurrency,
		IsBuy:         isBuy,
		Price:         price,
		Size:          size,
		ReduceOnly:    reduceOnly,
		OrderType:     orderTypeFrom3C(m),
		ClientOrderID: md.HexAsPointer(),
	}
	return req
}

func chooseSize(m tc.MarketOrder) float64 {
	// status_string & quantity fields come from your generated model
	// (strings representing numbers). Use remaining if active & >0.
	if strings.EqualFold(string(m.StatusString), "Active") {
		if f := parseFloat(m.QuantityRemaining); f > 0 {
			return f
		}
	}
	return parseFloat(m.Quantity)
}

func firstFloat(vals ...string) float64 {
	for _, v := range vals {
		if f := parseFloat(v); f > 0 {
			return f
		}
	}
	return 0
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func strPtr(s string) *string { return &s }

func orderTypeFrom3C(m tc.MarketOrder) hyperliquid.OrderType {
	if m.StatusString == tc.Active {
		return hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		}
	}
	return hyperliquid.OrderType{
		Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
	}
}
