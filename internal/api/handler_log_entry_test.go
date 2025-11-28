package api

import (
	"context"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"

	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
)

func newLogEntryHandler(t *testing.T) *ApiHandler {
	t.Helper()
	handler, _, cleanup := newOrderScalerTestHarness(t)
	t.Cleanup(cleanup)
	return handler
}

func TestMakeOrderLogEntry_ThreeCommasEvent(t *testing.T) {
	handler := newLogEntryHandler(t)

	ctx := context.Background()
	observedAt := time.Now().UTC().Truncate(time.Second)
	baseIdentifiers := &OrderIdentifiers{
		Hex:        "base",
		BotId:      99,
		DealId:     98,
		BotEventId: 97,
		CreatedAt:  observedAt.Add(-2 * time.Hour),
		VenueId:    "hyperliquid:one",
		Wallet:     "0xbase",
	}
	botEvent := &tc.BotEvent{
		CreatedAt:        observedAt.Add(-time.Minute),
		Action:           tc.BotEventAction("Execute"),
		Coin:             "ETH",
		Type:             tc.MarketOrderOrderType("buy"),
		Status:           tc.MarketOrderStatusString("filled"),
		Price:            2410.5,
		Size:             2.5,
		OrderType:        tc.MarketOrderDealOrderType("safety"),
		OrderSize:        2,
		OrderPosition:    1,
		QuoteVolume:      6041,
		QuoteCurrency:    "USDT",
		IsMarket:         true,
		Profit:           1.25,
		ProfitCurrency:   "USDT",
		ProfitUSD:        1.25,
		ProfitPercentage: 0.5,
		Text:             "unit test event",
	}
	oid := orderid.OrderId{BotID: 7, DealID: 11, BotEventID: 13}
	sequence := int64(42)

	entry, ok := handler.makeOrderLogEntry(ctx, oid, observedAt, ThreeCommasEvent, botEvent, nil, nil, nil, nil, nil, nil, baseIdentifiers, &sequence)
	require.True(t, ok)

	logEntry, err := entry.AsThreeCommasLogEntry()
	require.NoError(t, err)
	require.Equal(t, oid.Hex(), logEntry.OrderId)
	require.Equal(t, observedAt, logEntry.ObservedAt)
	require.NotNil(t, logEntry.BotEventId)
	require.Equal(t, int64(oid.BotEventID), *logEntry.BotEventId)
	require.NotNil(t, logEntry.Sequence)
	require.Equal(t, sequence, *logEntry.Sequence)
	require.NotNil(t, logEntry.Identifiers)
	require.Equal(t, botEvent.CreatedAt, logEntry.Identifiers.CreatedAt)
	require.Equal(t, botEvent.Coin, logEntry.Event.Coin)
}

func TestMakeOrderLogEntry_HyperliquidSubmission(t *testing.T) {
	handler := newLogEntryHandler(t)
	ctx := context.Background()
	observedAt := time.Now().UTC()
	oid := orderid.OrderId{BotID: 3, DealID: 5, BotEventID: 7}

	cloid := "CLOID-123"
	submission := hyperliquid.CreateOrderRequest{
		Coin:       "SOL",
		IsBuy:      true,
		Price:      19.25,
		Size:       3.5,
		ReduceOnly: true,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
		ClientOrderID: &cloid,
	}
	logIdent := recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "0xabc", oid)

	entry, ok := handler.makeOrderLogEntry(ctx, oid, observedAt, HyperliquidSubmission, nil, submission, nil, nil, nil, nil, &logIdent, nil, nil)
	require.True(t, ok)

	logEntry, err := entry.AsHyperliquidSubmissionLogEntry()
	require.NoError(t, err)
	require.Equal(t, oid.Hex(), logEntry.OrderId)
	require.NotNil(t, logEntry.Identifiers)
	require.Equal(t, logIdent.Venue(), logEntry.Identifiers.VenueId)
	require.Equal(t, logIdent.Wallet, logEntry.Identifiers.Wallet)
	require.Equal(t, observedAt, logEntry.Identifiers.CreatedAt)

	action, err := logEntry.Action.AsHyperliquidCreateAction()
	require.NoError(t, err)
	require.Equal(t, submission.Coin, action.Order.Coin)
	require.NotNil(t, action.Order.Cloid)
	require.Equal(t, cloid, *action.Order.Cloid)
}

