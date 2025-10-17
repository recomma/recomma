package adapter

import (
	"math"

	"github.com/sonirico/go-hyperliquid"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/metadata"
	"github.com/terwey/recomma/recomma"
)

// ToCreateOrderRequest converts one 3Commas BotEvent + its Deal metadata
// into your venue's CreateOrderRequest.
//
// Mapping notes:
// - Coin: Deal.ToCurrency (base coin).
// - IsBuy: BotEvent.Type == "BUY".
// - Price / Size: BotEvent already exposes float64 fields.
// - ReduceOnly: true for SELL + take-profit BotEvent.
func ToCreateOrderRequest(d *tc.Deal, event recomma.BotEvent, md metadata.Metadata) hyperliquid.CreateOrderRequest {
	isBuy := event.Type == tc.BUY
	reduceOnly := !isBuy && event.OrderType == tc.MarketOrderDealOrderTypeTakeProfit

	req := hyperliquid.CreateOrderRequest{
		Coin:          d.ToCurrency,
		IsBuy:         isBuy,
		Price:         event.Price,
		Size:          event.Size,
		ReduceOnly:    reduceOnly,
		OrderType:     orderTypeFrom3C(event),
		ClientOrderID: md.HexAsPointer(),
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
	deal *tc.Deal,
	prev *recomma.BotEvent,
	next recomma.BotEvent,
	md metadata.Metadata,
) recomma.Action {
	// 1. classify transition
	prevState := classify(prev)
	nextState := classify(&next)

	switch {
	case prev == nil && nextState == stateActive:
		// New order we have never submitted → create
		req := ToCreateOrderRequest(deal, next, md)
		return recomma.Action{Type: recomma.ActionCreate, Create: &req}
	case prev == nil:
		// Never saw it active; HL won’t have it either
		return recomma.Action{Type: recomma.ActionNone, Reason: "skipped: no prior order submitted"}
	case prevState == stateActive && nextState == stateCancelled:
		cancel := ToCancelByCloidRequest(deal, md)
		return recomma.Action{Type: recomma.ActionCancel, Cancel: &cancel}
	case prevState == stateActive && nextState == stateFilled:
		// Nothing to do on Hyperliquid, but tell caller to persist updated snapshot
		return recomma.Action{Type: recomma.ActionNone, Reason: "filled locally"}
	case prevState == stateActive && nextState == stateActive:
		if needsModify(prev, &next) {
			modify := ToModifyOrderRequest(deal, next, md)
			return recomma.Action{Type: recomma.ActionModify, Modify: &modify}
		}
		return recomma.Action{Type: recomma.ActionNone, Reason: "no material change"}
	default:
		return recomma.Action{Type: recomma.ActionNone, Reason: "unsupported transition"}
	}
}

func ToCancelByCloidRequest(d *tc.Deal, md metadata.Metadata) hyperliquid.CancelOrderRequestByCloid {
	return hyperliquid.CancelOrderRequestByCloid{
		Coin:  d.ToCurrency,
		Cloid: md.Hex(),
	}
}

func ToModifyOrderRequest(d *tc.Deal, next recomma.BotEvent, md metadata.Metadata) hyperliquid.ModifyOrderRequest {
	req := ToCreateOrderRequest(d, next, md)
	return hyperliquid.ModifyOrderRequest{
		Oid:   hyperliquid.Cloid{Value: md.Hex()},
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
