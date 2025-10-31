package storage

import (
	"context"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/metadata"
	"github.com/stretchr/testify/require"
)

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

	err = store.DeleteBotOrderScaler(ctx, botID)
	require.NoError(t, err)

	_, found, err := store.GetBotOrderScaler(ctx, botID)
	require.NoError(t, err)
	require.False(t, found)

	overrides, err = store.ListBotOrderScalers(ctx)
	require.NoError(t, err)
	require.Empty(t, overrides)
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
	_, err = store.InsertScaledOrderAudit(ctx, ScaledOrderAuditParams{
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

	byDeal, err := store.ListScaledOrdersByDeal(ctx, dealID)
	require.NoError(t, err)
	require.Len(t, byDeal, 3)
	require.Equal(t, otherMD.Hex(), byDeal[2].Metadata.Hex())
	require.NotNil(t, byDeal[2].SubmittedOrderID)
	require.Equal(t, submittedID, *byDeal[2].SubmittedOrderID)
	require.Equal(t, "system", byDeal[2].MultiplierUpdatedBy)
}
