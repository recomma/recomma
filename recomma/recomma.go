package recomma

import (
	"context"

	"github.com/sonirico/go-hyperliquid"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/metadata"
)

type OrderWork struct {
	MD       metadata.Metadata
	Action   Action
	BotEvent BotEvent
}

type Emitter interface {
	Emit(ctx context.Context, w OrderWork) error
}

type ActionType int

const (
	ActionNone ActionType = iota
	ActionCreate
	ActionModify
	ActionCancel
)

type Action struct {
	Type   ActionType
	Create *hyperliquid.CreateOrderRequest
	Modify *hyperliquid.ModifyOrderRequest
	Cancel *hyperliquid.CancelOrderRequestByCloid
	Reason string // optional human hint for ActionNone
}

type BotEvent struct {
	RowID int64
	tc.BotEvent
}

func ToThreeCommasBotEvent(in []BotEvent) []tc.BotEvent {
	var out []tc.BotEvent
	for _, be := range in {
		out = append(out, be.BotEvent)
	}
	return out
}
