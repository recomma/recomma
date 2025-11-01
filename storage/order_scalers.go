package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
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
	OrderId             orderid.OrderId
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
	Skipped             bool
	SkipReason          *string
}

type ScaledOrderAuditParams struct {
	OrderId             orderid.OrderId
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
	Skipped             bool
	SkipReason          *string
}

type OrderScalerSource string

const (
	OrderScalerSourceDefault     OrderScalerSource = "default"
	OrderScalerSourceBotOverride OrderScalerSource = "bot_override"
)

type EffectiveOrderScaler struct {
	OrderId    orderid.OrderId
	Multiplier float64
	Source     OrderScalerSource
	Default    OrderScalerState
	Override   *BotOrderScalerOverride
}

func (e EffectiveOrderScaler) Actor() string {
	if e.Source == OrderScalerSourceBotOverride && e.Override != nil && e.Override.UpdatedBy != "" {
		return e.Override.UpdatedBy
	}
	return e.Default.UpdatedBy
}

func (e EffectiveOrderScaler) UpdatedAt() time.Time {
	if e.Source == OrderScalerSourceBotOverride && e.Override != nil {
		if !e.Override.UpdatedAt.IsZero() {
			return e.Override.UpdatedAt
		}
	}
	return e.Default.UpdatedAt
}

