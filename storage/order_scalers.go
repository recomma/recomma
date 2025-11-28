package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage/sqlcgen"
)

const (
	scaledOrderPayloadType = "scaled_order.audit.v1"
)

type scaledOrderPayload struct {
	SubmittedOrderID *string `json:"submitted_order_id,omitempty"`
}

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
	VenueID             string
	Wallet              string
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
	Identifier          recomma.OrderIdentifier
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
	Identifier        recomma.OrderIdentifier
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

func (s *Storage) ListOrderScalers(ctx context.Context, opts api.ListOrderScalersOptions) ([]api.OrderScalerConfigItem, *string, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	orderOpts := api.ListOrdersOptions{
		OrderIdPrefix: opts.OrderIdPrefix,
		BotID:         opts.BotID,
		DealID:        opts.DealID,
		BotEventID:    opts.BotEventID,
		Limit:         opts.Limit,
		PageToken:     opts.PageToken,
	}

	rows, next, err := s.ListOrders(ctx, orderOpts)
	if err != nil {
		return nil, nil, err
	}

	items := make([]api.OrderScalerConfigItem, 0, len(rows))
	for _, row := range rows {
		effective, err := s.ResolveEffectiveOrderScaler(ctx, row.OrderId)
		if err != nil {
			return nil, nil, err
		}
		cfgPtr := toAPIEffectiveOrderScaler(effective)
		if cfgPtr == nil {
			return nil, nil, fmt.Errorf("build effective scaler for %s", row.OrderId.Hex())
		}
		items = append(items, api.OrderScalerConfigItem{
			OrderId:    row.OrderId,
			ObservedAt: effective.UpdatedAt(),
			Actor:      effective.Actor(),
			Config:     *cfgPtr,
		})
	}

	return items, next, nil
}

func (s *Storage) GetDefaultOrderScaler(ctx context.Context) (api.OrderScalerState, error) {
	state, err := s.GetOrderScaler(ctx)
	if err != nil {
		return api.OrderScalerState{}, err
	}
	return toAPIOrderScalerState(state), nil
}

func (s *Storage) UpsertDefaultOrderScaler(ctx context.Context, multiplier float64, updatedBy string, notes *string) (api.OrderScalerState, error) {
	state, err := s.UpsertOrderScaler(ctx, multiplier, updatedBy, notes)
	if err != nil {
		return api.OrderScalerState{}, err
	}
	return toAPIOrderScalerState(state), nil
}

func (s *Storage) GetBotOrderScalerOverride(ctx context.Context, botID uint32) (*api.OrderScalerOverride, bool, error) {
	state, found, err := s.GetBotOrderScaler(ctx, botID)
	if err != nil {
		return nil, false, err
	}
	if !found || state == nil {
		return nil, false, nil
	}
	apiOverride := toAPIOrderScalerOverride(*state)
	return &apiOverride, true, nil
}

func (s *Storage) UpsertBotOrderScalerOverride(ctx context.Context, botID uint32, multiplier *float64, notes *string, updatedBy string) (api.OrderScalerOverride, error) {
	override, err := s.UpsertBotOrderScaler(ctx, botID, multiplier, notes, updatedBy)
	if err != nil {
		return api.OrderScalerOverride{}, err
	}
	return toAPIOrderScalerOverride(override), nil
}

func (s *Storage) DeleteBotOrderScalerOverride(ctx context.Context, botID uint32, updatedBy string) error {
	return s.DeleteBotOrderScaler(ctx, botID, updatedBy)
}

func (s *Storage) ResolveEffectiveOrderScalerConfig(ctx context.Context, oid orderid.OrderId) (api.EffectiveOrderScaler, error) {
	effective, err := s.ResolveEffectiveOrderScaler(ctx, oid)
	if err != nil {
		return api.EffectiveOrderScaler{}, err
	}
	apiEffective := toAPIEffectiveOrderScaler(effective)
	if apiEffective == nil {
		return api.EffectiveOrderScaler{}, fmt.Errorf("convert effective order scaler")
	}
	return *apiEffective, nil
}

