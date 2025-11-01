package adapter

import (
	"math"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/sonirico/go-hyperliquid"
)

// ToCreateOrderRequest converts one 3Commas BotEvent + its Deal OrderId
// into your venue's CreateOrderRequest.
//
// Mapping notes:
// - Coin: Deal.ToCurrency (base coin).
// - IsBuy: BotEvent.Type == "BUY".
// - Price / Size: BotEvent already exposes float64 fields.
// - ReduceOnly: true for SELL + take-profit BotEvent.
func ToCreateOrderRequest(currency string, event recomma.BotEvent, oid orderid.OrderId) hyperliquid.CreateOrderRequest {
	isBuy := event.Type == tc.BUY
	reduceOnly := !isBuy && event.OrderType == tc.MarketOrderDealOrderTypeTakeProfit

	req := hyperliquid.CreateOrderRequest{
		Coin:          currency,
		IsBuy:         isBuy,
		Price:         event.Price,
		Size:          event.Size,
		ReduceOnly:    reduceOnly,
		OrderType:     orderTypeFrom3C(event),
		ClientOrderID: oid.HexAsPointer(),
	}

	return req
}

func orderTypeFrom3C(event recomma.BotEvent) hyperliquid.OrderType {
	tif := hyperliquid.TifIoc
	if event.Status == tc.Active && !event.IsMarket {
		tif = hyperliquid.TifGtc
	}

	return hyperliquid.OrderType{
		Limit: &hyperliquid.LimitOrderType{Tif: tif},
	}
}

type orderState int

const (
	stateUnknown orderState = iota
	stateActive
	stateCancelled
	stateFilled
)

func classify(event *recomma.BotEvent) orderState {
	if event == nil {
		return stateUnknown
	}

	switch event.Status {
	case tc.Active:
		return stateActive
	case tc.Cancelled:
		return stateCancelled
	case tc.Filled, tc.Finished:
		return stateFilled
	default:
		return stateUnknown
	}
}

const (
	floatAbsEpsilon = 1e-8
	floatRelEpsilon = 1e-6
)

func diffFloat(a, b float64) bool {
	return !almostEqual(a, b)
}

func almostEqual(a, b float64) bool {
	diff := math.Abs(a - b)
	if diff <= floatAbsEpsilon {
		return true
	}

	largest := math.Max(math.Abs(a), math.Abs(b))
	if largest == 0 {
		return true
	}
	return diff/largest <= floatRelEpsilon
}

// prev is nil if we have no prior snapshot stored.
func BuildAction(
	currency string,
	prev *recomma.BotEvent,
	next recomma.BotEvent,
	oid orderid.OrderId,
) recomma.Action {
	// 1. classify transition
	prevState := classify(prev)
	nextState := classify(&next)

	switch {
	case prev == nil && nextState == stateActive:
		// New order we have never submitted → create
		req := ToCreateOrderRequest(currency, next, oid)
		return recomma.Action{Type: recomma.ActionCreate, Create: &req}
	case prev == nil:
		// Never saw it active; HL won’t have it either
		return recomma.Action{Type: recomma.ActionNone, Reason: "skipped: no prior order submitted"}
	case prevState == stateActive && nextState == stateCancelled:
		cancel := ToCancelByCloidRequest(currency, oid)
		return recomma.Action{Type: recomma.ActionCancel, Cancel: &cancel}
	case prevState == stateActive && nextState == stateFilled:
		// Nothing to do on Hyperliquid, but tell caller to persist updated snapshot
		return recomma.Action{Type: recomma.ActionNone, Reason: "filled locally"}
	case prevState == stateActive && nextState == stateActive:
		if needsModify(prev, &next) {
			modify := ToModifyOrderRequest(currency, next, oid)
			return recomma.Action{Type: recomma.ActionModify, Modify: &modify}
		}
		return recomma.Action{Type: recomma.ActionNone, Reason: "no material change"}
	default:
		return recomma.Action{Type: recomma.ActionNone, Reason: "unsupported transition"}
	}
}

func ToCancelByCloidRequest(currency string, oid orderid.OrderId) hyperliquid.CancelOrderRequestByCloid {
	return hyperliquid.CancelOrderRequestByCloid{
		Coin:  currency,
		Cloid: oid.Hex(),
	}
}

func ToModifyOrderRequest(currency string, next recomma.BotEvent, oid orderid.OrderId) hyperliquid.ModifyOrderRequest {
	req := ToCreateOrderRequest(currency, next, oid)
	return hyperliquid.ModifyOrderRequest{
		Oid:   oid.Hex(),
		Order: req,
	}
}

func needsModify(prev, next *recomma.BotEvent) bool {
	if prev == nil || next == nil {
		return false
	}

	if prev.Type != next.Type || prev.OrderType != next.OrderType {
		return true
	}

	if prev.IsMarket != next.IsMarket {
		return true
	}

	return diffFloat(prev.Price, next.Price) ||
		diffFloat(prev.Size, next.Size)
}
