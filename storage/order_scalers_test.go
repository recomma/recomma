package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	api "github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/metadata"
	"github.com/stretchr/testify/require"
)

type captureStream struct {
	mu     sync.Mutex
	events []api.StreamEvent
}

func (c *captureStream) Publish(evt api.StreamEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
}

func (c *captureStream) all() []api.StreamEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]api.StreamEvent, len(c.events))
	copy(out, c.events)
	return out
}

func newTestStorageWithStream(t *testing.T, stream api.StreamPublisher) *Storage {
	t.Helper()

	store, err := New(":memory:", WithStreamPublisher(stream))
	if err != nil {
		t.Fatalf("open sqlite storage: %v", err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite storage: %v", err)
		}
	})

	return store
}

func TestOrderScalerUpsertAndGet(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	initial, err := store.GetOrderScaler(ctx)
	require.NoError(t, err)
	require.Equal(t, 1.0, initial.Multiplier)
	require.Equal(t, "system", initial.UpdatedBy)
	require.Nil(t, initial.Notes)

	note := "manual baseline"
	updated, err := store.UpsertOrderScaler(ctx, 0.42, "tester", &note)
	require.NoError(t, err)
	require.Equal(t, 0.42, updated.Multiplier)
	require.Equal(t, "tester", updated.UpdatedBy)
	require.NotNil(t, updated.Notes)
	require.Equal(t, note, *updated.Notes)
	require.False(t, updated.UpdatedAt.Before(initial.UpdatedAt))

	again, err := store.GetOrderScaler(ctx)
	require.NoError(t, err)
	require.Equal(t, updated.Multiplier, again.Multiplier)
	require.Equal(t, updated.UpdatedBy, again.UpdatedBy)
	require.Equal(t, updated.Notes, again.Notes)
	require.False(t, again.UpdatedAt.Before(updated.UpdatedAt))
}

func TestResolveEffectiveOrderScaler(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	botID := uint32(99)
	now := time.Now().UTC()
	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, now)
	require.NoError(t, err)

	md := metadata.Metadata{BotID: botID}
	effective, err := store.ResolveEffectiveOrderScaler(ctx, md)
	require.NoError(t, err)
	require.Equal(t, OrderScalerSourceDefault, effective.Source)
	require.Nil(t, effective.Override)
	require.Equal(t, 1.0, effective.Multiplier)

	multiplier := 0.55
	note := "risk-adjusted"
	override, err := store.UpsertBotOrderScaler(ctx, botID, &multiplier, &note, "tester")
	require.NoError(t, err)

	effective, err = store.ResolveEffectiveOrderScaler(ctx, metadata.Metadata{BotID: botID, DealID: 1, BotEventID: 1})
	require.NoError(t, err)
	require.Equal(t, OrderScalerSourceBotOverride, effective.Source)
	require.NotNil(t, effective.Override)
	require.Equal(t, override.UpdatedAt, effective.Override.UpdatedAt)
	require.InDelta(t, multiplier, effective.Multiplier, 1e-9)
	require.Equal(t, "tester", effective.Actor())
}

func TestBotOrderScalerCRUD(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	botID := uint32(4242)
	now := time.Now().UTC()
	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, now)
	require.NoError(t, err)

	multiplier := 0.75
	note := "hedged exposure"
	override, err := store.UpsertBotOrderScaler(ctx, botID, &multiplier, &note, "operator")
	require.NoError(t, err)
	require.Equal(t, botID, override.BotID)
	require.NotNil(t, override.Multiplier)
	require.Equal(t, multiplier, *override.Multiplier)
	require.NotNil(t, override.Notes)
	require.Equal(t, note, *override.Notes)
	require.Equal(t, "operator", override.UpdatedBy)

	effectiveFrom := override.EffectiveFrom
	firstUpdatedAt := override.UpdatedAt

	overrides, err := store.ListBotOrderScalers(ctx)
	require.NoError(t, err)
	require.Len(t, overrides, 1)
	require.Equal(t, override, overrides[0])

	updated, err := store.UpsertBotOrderScaler(ctx, botID, nil, nil, "inherit")
	require.NoError(t, err)
	require.Nil(t, updated.Multiplier)
	require.Nil(t, updated.Notes)
	require.Equal(t, "inherit", updated.UpdatedBy)
	require.True(t, updated.EffectiveFrom.Equal(effectiveFrom))
	require.False(t, updated.UpdatedAt.Before(firstUpdatedAt))

	err = store.DeleteBotOrderScaler(ctx, botID, "cleanup")
	require.NoError(t, err)

	_, found, err := store.GetBotOrderScaler(ctx, botID)
	require.NoError(t, err)
	require.False(t, found)

	overrides, err = store.ListBotOrderScalers(ctx)
	require.NoError(t, err)
	require.Empty(t, overrides)
}