type RecordScaledOrderParams struct {
	OrderId           orderid.OrderId
	DealID            uint32
	BotID             uint32
	OriginalSize      float64
	ScaledSize        float64
	AppliedMultiplier *float64
	StackIndex        int
	OrderSide         string
	CreatedAt         time.Time
	SubmittedOrderID  *string
	Skipped           bool
	SkipReason        *string
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

func (s *Storage) ResolveEffectiveOrderScaler(ctx context.Context, oid orderid.OrderId) (EffectiveOrderScaler, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stateRow, err := s.queries.GetOrderScaler(ctx)
	if err != nil {
		return EffectiveOrderScaler{}, err
	}
	defaultState := convertOrderScaler(stateRow)

	effective := EffectiveOrderScaler{
		OrderId:    oid,
		Multiplier: defaultState.Multiplier,
		Source:     OrderScalerSourceDefault,
		Default:    defaultState,
	}

	overrideRow, err := s.queries.GetBotOrderScaler(ctx, int64(oid.BotID))
	if err != nil {
		if err == sql.ErrNoRows {
			return effective, nil
		}
		return EffectiveOrderScaler{}, err
	}

	override := convertBotOrderScaler(overrideRow)
	effective.Override = &override
	if override.Multiplier != nil {
		effective.Multiplier = *override.Multiplier
		effective.Source = OrderScalerSourceBotOverride
	}

	return effective, nil
}

func (s *Storage) ListTakeProfitStackSizes(ctx context.Context, oid orderid.OrderId, stackSize int) ([]float64, error) {
	if stackSize <= 0 {
		return nil, fmt.Errorf("stack size must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.ListLatestTakeProfitStackSizesParams{
		DealID:    int64(oid.DealID),
		OrderSize: int64(stackSize),
	}

	rows, err := s.queries.ListLatestTakeProfitStackSizes(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list take profit stack sizes: %w", err)
	}

	sizes := make([]float64, 0, stackSize)
	for _, row := range rows {
		sizes = append(sizes, row.Size)
	}
	if len(sizes) != stackSize {
		return nil, fmt.Errorf("incomplete take profit stack: expected %d legs, got %d", stackSize, len(sizes))
	}

	return sizes, nil
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

	state := convertOrderScaler(row)
	s.publishOrderScalerEventLocked(orderid.OrderId{}, EffectiveOrderScaler{
		OrderId:    orderid.OrderId{},
		Multiplier: state.Multiplier,
		Source:     OrderScalerSourceDefault,
		Default:    state,
	}, updatedBy, state.UpdatedAt)

	return state, nil
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

	override := convertBotOrderScaler(row)

	stateRow, err := s.queries.GetOrderScaler(ctx)
	if err != nil {
		return BotOrderScalerOverride{}, err
	}
	defaultState := convertOrderScaler(stateRow)

	effective := EffectiveOrderScaler{
		OrderId:    orderid.OrderId{BotID: botID},
		Multiplier: defaultState.Multiplier,
		Source:     OrderScalerSourceDefault,
		Default:    defaultState,
		Override:   &override,
	}
	if override.Multiplier != nil {
		effective.Multiplier = *override.Multiplier
		effective.Source = OrderScalerSourceBotOverride
	}

	s.publishOrderScalerEventLocked(effective.OrderId, effective, updatedBy, override.UpdatedAt)

	return override, nil
}

func (s *Storage) DeleteBotOrderScaler(ctx context.Context, botID uint32, updatedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.queries.DeleteBotOrderScaler(ctx, int64(botID)); err != nil {
		return err
	}

	stateRow, err := s.queries.GetOrderScaler(ctx)
	if err != nil {
		return err
	}
	defaultState := convertOrderScaler(stateRow)

	effective := EffectiveOrderScaler{
		OrderId:    orderid.OrderId{BotID: botID},
		Multiplier: defaultState.Multiplier,
		Source:     OrderScalerSourceDefault,
		Default:    defaultState,
	}

	s.publishOrderScalerEventLocked(effective.OrderId, effective, updatedBy, time.Now().UTC())

	return nil
}

func (s *Storage) RecordScaledOrder(ctx context.Context, params RecordScaledOrderParams) (ScaledOrderAudit, EffectiveOrderScaler, error) {
	effective, err := s.ResolveEffectiveOrderScaler(ctx, params.OrderId)
	if err != nil {
		return ScaledOrderAudit{}, EffectiveOrderScaler{}, err
	}

	multiplier := effective.Multiplier
	if params.AppliedMultiplier != nil {
		multiplier = *params.AppliedMultiplier
	}

	roundingDelta := params.ScaledSize - (params.OriginalSize * multiplier)

	audit, err := s.InsertScaledOrderAudit(ctx, ScaledOrderAuditParams{
		OrderId:             params.OrderId,
		DealID:              params.DealID,
		BotID:               params.BotID,
		OriginalSize:        params.OriginalSize,
		ScaledSize:          params.ScaledSize,
		Multiplier:          multiplier,
		RoundingDelta:       roundingDelta,
		StackIndex:          params.StackIndex,
		OrderSide:           params.OrderSide,
		MultiplierUpdatedBy: effective.Actor(),
		CreatedAt:           params.CreatedAt,
		SubmittedOrderID:    params.SubmittedOrderID,
		Skipped:             params.Skipped,
		SkipReason:          params.SkipReason,
	})
	if err != nil {
		return ScaledOrderAudit{}, EffectiveOrderScaler{}, err
	}

	effective.Multiplier = multiplier

	return audit, effective, nil
}

func (s *Storage) InsertScaledOrderAudit(ctx context.Context, params ScaledOrderAuditParams) (ScaledOrderAudit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	insert := sqlcgen.InsertScaledOrderParams{
		OrderID:             params.OrderId.Hex(),
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
		Skipped:             boolToInt(params.Skipped),
		SkipReason:          params.SkipReason,
	}

	id, err := s.queries.InsertScaledOrder(ctx, insert)
	if err != nil {
		return ScaledOrderAudit{}, err
	}

	audit := ScaledOrderAudit{
		ID:                  id,
		OrderId:             params.OrderId,
		DealID:              params.DealID,
		BotID:               params.BotID,
		OriginalSize:        params.OriginalSize,
		ScaledSize:          params.ScaledSize,
		Multiplier:          params.Multiplier,
		RoundingDelta:       params.RoundingDelta,
		StackIndex:          params.StackIndex,
		OrderSide:           params.OrderSide,
		MultiplierUpdatedBy: params.MultiplierUpdatedBy,
		CreatedAt:           createdAt,
		SubmittedOrderID:    params.SubmittedOrderID,
		Skipped:             params.Skipped,
		SkipReason:          params.SkipReason,
	}

	stateRow, err := s.queries.GetOrderScaler(ctx)
	if err != nil {
		return ScaledOrderAudit{}, err
	}
	defaultState := convertOrderScaler(stateRow)

	effective := EffectiveOrderScaler{
		OrderId:    params.OrderId,
		Multiplier: params.Multiplier,
		Source:     OrderScalerSourceDefault,
		Default:    defaultState,
	}

	overrideRow, err := s.queries.GetBotOrderScaler(ctx, int64(params.BotID))
	if err == nil {
		override := convertBotOrderScaler(overrideRow)
		effective.Override = &override
		if override.Multiplier != nil {
			effective.Source = OrderScalerSourceBotOverride
		}
	} else if err != sql.ErrNoRows {
		return ScaledOrderAudit{}, err
	}

	actor := effective.Actor()

	s.publishStreamEventLocked(api.StreamEvent{
		Type:             api.ScaledOrderAuditEntry,
		OrderId:          params.OrderId,
		ObservedAt:       createdAt,
		Actor:            &actor,
		ScaledOrderAudit: toAPIScaledOrderAudit(audit),
		ScalerConfig:     toAPIEffectiveOrderScaler(effective),
	})

	return audit, nil
}

func (s *Storage) ListScaledOrdersByOrderId(ctx context.Context, oid orderid.OrderId) ([]ScaledOrderAudit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListScaledOrdersByOrderId(ctx, oid.Hex())
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

func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
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
	oid, err := orderid.FromHexString(row.OrderID)
	if err != nil {
		return ScaledOrderAudit{}, fmt.Errorf("decode orderid %q: %w", row.OrderID, err)
	}

	return ScaledOrderAudit{
		ID:                  row.ID,
		OrderId:             *oid,
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
		Skipped:             row.Skipped != 0,
		SkipReason:          row.SkipReason,
	}, nil
}

func (s *Storage) publishOrderScalerEventLocked(oid orderid.OrderId, effective EffectiveOrderScaler, actor string, observedAt time.Time) {
	if s.stream == nil {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	cfg := toAPIEffectiveOrderScaler(effective)
	actorCopy := actor
	s.publishStreamEventLocked(api.StreamEvent{
		Type:         api.OrderScalerConfigEntry,
		OrderId:      oid,
		ObservedAt:   observedAt,
		Actor:        &actorCopy,
		ScalerConfig: cfg,
	})
}

func toAPIEffectiveOrderScaler(e EffectiveOrderScaler) *api.EffectiveOrderScaler {
	cfg := api.EffectiveOrderScaler{
		OrderId:    e.OrderId.Hex(),
		Multiplier: e.Multiplier,
		Source:     api.OrderScalerSource(e.Source),
		Default: api.OrderScalerState{
			Multiplier: e.Default.Multiplier,
			UpdatedAt:  e.Default.UpdatedAt,
			UpdatedBy:  e.Default.UpdatedBy,
			Notes:      e.Default.Notes,
		},
	}
	if e.Override != nil {
		override := api.OrderScalerOverride{
			BotId:         int64(e.Override.BotID),
			EffectiveFrom: e.Override.EffectiveFrom,
			UpdatedAt:     e.Override.UpdatedAt,
			UpdatedBy:     e.Override.UpdatedBy,
			Notes:         e.Override.Notes,
		}
		if e.Override.Multiplier != nil {
			value := *e.Override.Multiplier
			override.Multiplier = &value
		}
		cfg.Override = &override
	}
	return &cfg
}

func toAPIScaledOrderAudit(audit ScaledOrderAudit) *api.ScaledOrderAudit {
	out := api.ScaledOrderAudit{
		DealId:              int64(audit.DealID),
		BotId:               int64(audit.BotID),
		OriginalSize:        audit.OriginalSize,
		ScaledSize:          audit.ScaledSize,
		Multiplier:          audit.Multiplier,
		RoundingDelta:       audit.RoundingDelta,
		StackIndex:          audit.StackIndex,
		OrderSide:           audit.OrderSide,
		MultiplierUpdatedBy: audit.MultiplierUpdatedBy,
		CreatedAt:           audit.CreatedAt,
		Skipped:             audit.Skipped,
	}
	if audit.SubmittedOrderID != nil {
		out.SubmittedOrderId = audit.SubmittedOrderID
	}
	if audit.SkipReason != nil {
		out.SkipReason = audit.SkipReason
	}
	return &out
}