func toAPIOrderScalerState(state OrderScalerState) api.OrderScalerState {
	return api.OrderScalerState{
		Multiplier: state.Multiplier,
		Notes:      state.Notes,
		UpdatedAt:  state.UpdatedAt,
		UpdatedBy:  state.UpdatedBy,
	}
}

func toAPIOrderScalerOverride(override BotOrderScalerOverride) api.OrderScalerOverride {
	return api.OrderScalerOverride{
		BotId:         int64(override.BotID),
		Multiplier:    override.Multiplier,
		Notes:         override.Notes,
		EffectiveFrom: override.EffectiveFrom,
		UpdatedAt:     override.UpdatedAt,
		UpdatedBy:     override.UpdatedBy,
	}
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

	byPosition := make(map[int]float64, stackSize)
	for _, row := range rows {
		pos := int(row.OrderPosition) - 1
		if pos < 0 || pos >= stackSize {
			continue
		}
		if row.Size <= 0 {
			continue
		}
		byPosition[pos] = row.Size
	}

	if len(byPosition) < stackSize {
		s.mergeStackFromDealLocked(ctx, oid, stackSize, byPosition)
	}

	if len(byPosition) == 0 {
		return nil, nil
	}

	out := make([]float64, 0, stackSize)
	for pos := 0; pos < stackSize; pos++ {
		size, ok := byPosition[pos]
		if !ok || size <= 0 {
			break
		}
		out = append(out, size)
	}

	if len(out) < stackSize {
		s.logger.Debug("take profit stack incomplete",
			slog.Uint64("deal_id", uint64(oid.DealID)),
			slog.Int("expected", stackSize),
			slog.Int("resolved", len(out)))
	}

	return out, nil
}

func (s *Storage) mergeStackFromDealLocked(ctx context.Context, oid orderid.OrderId, stackSize int, sizes map[int]float64) {
	payload, err := s.queries.FetchDeal(ctx, int64(oid.DealID))
	if err != nil {
		if err != sql.ErrNoRows {
			s.logger.Debug("fetch deal payload failed",
				slog.Uint64("deal_id", uint64(oid.DealID)),
				slog.String("error", err.Error()))
		}
		return
	}

	var deal tc.Deal
	if err := json.Unmarshal(payload, &deal); err != nil {
		s.logger.Debug("decode deal payload failed",
			slog.Uint64("deal_id", uint64(oid.DealID)),
			slog.String("error", err.Error()))
		return
	}

	stack := takeProfitStackFromDeal(deal, stackSize)
	if len(stack) == 0 {
		return
	}

	for pos, size := range stack {
		if pos < 0 || pos >= stackSize || size <= 0 {
			continue
		}
		if _, exists := sizes[pos]; !exists {
			sizes[pos] = size
		}
	}
}

func takeProfitStackFromDeal(deal tc.Deal, stackSizeHint int) map[int]float64 {
	if len(deal.TakeProfitSteps) == 0 {
		return nil
	}

	baseVolume := parseNumericString(deal.BaseOrderVolume)
	if baseVolume <= 0 {
		baseVolume = parseNumericString(deal.BoughtAmount)
	}

	result := make(map[int]float64, len(deal.TakeProfitSteps))
	for idx, step := range deal.TakeProfitSteps {
		pos := idx
		if step.Id != nil {
			switch {
			case *step.Id > 0:
				pos = *step.Id - 1
			case *step.Id == 0:
				pos = 0
			}
		}

		size := parseNumericString(nullableString(step.InitialAmount))
		if size <= 0 && step.AmountPercentage != nil && baseVolume > 0 {
			size = baseVolume * float64(*step.AmountPercentage) / 100.0
		}
		if size <= 0 {
			continue
		}
		result[pos] = size
	}

	if len(result) == 0 {
		return nil
	}

	if stackSizeHint > 0 && len(result) != stackSizeHint {
		normalized := make(map[int]float64, stackSizeHint)
		for pos, size := range result {
			if pos < 0 || pos >= stackSizeHint {
				continue
			}
			normalized[pos] = size
		}
		if len(normalized) > 0 {
			return normalized
		}
	}

	return result
}