func TestOrderScalerConfigEvents(t *testing.T) {
	stream := &captureStream{}
	store := newTestStorageWithStream(t, stream)
	ctx := context.Background()

	botID := uint32(321)
	now := time.Now().UTC()
	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, now)
	require.NoError(t, err)

	_, err = store.UpsertOrderScaler(ctx, 0.82, "admin", nil)
	require.NoError(t, err)

	mult := 0.64
	_, err = store.UpsertBotOrderScaler(ctx, botID, &mult, nil, "operator")
	require.NoError(t, err)

	events := stream.all()
	require.GreaterOrEqual(t, len(events), 2)

	var defaultEvt, overrideEvt *api.StreamEvent
	for i := range events {
		evt := &events[i]
		if evt.Type != api.OrderScalerConfigEntry {
			continue
		}
		if evt.Metadata.BotID == 0 {
			defaultEvt = evt
		} else if evt.Metadata.BotID == botID {
			overrideEvt = evt
		}
	}

	require.NotNil(t, defaultEvt)
	require.NotNil(t, defaultEvt.Actor)
	require.Equal(t, "admin", *defaultEvt.Actor)
	require.NotNil(t, defaultEvt.ScalerConfig)
	require.InDelta(t, 0.82, defaultEvt.ScalerConfig.Multiplier, 1e-9)

	require.NotNil(t, overrideEvt)
	require.NotNil(t, overrideEvt.Actor)
	require.Equal(t, "operator", *overrideEvt.Actor)
	require.NotNil(t, overrideEvt.ScalerConfig)
	require.InDelta(t, mult, overrideEvt.ScalerConfig.Multiplier, 1e-9)
	require.NotNil(t, overrideEvt.ScalerConfig.Override)
}

func TestRecordScaledOrderPublishesEvent(t *testing.T) {
	stream := &captureStream{}
	store := newTestStorageWithStream(t, stream)
	ctx := context.Background()

	botID := uint32(111)
	dealID := uint32(222)
	base := time.Now().UTC()

	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, base)
	require.NoError(t, err)

	err = store.RecordThreeCommasDeal(ctx, tc.Deal{Id: int(dealID), BotId: int(botID), CreatedAt: base, UpdatedAt: base})
	require.NoError(t, err)

	mult := 0.5
	_, err = store.UpsertBotOrderScaler(ctx, botID, &mult, nil, "tester")
	require.NoError(t, err)

	md := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	params := RecordScaledOrderParams{
		Metadata:     md,
		DealID:       dealID,
		BotID:        botID,
		OriginalSize: 200.0,
		ScaledSize:   100.0,
		StackIndex:   0,
		OrderSide:    "sell",
		CreatedAt:    base.Add(2 * time.Second),
	}

	audit, effective, err := store.RecordScaledOrder(ctx, params)
	require.NoError(t, err)
	require.InDelta(t, mult, effective.Multiplier, 1e-9)
	require.InDelta(t, 0, audit.RoundingDelta, 1e-9)
	require.Equal(t, "tester", audit.MultiplierUpdatedBy)
	require.False(t, audit.Skipped)
	require.Nil(t, audit.SkipReason)

	events := stream.all()
	require.GreaterOrEqual(t, len(events), 2)

	var auditEvt *api.StreamEvent
	for i := range events {
		if events[i].Type == api.ScaledOrderAuditEntry {
			auditEvt = &events[i]
		}
	}
	require.NotNil(t, auditEvt)
	require.NotNil(t, auditEvt.Actor)
	require.Equal(t, "tester", *auditEvt.Actor)
	require.NotNil(t, auditEvt.ScaledOrderAudit)
	require.InDelta(t, 100.0, auditEvt.ScaledOrderAudit.ScaledSize, 1e-9)
	require.False(t, auditEvt.ScaledOrderAudit.Skipped)
	require.Nil(t, auditEvt.ScaledOrderAudit.SkipReason)
	require.NotNil(t, auditEvt.ScalerConfig)
	require.InDelta(t, mult, auditEvt.ScalerConfig.Multiplier, 1e-9)
	require.Equal(t, md.Hex(), auditEvt.ScalerConfig.Metadata)
}

