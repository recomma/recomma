package recomma

import (
	"context"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/metadata"
	"github.com/sonirico/go-hyperliquid"
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

type BotEventLog struct {
	RowID      int64
	BotEvent   tc.BotEvent
	Md         metadata.Metadata
	BotID      int64
	DealID     int64
	BoteventID int64
	CreatedAt  time.Time
	ObservedAt time.Time
}

func ToThreeCommasBotEvent(in []BotEvent) []tc.BotEvent {
	var out []tc.BotEvent
	for _, be := range in {
		out = append(out, be.BotEvent)
	}
	return out
}
