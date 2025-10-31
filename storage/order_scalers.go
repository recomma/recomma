package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/recomma/recomma/metadata"
	"github.com/recomma/recomma/storage/sqlcgen"
)

type OrderScalerState struct {
	Multiplier float64
	UpdatedAt  time.Time
	UpdatedBy  string
	Notes      *string
}

type BotOrderScalerOverride struct {
	BotID         uint32
	Multiplier    *float64
	Notes         *string
	EffectiveFrom time.Time
	UpdatedAt     time.Time
	UpdatedBy     string
}

type ScaledOrderAudit struct {
	ID                  int64
	Metadata            metadata.Metadata
	DealID              uint32
	BotID               uint32
	OriginalSize        float64
	ScaledSize          float64
	Multiplier          float64
	RoundingDelta       float64
	StackIndex          int
	OrderSide           string
	MultiplierUpdatedBy string
	CreatedAt           time.Time
	SubmittedOrderID    *string
}

type ScaledOrderAuditParams struct {
	Metadata            metadata.Metadata
	DealID              uint32
	BotID               uint32
	OriginalSize        float64
	ScaledSize          float64
	Multiplier          float64
	RoundingDelta       float64
	StackIndex          int
	OrderSide           string
	MultiplierUpdatedBy string
	CreatedAt           time.Time
	SubmittedOrderID    *string
}

func (s *Storage) GetOrderScaler(ctx context.Context) (OrderScalerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := s.queries.GetOrderScaler(ctx)
	if err != nil {
		return OrderScalerState{}, err
	}

	return convertOrderScaler(row), nil
}

func (s *Storage) UpsertOrderScaler(ctx context.Context, multiplier float64, updatedBy string, notes *string) (OrderScalerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpsertOrderScalerParams{
		Multiplier: multiplier,
		UpdatedBy:  updatedBy,
		Notes:      notes,
	}

	if err := s.queries.UpsertOrderScaler(ctx, params); err != nil {
		return OrderScalerState{}, err
	}

	row, err := s.queries.GetOrderScaler(ctx)
	if err != nil {
		return OrderScalerState{}, err
	}

	return convertOrderScaler(row), nil
}

func (s *Storage) ListBotOrderScalers(ctx context.Context) ([]BotOrderScalerOverride, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListBotOrderScalers(ctx)
	if err != nil {
		return nil, err
	}

	overrides := make([]BotOrderScalerOverride, 0, len(rows))
	for _, row := range rows {
		overrides = append(overrides, convertBotOrderScaler(row))
	}

	return overrides, nil
}

func (s *Storage) GetBotOrderScaler(ctx context.Context, botID uint32) (*BotOrderScalerOverride, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := s.queries.GetBotOrderScaler(ctx, int64(botID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}

	override := convertBotOrderScaler(row)
	return &override, true, nil
}

func (s *Storage) UpsertBotOrderScaler(ctx context.Context, botID uint32, multiplier *float64, notes *string, updatedBy string) (BotOrderScalerOverride, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpsertBotOrderScalerParams{
		BotID:      int64(botID),
		Multiplier: multiplier,
		Notes:      notes,
		UpdatedBy:  updatedBy,
	}

	if err := s.queries.UpsertBotOrderScaler(ctx, params); err != nil {
		return BotOrderScalerOverride{}, err
	}

	row, err := s.queries.GetBotOrderScaler(ctx, int64(botID))
	if err != nil {
		return BotOrderScalerOverride{}, err
	}

	return convertBotOrderScaler(row), nil
}

func (s *Storage) DeleteBotOrderScaler(ctx context.Context, botID uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.queries.DeleteBotOrderScaler(ctx, int64(botID))
}

func (s *Storage) InsertScaledOrderAudit(ctx context.Context, params ScaledOrderAuditParams) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	insert := sqlcgen.InsertScaledOrderParams{
		Md:                  params.Metadata.Hex(),
		DealID:              int64(params.DealID),
		BotID:               int64(params.BotID),
		OriginalSize:        params.OriginalSize,
		ScaledSize:          params.ScaledSize,
		Multiplier:          params.Multiplier,
		RoundingDelta:       params.RoundingDelta,
		StackIndex:          int64(params.StackIndex),
		OrderSide:           params.OrderSide,
		MultiplierUpdatedBy: params.MultiplierUpdatedBy,
		CreatedAtUtc:        createdAt.UTC().UnixMilli(),
		SubmittedOrderID:    params.SubmittedOrderID,
	}

	return s.queries.InsertScaledOrder(ctx, insert)
}

func (s *Storage) ListScaledOrdersByMetadata(ctx context.Context, md metadata.Metadata) ([]ScaledOrderAudit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListScaledOrdersByMetadata(ctx, md.Hex())
	if err != nil {
		return nil, err
	}

	return convertScaledOrderRows(rows)
}

func (s *Storage) ListScaledOrdersByDeal(ctx context.Context, dealID uint32) ([]ScaledOrderAudit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListScaledOrdersByDeal(ctx, int64(dealID))
	if err != nil {
		return nil, err
	}

	return convertScaledOrderRows(rows)
}

func convertOrderScaler(row sqlcgen.OrderScaler) OrderScalerState {
	return OrderScalerState{
		Multiplier: row.Multiplier,
		UpdatedAt:  time.UnixMilli(row.UpdatedAtUtc).UTC(),
		UpdatedBy:  row.UpdatedBy,
		Notes:      row.Notes,
	}
}

func convertBotOrderScaler(row sqlcgen.BotOrderScaler) BotOrderScalerOverride {
	return BotOrderScalerOverride{
		BotID:         uint32(row.BotID),
		Multiplier:    row.Multiplier,
		Notes:         row.Notes,
		EffectiveFrom: time.UnixMilli(row.EffectiveFromUtc).UTC(),
		UpdatedAt:     time.UnixMilli(row.UpdatedAtUtc).UTC(),
		UpdatedBy:     row.UpdatedBy,
	}
}

func convertScaledOrderRows(rows []sqlcgen.ScaledOrder) ([]ScaledOrderAudit, error) {
	audits := make([]ScaledOrderAudit, 0, len(rows))
	for _, row := range rows {
		audit, err := convertScaledOrder(row)
		if err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, nil
}

func convertScaledOrder(row sqlcgen.ScaledOrder) (ScaledOrderAudit, error) {
	md, err := metadata.FromHexString(row.Md)
	if err != nil {
		return ScaledOrderAudit{}, fmt.Errorf("decode metadata %q: %w", row.Md, err)
	}

	return ScaledOrderAudit{
		ID:                  row.ID,
		Metadata:            *md,
		DealID:              uint32(row.DealID),
		BotID:               uint32(row.BotID),
		OriginalSize:        row.OriginalSize,
		ScaledSize:          row.ScaledSize,
		Multiplier:          row.Multiplier,
		RoundingDelta:       row.RoundingDelta,
		StackIndex:          int(row.StackIndex),
		OrderSide:           row.OrderSide,
		MultiplierUpdatedBy: row.MultiplierUpdatedBy,
		CreatedAt:           time.UnixMilli(row.CreatedAtUtc).UTC(),
		SubmittedOrderID:    row.SubmittedOrderID,
	}, nil
}