func TestRecordScaledOrderUsesAppliedMultiplier(t *testing.T) {
	stream := &captureStream{}
	store := newTestStorageWithStream(t, stream)
	ctx := context.Background()

	botID := uint32(333)
	dealID := uint32(444)
	base := time.Now().UTC()

	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, base)
	require.NoError(t, err)

	err = store.RecordThreeCommasDeal(ctx, tc.Deal{Id: int(dealID), BotId: int(botID), CreatedAt: base, UpdatedAt: base})
	require.NoError(t, err)

	overrideMult := 3.0
	_, err = store.UpsertBotOrderScaler(ctx, botID, &overrideMult, nil, "tester")
	require.NoError(t, err)

	applied := 1.5
	md := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 7}
	params := RecordScaledOrderParams{
		Metadata:          md,
		DealID:            dealID,
		BotID:             botID,
		OriginalSize:      100.0,
		ScaledSize:        150.0,
		AppliedMultiplier: &applied,
		StackIndex:        0,
		OrderSide:         "buy",
		CreatedAt:         base.Add(3 * time.Second),
	}

	audit, effective, err := store.RecordScaledOrder(ctx, params)
	require.NoError(t, err)
	require.InDelta(t, applied, audit.Multiplier, 1e-9)
	require.InDelta(t, applied, effective.Multiplier, 1e-9)
	require.Equal(t, OrderScalerSourceBotOverride, effective.Source)
	require.InDelta(t, 0, audit.RoundingDelta, 1e-9)
	require.False(t, audit.Skipped)

	events := stream.all()
	var auditEvt *api.StreamEvent
	for i := range events {
		if events[i].Type == api.ScaledOrderAuditEntry {
			auditEvt = &events[i]
		}
	}
	require.NotNil(t, auditEvt)
	require.NotNil(t, auditEvt.ScaledOrderAudit)
	require.InDelta(t, applied, auditEvt.ScaledOrderAudit.Multiplier, 1e-9)
	require.False(t, auditEvt.ScaledOrderAudit.Skipped)
	require.NotNil(t, auditEvt.ScalerConfig)
	require.InDelta(t, applied, auditEvt.ScalerConfig.Multiplier, 1e-9)
}