func nullableString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func parseNumericString(val string) float64 {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0
	}
	return f
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
	effective, err := s.ResolveEffectiveOrderScaler(ctx, params.Identifier.OrderId)
	if err != nil {
		return ScaledOrderAudit{}, EffectiveOrderScaler{}, err
	}

	multiplier := effective.Multiplier
	if params.AppliedMultiplier != nil {
		multiplier = *params.AppliedMultiplier
	}

	roundingDelta := params.ScaledSize - (params.OriginalSize * multiplier)

	audit, err := s.InsertScaledOrderAudit(ctx, ScaledOrderAuditParams{
		Identifier:          params.Identifier,
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

	var (
		payloadType *string
		payloadBlob []byte
	)

	if params.SubmittedOrderID != nil {
		encoded, err := json.Marshal(scaledOrderPayload{SubmittedOrderID: params.SubmittedOrderID})
		if err != nil {
			return ScaledOrderAudit{}, fmt.Errorf("encode scaled order payload: %w", err)
		}
		payloadBlob = encoded
		pt := scaledOrderPayloadType
		payloadType = &pt
	}

	orderID := fmt.Sprintf("%s#%d#%d", params.Identifier.OrderId.Hex(), params.StackIndex, createdAt.UTC().UnixNano())

	// Use venue and wallet from the identifier instead of defaultVenueAssignmentLocked
	venueID := params.Identifier.VenueID
	wallet := params.Identifier.Wallet

	// Fallback to default if identifier is empty (for backward compatibility during transition)
	if venueID == "" || wallet == "" {
		defaultAssignment, err := s.defaultVenueAssignmentLocked(ctx)
		if err != nil {
			return ScaledOrderAudit{}, fmt.Errorf("load default venue: %w", err)
		}
		if venueID == "" {
			venueID = defaultAssignment.VenueID
		}
		if wallet == "" {
			wallet = defaultAssignment.Wallet
		}
	}

	insert := sqlcgen.InsertScaledOrderParams{
		VenueID:             string(venueID),
		Wallet:              wallet,
		OrderID:             orderID,
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
		Skipped:             boolToInt(params.Skipped),
		SkipReason:          params.SkipReason,
		PayloadType:         payloadType,
		PayloadBlob:         payloadBlob,
	}

	if err := s.queries.InsertScaledOrder(ctx, insert); err != nil {
		return ScaledOrderAudit{}, err
	}

	audit := ScaledOrderAudit{
		OrderId:             params.Identifier.OrderId,
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
		OrderId:    params.Identifier.OrderId,
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

	ident := ensureIdentifier(params.Identifier)
	identCopy := ident
	s.publishStreamEventLocked(api.StreamEvent{
		Type:             api.ScaledOrderAuditEntry,
		OrderID:          params.Identifier.OrderId,
		Identifier:       &identCopy,
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

	rows, err := s.queries.ListScaledOrdersByOrderId(ctx, sqlcgen.ListScaledOrdersByOrderIdParams{
		OrderID:       oid.Hex(),
		OrderIDPrefix: fmt.Sprintf("%s#%%", oid.Hex()),
	})
	if err != nil {
		return nil, err
	}

	return convertScaledOrdersFromOrderRows(rows)
}

func (s *Storage) ListScaledOrdersByDeal(ctx context.Context, dealID uint32) ([]ScaledOrderAudit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListScaledOrdersByDeal(ctx, int64(dealID))
	if err != nil {
		return nil, err
	}

	return convertScaledOrdersFromDealRows(rows)
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

func convertScaledOrdersFromOrderRows(rows []sqlcgen.ScaledOrder) ([]ScaledOrderAudit, error) {
	audits := make([]ScaledOrderAudit, 0, len(rows))
	for _, row := range rows {
		audit, err := convertScaledOrder(sqlcgen.ScaledOrder{
			VenueID:             row.VenueID,
			Wallet:              row.Wallet,
			OrderID:             row.OrderID,
			DealID:              row.DealID,
			BotID:               row.BotID,
			OriginalSize:        row.OriginalSize,
			ScaledSize:          row.ScaledSize,
			Multiplier:          row.Multiplier,
			RoundingDelta:       row.RoundingDelta,
			StackIndex:          row.StackIndex,
			OrderSide:           row.OrderSide,
			MultiplierUpdatedBy: row.MultiplierUpdatedBy,
			CreatedAtUtc:        row.CreatedAtUtc,
			Skipped:             row.Skipped,
			SkipReason:          row.SkipReason,
			PayloadType:         row.PayloadType,
			PayloadBlob:         row.PayloadBlob,
		})
		if err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, nil
}

func convertScaledOrdersFromDealRows(rows []sqlcgen.ScaledOrder) ([]ScaledOrderAudit, error) {
	audits := make([]ScaledOrderAudit, 0, len(rows))
	for _, row := range rows {
		audit, err := convertScaledOrder(sqlcgen.ScaledOrder{
			VenueID:             row.VenueID,
			Wallet:              row.Wallet,
			OrderID:             row.OrderID,
			DealID:              row.DealID,
			BotID:               row.BotID,
			OriginalSize:        row.OriginalSize,
			ScaledSize:          row.ScaledSize,
			Multiplier:          row.Multiplier,
			RoundingDelta:       row.RoundingDelta,
			StackIndex:          row.StackIndex,
			OrderSide:           row.OrderSide,
			MultiplierUpdatedBy: row.MultiplierUpdatedBy,
			CreatedAtUtc:        row.CreatedAtUtc,
			Skipped:             row.Skipped,
			SkipReason:          row.SkipReason,
			PayloadType:         row.PayloadType,
			PayloadBlob:         row.PayloadBlob,
		})
		if err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, nil
}

func convertScaledOrderFromAuditRow(row sqlcgen.ScaledOrder) (ScaledOrderAudit, error) {
	return convertScaledOrder(sqlcgen.ScaledOrder{
		VenueID:             row.VenueID,
		Wallet:              row.Wallet,
		OrderID:             row.OrderID,
		DealID:              row.DealID,
		BotID:               row.BotID,
		OriginalSize:        row.OriginalSize,
		ScaledSize:          row.ScaledSize,
		Multiplier:          row.Multiplier,
		RoundingDelta:       row.RoundingDelta,
		StackIndex:          row.StackIndex,
		OrderSide:           row.OrderSide,
		MultiplierUpdatedBy: row.MultiplierUpdatedBy,
		CreatedAtUtc:        row.CreatedAtUtc,
		Skipped:             row.Skipped,
		SkipReason:          row.SkipReason,
		PayloadType:         row.PayloadType,
		PayloadBlob:         row.PayloadBlob,
	})
}

func convertScaledOrder(row sqlcgen.ScaledOrder) (ScaledOrderAudit, error) {
	originalOrderID := row.OrderID
	trimmedOrderID := originalOrderID
	if idx := strings.Index(trimmedOrderID, "#"); idx >= 0 {
		trimmedOrderID = trimmedOrderID[:idx]
	}
	oid, err := orderid.FromHexString(trimmedOrderID)
	if err != nil {
		return ScaledOrderAudit{}, fmt.Errorf("decode orderid %q: %w", originalOrderID, err)
	}

	var submittedID *string
	if row.PayloadType != nil && *row.PayloadType == scaledOrderPayloadType && len(row.PayloadBlob) > 0 {
		var payload scaledOrderPayload
		if err := json.Unmarshal(row.PayloadBlob, &payload); err != nil {
			return ScaledOrderAudit{}, fmt.Errorf("decode scaled order payload for %q: %w", row.OrderID, err)
		}
		submittedID = payload.SubmittedOrderID
	}

	return ScaledOrderAudit{
		VenueID:             row.VenueID,
		Wallet:              row.Wallet,
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
		SubmittedOrderID:    submittedID,
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
	ident := ensureIdentifier(recomma.NewOrderIdentifier("", "", oid))
	identCopy := ident
	s.publishStreamEventLocked(api.StreamEvent{
		Type:         api.OrderScalerConfigEntry,
		OrderID:      oid,
		Identifier:   &identCopy,
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
