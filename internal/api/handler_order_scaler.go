package api

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/recomma/recomma/orderid"
)

type ListOrderScalersOptions struct {
	OrderIdPrefix *string
	BotID         *int64
	DealID        *int64
	BotEventID    *int64
	Limit         int
	PageToken     string
}

// ListOrderScalers satisfies StrictServerInterface.
func (h *ApiHandler) ListOrderScalers(ctx context.Context, req ListOrderScalersRequestObject) (ListOrderScalersResponseObject, error) {
	limit := clampPageSize(req.Params.Limit)

	opts := ListOrderScalersOptions{
		OrderIdPrefix: req.Params.OrderId,
		BotID:         req.Params.BotId,
		DealID:        req.Params.DealId,
		BotEventID:    req.Params.BotEventId,
		Limit:         limit,
		PageToken:     deref(req.Params.PageToken),
	}

	rows, next, err := h.store.ListOrderScalers(ctx, opts)
	if err != nil {
		return nil, err
	}

	records := make([]OrderScalerConfigRecord, 0, len(rows))
	for _, item := range rows {
		cfg := item.Config
		cfg.Multiplier = clampOrderScalerMultiplier(cfg.Multiplier, h.orderScalerMaxMultiplier)
		records = append(records, OrderScalerConfigRecord{
			OrderId:    item.OrderId.Hex(),
			ObservedAt: item.ObservedAt,
			Actor:      item.Actor,
			Config:     cfg,
		})
	}

	resp := ListOrderScalers200JSONResponse{Items: records}
	if next != nil {
		resp.NextPageToken = next
	}
	return resp, nil
}

// GetOrderScalerConfig satisfies StrictServerInterface.
func (h *ApiHandler) GetOrderScalerConfig(ctx context.Context, req GetOrderScalerConfigRequestObject) (GetOrderScalerConfigResponseObject, error) {
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			return GetOrderScalerConfig401Response{}, nil
		}
		return GetOrderScalerConfig401Response{}, nil
	}

	defaultState, err := h.store.GetDefaultOrderScaler(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "get default order scaler", slog.String("error", err.Error()))
		return GetOrderScalerConfig500Response{}, nil
	}

	effective, err := h.store.ResolveEffectiveOrderScalerConfig(ctx, orderid.OrderId{})
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve default order scaler", slog.String("error", err.Error()))
		return GetOrderScalerConfig500Response{}, nil
	}

	response := OrderScalerConfigResponse{
		Default:   defaultState,
		Effective: buildOrderScalerEffectiveMultiplier(effective, h.orderScalerMaxMultiplier),
	}
	return GetOrderScalerConfig200JSONResponse(response), nil
}

// UpdateOrderScalerConfig satisfies StrictServerInterface.
func (h *ApiHandler) UpdateOrderScalerConfig(ctx context.Context, req UpdateOrderScalerConfigRequestObject) (UpdateOrderScalerConfigResponseObject, error) {
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			return UpdateOrderScalerConfig401Response{}, nil
		}
		return UpdateOrderScalerConfig401Response{}, nil
	}

	if req.Body == nil {
		return UpdateOrderScalerConfig400Response{}, nil
	}

	if err := h.validateOrderScalerMultiplier(req.Body.Multiplier); err != nil {
		h.logger.WarnContext(ctx, "invalid order scaler multiplier", slog.String("error", err.Error()))
		return UpdateOrderScalerConfig400Response{}, nil
	}

	actor := h.resolveActor()

	state, err := h.store.UpsertDefaultOrderScaler(ctx, req.Body.Multiplier, actor, req.Body.Notes)
	if err != nil {
		h.logger.ErrorContext(ctx, "upsert default order scaler", slog.String("error", err.Error()))
		return UpdateOrderScalerConfig500Response{}, nil
	}

	effective := EffectiveOrderScaler{
		Default:    state,
		OrderId:    "",
		Multiplier: state.Multiplier,
		Source:     Default,
	}

	response := OrderScalerConfigResponse{
		Default:   state,
		Effective: buildOrderScalerEffectiveMultiplier(effective, h.orderScalerMaxMultiplier),
	}
	return UpdateOrderScalerConfig200JSONResponse(response), nil
}