func TestScaledOrderAuditHistory(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	botID := uint32(777)
	dealID := uint32(888)
	base := time.Now().UTC()

	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, base)
	require.NoError(t, err)

	err = store.RecordThreeCommasDeal(ctx, tc.Deal{
		Id:        int(dealID),
		BotId:     int(botID),
		CreatedAt: base,
		UpdatedAt: base,
	})
	require.NoError(t, err)

	md := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 101}
	later := base.Add(2 * time.Second)
	earlier := base.Add(1 * time.Second)
	submittedID := "hl-order-1"

	_, err = store.InsertScaledOrderAudit(ctx, ScaledOrderAuditParams{
		Metadata:            md,
		DealID:              dealID,
		BotID:               botID,
		OriginalSize:        250.0,
		ScaledSize:          125.0,
		Multiplier:          0.5,
		RoundingDelta:       0.0,
		StackIndex:          0,
		OrderSide:           "sell",
		MultiplierUpdatedBy: "operator",
		CreatedAt:           earlier,
	})
	require.NoError(t, err)

	_, err = store.InsertScaledOrderAudit(ctx, ScaledOrderAuditParams{
		Metadata:            md,
		DealID:              dealID,
		BotID:               botID,
		OriginalSize:        125.0,
		ScaledSize:          62.5,
		Multiplier:          0.5,
		RoundingDelta:       -0.1,
		StackIndex:          1,
		OrderSide:           "sell",
		MultiplierUpdatedBy: "operator",
		CreatedAt:           later,
	})
	require.NoError(t, err)

	otherMD := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 202}
	audit3, err := store.InsertScaledOrderAudit(ctx, ScaledOrderAuditParams{
		Metadata:            otherMD,
		DealID:              dealID,
		BotID:               botID,
		OriginalSize:        300.0,
		ScaledSize:          150.0,
		Multiplier:          0.5,
		RoundingDelta:       0.2,
		StackIndex:          2,
		OrderSide:           "buy",
		MultiplierUpdatedBy: "system",
		CreatedAt:           base.Add(3 * time.Second),
		SubmittedOrderID:    &submittedID,
	})
	require.NoError(t, err)

	byMetadata, err := store.ListScaledOrdersByMetadata(ctx, md)
	require.NoError(t, err)
	require.Len(t, byMetadata, 2)
	require.True(t, !byMetadata[0].CreatedAt.After(byMetadata[1].CreatedAt))
	require.Equal(t, md.Hex(), byMetadata[0].Metadata.Hex())
	require.Equal(t, 0, byMetadata[0].StackIndex)
	require.Equal(t, 1, byMetadata[1].StackIndex)
	require.False(t, byMetadata[0].Skipped)
	require.Nil(t, byMetadata[0].SkipReason)

	byDeal, err := store.ListScaledOrdersByDeal(ctx, dealID)
	require.NoError(t, err)
	require.Len(t, byDeal, 3)
	require.Equal(t, otherMD.Hex(), byDeal[2].Metadata.Hex())
	require.NotNil(t, byDeal[2].SubmittedOrderID)
	require.Equal(t, submittedID, *byDeal[2].SubmittedOrderID)
	require.Equal(t, "system", byDeal[2].MultiplierUpdatedBy)
	require.Equal(t, audit3.CreatedAt.UTC().UnixMilli(), byDeal[2].CreatedAt.UTC().UnixMilli())
	require.False(t, byDeal[2].Skipped)
}

func TestListOrderScalers(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	botID := uint32(555)
	dealID := uint32(666)
	base := time.Now().UTC()

	err := store.RecordBot(ctx, tc.Bot{Id: int(botID)}, base)
	require.NoError(t, err)

	err = store.RecordThreeCommasDeal(ctx, tc.Deal{Id: int(dealID), BotId: int(botID), CreatedAt: base, UpdatedAt: base})
	require.NoError(t, err)

	md := metadata.Metadata{BotID: botID, DealID: dealID, BotEventID: 1}
	_, err = store.RecordThreeCommasBotEvent(ctx, md, tc.BotEvent{CreatedAt: base})
	require.NoError(t, err)

	mult := 0.73
	_, err = store.UpsertBotOrderScaler(ctx, botID, &mult, nil, "auditor")
	require.NoError(t, err)

	rows, next, err := store.ListOrderScalers(ctx, api.ListOrderScalersOptions{Limit: 10})
	require.NoError(t, err)
	require.Nil(t, next)
	require.Len(t, rows, 1)

	record := rows[0]
	require.Equal(t, md.Hex(), record.Metadata.Hex())
	require.Equal(t, "auditor", record.Actor)
	require.InDelta(t, mult, record.Config.Multiplier, 1e-9)
	require.Equal(t, api.BotOverride, record.Config.Source)
}
