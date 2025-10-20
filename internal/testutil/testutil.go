// internal/testutil/events.go
package testutil

import (
	"testing"
	"time"

	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/metadata"
)

type EventOpt func(*tc.BotEvent)

func NewBotEvent(t *testing.T, base time.Time, botID uint32,
	dealID uint32, opts ...EventOpt) (tc.BotEvent, metadata.Metadata) {
	t.Helper()

	evt := tc.BotEvent{
		CreatedAt:     ptr(base.UTC()),
		Action:        tc.BotEventActionPlace,
		Coin:          "BTC",
		Type:          tc.MarketOrderOrderType(tc.BUY),
		Status:        tc.MarketOrderStatusString(tc.Active),
		Price:         1.0,
		Size:          1.0,
		OrderType:     tc.MarketOrderDealOrderTypeBase,
		OrderSize:     1,
		OrderPosition: 1,
		IsMarket:      false,
	}
	for _, opt := range opts {
		opt(&evt)
	}
	md := metadata.Metadata{
		BotID:      uint32(botID),
		DealID:     uint32(dealID),
		BotEventID: evt.FingerprintAsID(),
	}
	return evt, md
}

// Modifiers
func WithStatus(status tc.MarketOrderStatusString) EventOpt {
	return func(e *tc.BotEvent) { e.Status = status }
}
func WithPrice(val float64) EventOpt {
	return func(e *tc.BotEvent) { e.Price = val }
}
func WithSize(val float64) EventOpt {
	return func(e *tc.BotEvent) { e.Size = val }
}
func WithOrderType(typ tc.MarketOrderDealOrderType) EventOpt {
	return func(e *tc.BotEvent) { e.OrderType = typ }
}
func WithType(typ tc.MarketOrderOrderType) EventOpt {
	return func(e *tc.BotEvent) { e.Type = typ }
}
func WithAction(action tc.BotEventAction) EventOpt {
	return func(e *tc.BotEvent) { e.Action = action }
}
func WithCreatedAt(ts time.Time) EventOpt {
	return func(e *tc.BotEvent) { e.CreatedAt = ptr(ts.UTC()) }
}
func WithMarket(isMarket bool) EventOpt {
	return func(e *tc.BotEvent) { e.IsMarket = isMarket }
}

func ptr(t time.Time) *time.Time { return &t }