// GetBotOrderScalerConfig satisfies StrictServerInterface.
func (h *ApiHandler) GetBotOrderScalerConfig(ctx context.Context, req GetBotOrderScalerConfigRequestObject) (GetBotOrderScalerConfigResponseObject, error) {
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			return GetBotOrderScalerConfig401Response{}, nil
		}
		return GetBotOrderScalerConfig401Response{}, nil
	}

	botID, ok := normalizeBotID(req.BotId)
	if !ok {
		return GetBotOrderScalerConfig400Response{}, nil
	}

	defaultState, err := h.store.GetDefaultOrderScaler(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "get default order scaler", slog.String("error", err.Error()))
		return GetBotOrderScalerConfig500Response{}, nil
	}

	override, overrideFound, err := h.store.GetBotOrderScalerOverride(ctx, botID)
	if err != nil {
		h.logger.ErrorContext(ctx, "get bot order scaler override", slog.String("error", err.Error()), slog.Uint64("bot_id", uint64(botID)))
		return GetBotOrderScalerConfig500Response{}, nil
	}

	effective, err := h.store.ResolveEffectiveOrderScalerConfig(ctx, orderid.OrderId{BotID: botID})
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve bot order scaler", slog.String("error", err.Error()), slog.Uint64("bot_id", uint64(botID)))
		return GetBotOrderScalerConfig500Response{}, nil
	}

	response := BotOrderScalerConfigResponse{
		BotId:     int64(botID),
		Default:   defaultState,
		Effective: buildOrderScalerEffectiveMultiplier(effective, h.orderScalerMaxMultiplier),
	}
	if overrideFound && override != nil {
		response.Override = override
	}

	return GetBotOrderScalerConfig200JSONResponse(response), nil
}

// UpsertBotOrderScalerConfig satisfies StrictServerInterface.
func (h *ApiHandler) UpsertBotOrderScalerConfig(ctx context.Context, req UpsertBotOrderScalerConfigRequestObject) (UpsertBotOrderScalerConfigResponseObject, error) {
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			return UpsertBotOrderScalerConfig401Response{}, nil
		}
		return UpsertBotOrderScalerConfig401Response{}, nil
	}

	botID, ok := normalizeBotID(req.BotId)
	if !ok {
		return UpsertBotOrderScalerConfig400Response{}, nil
	}

	if req.Body == nil {
		return UpsertBotOrderScalerConfig400Response{}, nil
	}

	if err := h.validateOrderScalerMultiplier(req.Body.Multiplier); err != nil {
		h.logger.WarnContext(ctx, "invalid bot order scaler multiplier", slog.String("error", err.Error()), slog.Uint64("bot_id", uint64(botID)))
		return UpsertBotOrderScalerConfig400Response{}, nil
	}

	actor := h.resolveActor()

	override, err := h.store.UpsertBotOrderScalerOverride(ctx, botID, &req.Body.Multiplier, req.Body.Notes, actor)
	if err != nil {
		h.logger.ErrorContext(ctx, "upsert bot order scaler override", slog.String("error", err.Error()), slog.Uint64("bot_id", uint64(botID)))
		return UpsertBotOrderScalerConfig500Response{}, nil
	}

	defaultState, err := h.store.GetDefaultOrderScaler(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "get default order scaler", slog.String("error", err.Error()))
		return UpsertBotOrderScalerConfig500Response{}, nil
	}

	effective, err := h.store.ResolveEffectiveOrderScalerConfig(ctx, orderid.OrderId{BotID: botID})
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve bot order scaler", slog.String("error", err.Error()), slog.Uint64("bot_id", uint64(botID)))
		return UpsertBotOrderScalerConfig500Response{}, nil
	}

	response := BotOrderScalerConfigResponse{
		BotId:     int64(botID),
		Default:   defaultState,
		Override:  &override,
		Effective: buildOrderScalerEffectiveMultiplier(effective, h.orderScalerMaxMultiplier),
	}
	return UpsertBotOrderScalerConfig200JSONResponse(response), nil
}