func TestMakeOrderLogEntry_HyperliquidStatus(t *testing.T) {
	handler := newLogEntryHandler(t)
	ctx := context.Background()
	observedAt := time.Now().UTC()
	oid := orderid.OrderId{BotID: 4, DealID: 6, BotEventID: 8}
	baseIdentifiers := &OrderIdentifiers{
		Hex:        "base",
		BotId:      1,
		DealId:     2,
		BotEventId: 3,
		CreatedAt:  observedAt.Add(-time.Hour),
		VenueId:    "hyperliquid:one",
		Wallet:     "0xwallet",
	}

	status := &hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      "DOGE",
			Side:      "B",
			LimitPx:   "0.123",
			Sz:        "100",
			Oid:       12345,
			Timestamp: observedAt.UnixMilli(),
			OrigSz:    "100",
		},
		Status:          hyperliquid.OrderStatusValueOpen,
		StatusTimestamp: observedAt.UnixMilli(),
	}

	entry, ok := handler.makeOrderLogEntry(ctx, oid, observedAt, HyperliquidStatus, nil, nil, status, nil, nil, nil, nil, baseIdentifiers, nil)
	require.True(t, ok)

	logEntry, err := entry.AsHyperliquidStatusLogEntry()
	require.NoError(t, err)
	require.Equal(t, oid.Hex(), logEntry.OrderId)
	require.NotNil(t, logEntry.Identifiers)
	require.Equal(t, baseIdentifiers.VenueId, logEntry.Identifiers.VenueId)
	require.Equal(t, baseIdentifiers.Wallet, logEntry.Identifiers.Wallet)
	require.Equal(t, baseIdentifiers.CreatedAt, logEntry.Identifiers.CreatedAt)
	require.Equal(t, status.Order.Coin, logEntry.Status.Order.Coin)
	require.Equal(t, string(status.Status), string(logEntry.Status.Status))
}

func TestMakeOrderLogEntry_ScaledOrderAuditIncludesEffective(t *testing.T) {
	handler := newLogEntryHandler(t)
	ctx := context.Background()
	observedAt := time.Now().UTC()
	oid := orderid.OrderId{BotID: 12, DealID: 15, BotEventID: 41}
	botEvent := &tc.BotEvent{CreatedAt: observedAt.Add(-30 * time.Second)}

	config := &EffectiveOrderScaler{
		OrderId:    oid.Hex(),
		Multiplier: 3.5,
		Source:     Default,
		Default: OrderScalerState{
			Multiplier: 1.0,
			UpdatedAt:  observedAt,
			UpdatedBy:  "system",
		},
	}
	audit := &ScaledOrderAudit{
		BotId:               int64(oid.BotID),
		CreatedAt:           observedAt,
		DealId:              int64(oid.DealID),
		Multiplier:          3.5,
		MultiplierUpdatedBy: "tester",
		OrderSide:           "buy",
		OriginalSize:        10,
		ScaledSize:          20,
		StackIndex:          2,
	}
	actor := "tester"

	entry, ok := handler.makeOrderLogEntry(ctx, oid, observedAt, ScaledOrderAuditEntry, botEvent, nil, nil, config, audit, &actor, nil, nil, nil)
	require.True(t, ok)

	logEntry, err := entry.AsScaledOrderAuditLogEntry()
	require.NoError(t, err)
	require.Equal(t, actor, logEntry.Actor)
	require.NotNil(t, logEntry.Effective)
	require.InDelta(t, 2.0, logEntry.Effective.Multiplier, 1e-9) // clamped to max multiplier
	require.Equal(t, audit.Multiplier, logEntry.Audit.Multiplier)
	require.NotNil(t, logEntry.Identifiers)
	require.Equal(t, botEvent.CreatedAt, logEntry.Identifiers.CreatedAt)
}

func TestMakeOrderLogEntry_InvalidPayloads(t *testing.T) {
	handler := newLogEntryHandler(t)
	ctx := context.Background()
	observedAt := time.Now().UTC()
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}

	testcases := []struct {
		name       string
		entryType  OrderLogEntryType
		botEvent   *tc.BotEvent
		submission interface{}
		status     *hyperliquid.WsOrder
		config     *EffectiveOrderScaler
		audit      *ScaledOrderAudit
	}{
		{name: "missing bot event", entryType: ThreeCommasEvent},
		{name: "invalid submission payload", entryType: HyperliquidSubmission, submission: struct{}{}},
		{name: "missing status payload", entryType: HyperliquidStatus},
		{name: "missing scaler config", entryType: OrderScalerConfigEntry},
		{name: "missing scaled audit", entryType: ScaledOrderAuditEntry},
	}

	for _, tc := range testcases {
		tt := tc
		t.Run(tt.name, func(t *testing.T) {
			_, ok := handler.makeOrderLogEntry(ctx, oid, observedAt, tt.entryType, tt.botEvent, tt.submission, tt.status, tt.config, tt.audit, nil, nil, nil, nil)
			require.False(t, ok)
		})
	}
}