// DeleteBotOrderScalerConfig satisfies StrictServerInterface.
func (h *ApiHandler) DeleteBotOrderScalerConfig(ctx context.Context, req DeleteBotOrderScalerConfigRequestObject) (DeleteBotOrderScalerConfigResponseObject, error) {
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			return DeleteBotOrderScalerConfig401Response{}, nil
		}
		return DeleteBotOrderScalerConfig401Response{}, nil
	}

	botID, ok := normalizeBotID(req.BotId)
	if !ok {
		return DeleteBotOrderScalerConfig400Response{}, nil
	}

	actor := h.resolveActor()

	if err := h.store.DeleteBotOrderScalerOverride(ctx, botID, actor); err != nil {
		h.logger.ErrorContext(ctx, "delete bot order scaler override", slog.String("error", err.Error()), slog.Uint64("bot_id", uint64(botID)))
		return DeleteBotOrderScalerConfig500Response{}, nil
	}

	defaultState, err := h.store.GetDefaultOrderScaler(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "get default order scaler", slog.String("error", err.Error()))
		return DeleteBotOrderScalerConfig500Response{}, nil
	}

	effective, err := h.store.ResolveEffectiveOrderScalerConfig(ctx, orderid.OrderId{BotID: botID})
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve bot order scaler", slog.String("error", err.Error()), slog.Uint64("bot_id", uint64(botID)))
		return DeleteBotOrderScalerConfig500Response{}, nil
	}

	response := BotOrderScalerConfigResponse{
		BotId:     int64(botID),
		Default:   defaultState,
		Effective: buildOrderScalerEffectiveMultiplier(effective, h.orderScalerMaxMultiplier),
	}
	return DeleteBotOrderScalerConfig200JSONResponse(response), nil
}

func makeBotRecords(rows []BotItem) []BotRecord {
	items := make([]BotRecord, 0, len(rows))
	for _, item := range rows {
		items = append(items, BotRecord{
			BotId:        int64(item.Bot.Id),
			LastSyncedAt: item.LastSyncedAt,
			Payload:      item.Bot,
		})
	}
	return items
}

func (h *ApiHandler) validateOrderScalerMultiplier(multiplier float64) error {
	if multiplier <= 0 {
		return fmt.Errorf("multiplier must be positive")
	}
	if h.orderScalerMaxMultiplier > 0 && multiplier > h.orderScalerMaxMultiplier {
		return fmt.Errorf("multiplier %.4f exceeds max %.4f", multiplier, h.orderScalerMaxMultiplier)
	}
	return nil
}

func buildOrderScalerEffectiveMultiplier(effective EffectiveOrderScaler, maxMultiplier float64) OrderScalerEffectiveMultiplier {
	notes := effective.Default.Notes
	updatedBy := effective.Default.UpdatedBy
	updatedAt := effective.Default.UpdatedAt

	if effective.Source == BotOverride && effective.Override != nil {
		updatedBy = effective.Override.UpdatedBy
		updatedAt = effective.Override.UpdatedAt
		if effective.Override.Notes != nil {
			notes = effective.Override.Notes
		} else {
			notes = nil
		}
	}

	return OrderScalerEffectiveMultiplier{
		Source:    effective.Source,
		Value:     clampOrderScalerMultiplier(effective.Multiplier, maxMultiplier),
		UpdatedAt: updatedAt,
		UpdatedBy: updatedBy,
		Notes:     notes,
	}
}

func clampOrderScalerMultiplier(multiplier, maxMultiplier float64) float64 {
	if maxMultiplier > 0 && multiplier > maxMultiplier {
		return maxMultiplier
	}
	return multiplier
}

func clampEffectiveOrderScaler(effective *EffectiveOrderScaler, maxMultiplier float64) *EffectiveOrderScaler {
	if effective == nil {
		return nil
	}

	clamped := *effective
	clamped.Multiplier = clampOrderScalerMultiplier(clamped.Multiplier, maxMultiplier)
	return &clamped
}
