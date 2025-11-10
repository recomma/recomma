package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/internal/debugmode"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	hyperliquid "github.com/sonirico/go-hyperliquid"
)

const (
	defaultPageSize = 100
	maxPageSize     = 500
)

// Store abstracts the bits of storage.Storage you’ll surface via the API.
// Provide a concrete implementation that issues the required SQL.
type Store interface {
	ListBots(ctx context.Context, opts ListBotsOptions) ([]BotItem, *string, error)
	ListDeals(ctx context.Context, opts ListDealsOptions) ([]tc.Deal, *string, error)
	ListOrders(ctx context.Context, opts ListOrdersOptions) ([]OrderItem, *string, error)
	ListOrderScalers(ctx context.Context, opts ListOrderScalersOptions) ([]OrderScalerConfigItem, *string, error)
	GetDefaultOrderScaler(ctx context.Context) (OrderScalerState, error)
	UpsertDefaultOrderScaler(ctx context.Context, multiplier float64, updatedBy string, notes *string) (OrderScalerState, error)
	GetBotOrderScalerOverride(ctx context.Context, botID uint32) (*OrderScalerOverride, bool, error)
	UpsertBotOrderScalerOverride(ctx context.Context, botID uint32, multiplier *float64, notes *string, updatedBy string) (OrderScalerOverride, error)
	DeleteBotOrderScalerOverride(ctx context.Context, botID uint32, updatedBy string) error
	ResolveEffectiveOrderScalerConfig(ctx context.Context, oid orderid.OrderId) (EffectiveOrderScaler, error)
	LoadHyperliquidSubmission(ctx context.Context, ident recomma.OrderIdentifier) (recomma.Action, bool, error)
	LoadHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error)
	ListSubmissionIdentifiersForOrder(ctx context.Context, oid orderid.OrderId) ([]recomma.OrderIdentifier, error)
	ListVenues(ctx context.Context) ([]VenueRecord, error)
	UpsertVenue(ctx context.Context, venueID string, payload VenueUpsertRequest) (VenueRecord, error)
	DeleteVenue(ctx context.Context, venueID string) error
	ListVenueAssignments(ctx context.Context, venueID string) ([]VenueAssignmentRecord, error)
	UpsertVenueAssignment(ctx context.Context, venueID string, botID int64, isPrimary bool) (VenueAssignmentRecord, error)
	DeleteVenueAssignment(ctx context.Context, venueID string, botID int64) error
	ListBotVenues(ctx context.Context, botID int64) ([]BotVenueAssignmentRecord, error)
}

// StreamSource publishes live order mutations for the SSE endpoint.
type StreamSource interface {
	Subscribe(ctx context.Context, filter StreamFilter) (<-chan StreamEvent, error)
}

// ApiHandler implements api.StrictServerInterface.
type ApiHandler struct {
	store        Store
	stream       StreamSource
	systemStream *SystemStreamController
	systemStatus *SystemStatusTracker
	logger       *slog.Logger
	now          func() time.Time
	webauthn     *WebAuthnService
	vault        *vault.Controller
	session      *vaultSessionManager
	debug        bool

	orderScalerMaxMultiplier float64
	orders                   recomma.Emitter
	prices                   HyperliquidPriceSource
}

// NewHandler wires everything together.
func NewHandler(store Store, stream StreamSource, opts ...HandlerOption) *ApiHandler {
	h := &ApiHandler{
		store:   store,
		stream:  stream,
		now:     time.Now,
		session: newVaultSessionManager(),
		prices:  newPriceSourceProxy(),
	}
	for _, opt := range opts {
		opt(h)
	}

	if h.logger == nil {
		h.logger = slog.Default()
	}

	return h
}

// HandlerOption configures ApiHandler optional dependencies.
type HandlerOption func(*ApiHandler)

// HyperliquidPriceSource provides subscription access to Hyperliquid BBO updates.
type HyperliquidPriceSource interface {
	SubscribeBBO(ctx context.Context, coin string) (<-chan hl.BestBidOffer, error)
}

var ErrPriceSourceNotReady = errors.New("hyperliquid price source not ready")

var (
	ErrVenueNotFound           = errors.New("api: venue not found")
	ErrVenueImmutable          = errors.New("api: venue immutable")
	ErrVenueInvalid            = errors.New("api: invalid venue payload")
	ErrVenueAssignmentNotFound = errors.New("api: venue assignment not found")
)

type priceSourceProxy struct {
	mu    sync.RWMutex
	src   HyperliquidPriceSource
	ready chan struct{}
}

func newPriceSourceProxy() *priceSourceProxy {
	return &priceSourceProxy{ready: make(chan struct{})}
}

func (p *priceSourceProxy) Set(source HyperliquidPriceSource) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.src = source
	if source != nil {
		select {
		case <-p.ready:
		default:
			close(p.ready)
		}
	}
}

func (p *priceSourceProxy) SubscribeBBO(ctx context.Context, coin string) (<-chan hl.BestBidOffer, error) {
	p.mu.RLock()
	source := p.src
	ready := p.ready
	p.mu.RUnlock()
	if source == nil {
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		p.mu.RLock()
		source = p.src
		p.mu.RUnlock()
		if source == nil {
			return nil, ErrPriceSourceNotReady
		}
	}
	return source.SubscribeBBO(ctx, coin)
}

// WithWebAuthnService injects the WebAuthn service used for registration/login flows.
func WithWebAuthnService(service *WebAuthnService) HandlerOption {
	return func(h *ApiHandler) {
		h.webauthn = service
	}
}

// WithVaultController injects the vault controller dependency.
func WithVaultController(controller *vault.Controller) HandlerOption {
	return func(h *ApiHandler) {
		h.vault = controller
	}
}

func WithLogger(logger *slog.Logger) HandlerOption {
	return func(h *ApiHandler) {
		h.logger = logger
	}
}

func WithSystemStream(stream *SystemStreamController) HandlerOption {
	return func(h *ApiHandler) {
		h.systemStream = stream
	}
}

func WithSystemStatus(status *SystemStatusTracker) HandlerOption {
	return func(h *ApiHandler) {
		h.systemStatus = status
	}
}

// WithOrderScalerMaxMultiplier sets the maximum allowable multiplier enforced by the API.
func WithOrderScalerMaxMultiplier(max float64) HandlerOption {
	return func(h *ApiHandler) {
		if max > 0 {
			h.orderScalerMaxMultiplier = max
		}
	}
}

// WithDebugMode toggles debug behaviour for the handler.
func WithDebugMode(enabled bool) HandlerOption {
	return func(h *ApiHandler) {
		h.debug = enabled && debugmode.Available()
	}
}

// WithOrderEmitter wires the queue-backed emitter used for manual order actions.
func WithOrderEmitter(emitter recomma.Emitter) HandlerOption {
	return func(h *ApiHandler) {
		h.orders = emitter
	}
}

// WithHyperliquidPriceSource attaches the Hyperliquid price stream source used for SSE.
func WithHyperliquidPriceSource(source HyperliquidPriceSource) HandlerOption {
	return func(h *ApiHandler) {
		switch proxy := h.prices.(type) {
		case *priceSourceProxy:
			proxy.Set(source)
			h.prices = proxy
		default:
			h.prices = source
		}
	}
}

// ListBots satisfies StrictServerInterface.
func (h *ApiHandler) ListBots(ctx context.Context, req ListBotsRequestObject) (ListBotsResponseObject, error) {
	limit := clampPageSize(req.Params.Limit)
	opts := ListBotsOptions{
		BotID:       req.Params.BotId,
		UpdatedFrom: req.Params.UpdatedFrom,
		UpdatedTo:   req.Params.UpdatedTo,
		Limit:       limit,
		PageToken:   deref(req.Params.PageToken),
	}

	rows, next, err := h.store.ListBots(ctx, opts)
	if err != nil {
		return nil, err
	}

	resp := ListBots200JSONResponse{Items: makeBotRecords(rows)}
	if next != nil {
		resp.NextPageToken = next
	}
	return resp, nil
}

// ListDeals satisfies StrictServerInterface.
func (h *ApiHandler) ListDeals(ctx context.Context, req ListDealsRequestObject) (ListDealsResponseObject, error) {
	limit := clampPageSize(req.Params.Limit)
	opts := ListDealsOptions{
		DealID:      req.Params.DealId,
		BotID:       req.Params.BotId,
		UpdatedFrom: req.Params.UpdatedFrom,
		UpdatedTo:   req.Params.UpdatedTo,
		Limit:       limit,
		PageToken:   deref(req.Params.PageToken),
	}

	rows, next, err := h.store.ListDeals(ctx, opts)
	if err != nil {
		return nil, err
	}

	resp := ListDeals200JSONResponse{Items: makeDealRecords(rows)}
	if next != nil {
		resp.NextPageToken = next
	}
	return resp, nil
}

// ListOrders satisfies StrictServerInterface.
func (h *ApiHandler) ListOrders(ctx context.Context, req ListOrdersRequestObject) (ListOrdersResponseObject, error) {
	includeLog := req.Params.IncludeLog != nil && *req.Params.IncludeLog
	limit := clampPageSize(req.Params.Limit)

	opts := ListOrdersOptions{
		OrderIdPrefix: req.Params.OrderId,
		BotID:         req.Params.BotId,
		DealID:        req.Params.DealId,
		BotEventID:    req.Params.BotEventId,
		ObservedFrom:  req.Params.ObservedFrom,
		ObservedTo:    req.Params.ObservedTo,
		IncludeLog:    includeLog,
		Limit:         limit,
		PageToken:     deref(req.Params.PageToken),
	}

	rows, next, err := h.store.ListOrders(ctx, opts)
	if err != nil {
		return nil, err
	}

	resp := ListOrders200JSONResponse{Items: h.buildOrderRecords(ctx, rows, includeLog)}
	if next != nil {
		resp.NextPageToken = next
	}
	return resp, nil
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

// ListVenues satisfies StrictServerInterface.
func (h *ApiHandler) ListVenues(ctx context.Context, req ListVenuesRequestObject) (ListVenuesResponseObject, error) {
	venues, err := h.store.ListVenues(ctx)
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "ListVenues failed", slog.String("error", err.Error()))
		}
		return ListVenues500Response{}, nil
	}
	return ListVenues200JSONResponse{Items: venues}, nil
}

// UpsertVenue satisfies StrictServerInterface.
func (h *ApiHandler) UpsertVenue(ctx context.Context, req UpsertVenueRequestObject) (UpsertVenueResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" || req.Body == nil {
		return UpsertVenue400Response{}, nil
	}

	record, err := h.store.UpsertVenue(ctx, venueID, VenueUpsertRequest(*req.Body))
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueInvalid):
			return UpsertVenue400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "UpsertVenue failed", slog.String("venue", venueID), slog.String("error", err.Error()))
			}
			return UpsertVenue500Response{}, nil
		}
	}

	return UpsertVenue200JSONResponse(record), nil
}

// DeleteVenue satisfies StrictServerInterface.
func (h *ApiHandler) DeleteVenue(ctx context.Context, req DeleteVenueRequestObject) (DeleteVenueResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" {
		return DeleteVenue400Response{}, nil
	}

	err := h.store.DeleteVenue(ctx, venueID)
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueNotFound):
			return DeleteVenue404Response{}, nil
		case errors.Is(err, ErrVenueImmutable):
			return DeleteVenue409Response{}, nil
		case errors.Is(err, ErrVenueInvalid):
			return DeleteVenue400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "DeleteVenue failed", slog.String("venue", venueID), slog.String("error", err.Error()))
			}
			return DeleteVenue500Response{}, nil
		}
	}

	return DeleteVenue204Response{}, nil
}

// ListVenueAssignments satisfies StrictServerInterface.
func (h *ApiHandler) ListVenueAssignments(ctx context.Context, req ListVenueAssignmentsRequestObject) (ListVenueAssignmentsResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" {
		return ListVenueAssignments400Response{}, nil
	}

	assignments, err := h.store.ListVenueAssignments(ctx, venueID)
	if err != nil {
		if errors.Is(err, ErrVenueNotFound) {
			return ListVenueAssignments404Response{}, nil
		}
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "ListVenueAssignments failed", slog.String("venue", venueID), slog.String("error", err.Error()))
		}
		return ListVenueAssignments500Response{}, nil
	}

	return ListVenueAssignments200JSONResponse{Items: assignments}, nil
}

// UpsertVenueAssignment satisfies StrictServerInterface.
func (h *ApiHandler) UpsertVenueAssignment(ctx context.Context, req UpsertVenueAssignmentRequestObject) (UpsertVenueAssignmentResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" || req.Body == nil {
		return UpsertVenueAssignment400Response{}, nil
	}

	botID := req.BotId
	if botID <= 0 {
		return UpsertVenueAssignment400Response{}, nil
	}

	record, err := h.store.UpsertVenueAssignment(ctx, venueID, botID, req.Body.IsPrimary)
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueNotFound):
			return UpsertVenueAssignment404Response{}, nil
		case errors.Is(err, ErrVenueInvalid):
			return UpsertVenueAssignment400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "UpsertVenueAssignment failed", slog.String("venue", venueID), slog.Int64("bot_id", botID), slog.String("error", err.Error()))
			}
			return UpsertVenueAssignment500Response{}, nil
		}
	}

	return UpsertVenueAssignment200JSONResponse(record), nil
}

// DeleteVenueAssignment satisfies StrictServerInterface.
func (h *ApiHandler) DeleteVenueAssignment(ctx context.Context, req DeleteVenueAssignmentRequestObject) (DeleteVenueAssignmentResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" {
		return DeleteVenueAssignment400Response{}, nil
	}
	botID := req.BotId
	if botID <= 0 {
		return DeleteVenueAssignment400Response{}, nil
	}

	err := h.store.DeleteVenueAssignment(ctx, venueID, botID)
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueAssignmentNotFound):
			return DeleteVenueAssignment404Response{}, nil
		case errors.Is(err, ErrVenueInvalid):
			return DeleteVenueAssignment400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "DeleteVenueAssignment failed", slog.String("venue", venueID), slog.Int64("bot_id", botID), slog.String("error", err.Error()))
			}
			return DeleteVenueAssignment500Response{}, nil
		}
	}

	return DeleteVenueAssignment204Response{}, nil
}

// ListBotVenues satisfies StrictServerInterface.
func (h *ApiHandler) ListBotVenues(ctx context.Context, req ListBotVenuesRequestObject) (ListBotVenuesResponseObject, error) {
	botID := req.BotId
	if botID <= 0 {
		return ListBotVenues400Response{}, nil
	}

	assignments, err := h.store.ListBotVenues(ctx, botID)
	if err != nil {
		if errors.Is(err, ErrVenueInvalid) {
			return ListBotVenues400Response{}, nil
		}
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "ListBotVenues failed", slog.Int64("bot_id", botID), slog.String("error", err.Error()))
		}
		return ListBotVenues500Response{}, nil
	}

	return ListBotVenues200JSONResponse{Items: assignments}, nil
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

func (h *ApiHandler) resolveActor() string {
	if h.vault != nil {
		status := h.vault.Status()
		if status.User != nil {
			if username := strings.TrimSpace(status.User.Username); username != "" {
				return username
			}
		}
	}
	return "system"
}

func normalizeBotID(raw int64) (uint32, bool) {
	if raw <= 0 || raw > math.MaxUint32 {
		return 0, false
	}
	return uint32(raw), true
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

func makeDealRecords(rows []tc.Deal) []DealRecord {
	items := make([]DealRecord, 0, len(rows))
	for _, deal := range rows {
		items = append(items, DealRecord{
			DealId:    int64(deal.Id),
			BotId:     int64(deal.BotId),
			CreatedAt: deal.CreatedAt,
			UpdatedAt: deal.UpdatedAt,
			Payload:   deal,
		})
	}
	return items
}

func (h *ApiHandler) buildOrderRecords(ctx context.Context, rows []OrderItem, includeLog bool) []OrderRecord {
	items := make([]OrderRecord, 0, len(rows))
	for _, row := range rows {
		if rec, ok := h.orderRecordFromItem(ctx, row, includeLog); ok {
			items = append(items, rec)
		}
	}
	return items
}

func (h *ApiHandler) orderRecordFromItem(ctx context.Context, row OrderItem, includeLog bool) (OrderRecord, bool) {
	if row.BotEvent == nil {
		h.logger.WarnContext(ctx, "order row missing bot event",
			slog.String("orderid", row.OrderId.Hex()))
		return OrderRecord{}, false
	}

	identifiers := makeOrderIdentifiers(row.OrderId, row.BotEvent, row.ObservedAt, row.Identifier)

	record := OrderRecord{
		OrderId:     row.OrderId.Hex(),
		Identifiers: identifiers,
		ObservedAt:  row.ObservedAt,
		ThreeCommas: ThreeCommasOrderState{
			Event: convertThreeCommasBotEvent(row.BotEvent),
		},
	}

	if state := buildHyperliquidOrderState(&identifiers, row.LatestSubmission, row.LatestStatus); state != nil {
		record.Hyperliquid = state
	}

	if includeLog && len(row.LogEntries) > 0 {
		entries := make([]OrderLogEntry, 0, len(row.LogEntries))
		identCopy := record.Identifiers
		for _, logRow := range row.LogEntries {
			entry, ok := h.makeOrderLogEntry(ctx, logRow.OrderId, logRow.ObservedAt, logRow.Type, logRow.BotEvent, logRow.Submission, logRow.Status, logRow.ScalerConfig, logRow.ScaledAudit, logRow.Actor, logRow.Identifier, &identCopy, nil)
			if !ok {
				continue
			}
			entries = append(entries, entry)
		}
		if len(entries) > 0 {
			record.LogEntries = &entries
		}
	}

	return record, true
}

// CancelOrderByOrderId satisfies StrictServerInterface.
func (h *ApiHandler) CancelOrderByOrderId(ctx context.Context, req CancelOrderByOrderIdRequestObject) (CancelOrderByOrderIdResponseObject, error) {
	if h.orders == nil {
		if h.logger != nil {
			h.logger.Warn("CancelOrderByOrderId requested but emitter not configured")
		}
		return CancelOrderByOrderId500Response{}, nil
	}

	rawOrderId := strings.TrimSpace(req.OrderId)
	if rawOrderId == "" {
		return CancelOrderByOrderId400Response{}, nil
	}

	oid, err := orderid.FromHexString(rawOrderId)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("CancelOrderByOrderId invalid orderid", slog.String("orderid", rawOrderId), slog.String("error", err.Error()))
		}
		return CancelOrderByOrderId400Response{}, nil
	}

	idents, err := h.store.ListSubmissionIdentifiersForOrder(ctx, *oid)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("CancelOrderByOrderId list identifiers failed", slog.String("orderid", oid.Hex()), slog.String("error", err.Error()))
		}
		return CancelOrderByOrderId500Response{}, nil
	}
	venueFilter := ""
	if req.Params.VenueId != nil {
		venueFilter = strings.TrimSpace(*req.Params.VenueId)
	}
	if venueFilter != "" {
		filtered := idents[:0]
		for _, ident := range idents {
			if strings.EqualFold(ident.Venue(), venueFilter) {
				filtered = append(filtered, ident)
			}
		}
		idents = filtered
	}
	if len(idents) == 0 {
		if h.logger != nil {
			h.logger.Info("CancelOrderByOrderId submission not found", slog.String("orderid", oid.Hex()))
		}
		return CancelOrderByOrderId404Response{}, nil
	}

	var (
		coin              string
		pending           []recomma.OrderIdentifier
		lastStatus        *hyperliquid.WsOrder
		haveLastStatus    bool
		anySubmission     bool
		anyCancelRecorded bool
		anyNotCancelable  bool
	)

	for _, ident := range idents {
		action, found, loadErr := h.store.LoadHyperliquidSubmission(ctx, ident)
		if loadErr != nil {
			if h.logger != nil {
				h.logger.Error("CancelOrderByOrderId load submission failed", slog.String("orderid", oid.Hex()), slog.String("venue", ident.Venue()), slog.String("error", loadErr.Error()))
			}
			return CancelOrderByOrderId500Response{}, nil
		}
		if !found || (action.Type != recomma.ActionCreate && action.Type != recomma.ActionModify) {
			continue
		}
		anySubmission = true

		if coin == "" {
			if action.Type == recomma.ActionCreate && strings.TrimSpace(action.Create.Coin) != "" {
				coin = strings.ToUpper(strings.TrimSpace(action.Create.Coin))
			} else if action.Type == recomma.ActionModify && strings.TrimSpace(action.Modify.Order.Coin) != "" {
				coin = strings.ToUpper(strings.TrimSpace(action.Modify.Order.Coin))
			}
		}

		if action.Type == recomma.ActionCancel {
			anyCancelRecorded = true
			continue
		}

		status, haveStatus, statusErr := h.store.LoadHyperliquidStatus(ctx, ident)
		if statusErr != nil {
			if h.logger != nil {
				h.logger.Error("CancelOrderByOrderId load status failed", slog.String("orderid", oid.Hex()), slog.String("venue", ident.Venue()), slog.String("error", statusErr.Error()))
			}
			return CancelOrderByOrderId500Response{}, nil
		}
		if haveStatus {
			lastStatus = status
			haveLastStatus = true
			if !isCancelableStatus(status) {
				anyNotCancelable = true
				continue
			}
		}

		pending = append(pending, ident)
	}

	if !anySubmission {
		if h.logger != nil {
			h.logger.Info("CancelOrderByOrderId submission not found", slog.String("orderid", oid.Hex()))
		}
		return CancelOrderByOrderId404Response{}, nil
	}
	if coin == "" {
		if h.logger != nil {
			h.logger.Info("CancelOrderByOrderId missing coin", slog.String("orderid", oid.Hex()))
		}
		return CancelOrderByOrderId404Response{}, nil
	}
	if len(pending) == 0 {
		if h.logger != nil {
			switch {
			case anyNotCancelable && haveLastStatus && lastStatus != nil:
				h.logger.Info("CancelOrderByOrderId order not cancelable", slog.String("orderid", oid.Hex()), slog.String("status", string(lastStatus.Status)))
			case anyCancelRecorded:
				h.logger.Info("CancelOrderByOrderId cancel already recorded", slog.String("orderid", oid.Hex()))
			default:
				h.logger.Info("CancelOrderByOrderId no pending venues", slog.String("orderid", oid.Hex()))
			}
		}
		return CancelOrderByOrderId409Response{}, nil
	}

	var (
		dryRun bool
		reason string
	)
	if req.Body != nil {
		if req.Body.DryRun != nil {
			dryRun = *req.Body.DryRun
		}
		if req.Body.Reason != nil {
			reason = strings.TrimSpace(*req.Body.Reason)
		}
	}

	cancelPayload := hyperliquid.CancelOrderRequestByCloid{
		Coin:  coin,
		Cloid: oid.Hex(),
	}
	resp := CancelOrderByOrderIdResponse{
		OrderId: oid.Hex(),
		Cancel: &HyperliquidCancelOrder{
			Coin:  cancelPayload.Coin,
			Cloid: cancelPayload.Cloid,
		},
	}

	if dryRun {
		resp.Status = "validated"
		resp.Message = strPtr("dry-run requested; cancel not enqueued")
		return CancelOrderByOrderId202JSONResponse(resp), nil
	}

	for _, ident := range pending {
		work := recomma.OrderWork{
			Identifier: ident,
			OrderId:    *oid,
			Action: recomma.Action{
				Type:   recomma.ActionCancel,
				Cancel: cancelPayload,
			},
		}
		if reason != "" {
			work.Action.Reason = reason
		}

		if err := h.orders.Emit(ctx, work); err != nil {
			if h.logger != nil {
				h.logger.Error("CancelOrderByOrderId emit failed", slog.String("orderid", oid.Hex()), slog.String("venue", ident.Venue()), slog.String("error", err.Error()))
			}
			return CancelOrderByOrderId500Response{}, nil
		}
	}

	resp.Status = "queued"
	if reason != "" {
		resp.Message = strPtr(reason)
	} else if haveLastStatus && lastStatus != nil {
		resp.Message = strPtr(fmt.Sprintf("latest status: %s", lastStatus.Status))
	}

	if h.logger != nil {
		h.logger.Info("CancelOrderByOrderId queued", slog.String("orderid", oid.Hex()), slog.String("coin", coin))
	}

	return CancelOrderByOrderId202JSONResponse(resp), nil
}

// StreamOrders satisfies StrictServerInterface.
func (h *ApiHandler) StreamOrders(ctx context.Context, req StreamOrdersRequestObject) (StreamOrdersResponseObject, error) {
	if h.stream == nil {
		return nil, fmt.Errorf("order streaming not configured")
	}

	filter := StreamFilter{
		OrderIdPrefix: req.Params.OrderId,
		BotID:         req.Params.BotId,
		DealID:        req.Params.DealId,
		BotEventID:    req.Params.BotEventId,
		ObservedFrom:  req.Params.ObservedFrom,
	}

	ch, err := h.stream.Subscribe(ctx, filter)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if err := h.writeSSEFrame(ctx, pw, evt); err != nil {
					h.logger.WarnContext(ctx, "write SSE frame", slog.String("error", err.Error()))
					return
				}
			}
		}
	}()

	return StreamOrders200TexteventStreamResponse{
		Body: pr,
	}, nil
}

// GetSystemStatus satisfies StrictServerInterface.
func (h *ApiHandler) GetSystemStatus(ctx context.Context, req GetSystemStatusRequestObject) (GetSystemStatusResponseObject, error) {
	if h.systemStatus == nil {
		return GetSystemStatus500Response{}, nil
	}

	snapshot := h.systemStatus.Snapshot()
	return GetSystemStatus200JSONResponse(snapshot), nil
}

// StreamSystemEvents satisfies StrictServerInterface.
func (h *ApiHandler) StreamSystemEvents(ctx context.Context, req StreamSystemEventsRequestObject) (StreamSystemEventsResponseObject, error) {
	h.logger.InfoContext(ctx, "StreamSystemEvents called")

	if h.systemStream == nil {
		h.logger.ErrorContext(ctx, "systemStream is nil")
		return StreamSystemEvents500Response{}, nil
	}

	h.logger.InfoContext(ctx, "Subscribing to system stream")
	eventCh, err := h.systemStream.Subscribe(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to subscribe to system stream", slog.String("error", err.Error()))
		return StreamSystemEvents500Response{}, nil
	}

	h.logger.InfoContext(ctx, "Subscription successful, starting SSE stream")

	// Use custom SSE response that flushes after each write
	return streamSystemEventsSSEResponse{
		ctx:     ctx,
		eventCh: eventCh,
		handler: h,
	}, nil
}

// streamSystemEventsSSEResponse implements custom SSE streaming with explicit flushing
type streamSystemEventsSSEResponse struct {
	ctx     context.Context
	eventCh <-chan SystemEvent
	handler *ApiHandler
}

func (r streamSystemEventsSSEResponse) VisitStreamSystemEventsResponse(w http.ResponseWriter) error {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		r.handler.logger.Error("ResponseWriter does not support flushing")
		return fmt.Errorf("streaming not supported")
	}

	// CRITICAL: Write initial SSE comment and flush immediately to complete handshake
	if _, err := w.Write([]byte(": connected\n\n")); err != nil {
		r.handler.logger.WarnContext(r.ctx, "failed to write initial SSE comment", slog.String("error", err.Error()))
		return err
	}
	flusher.Flush()
	r.handler.logger.InfoContext(r.ctx, "SSE handshake completed, headers flushed")

	// Stream events
	for {
		select {
		case <-r.ctx.Done():
			r.handler.logger.InfoContext(r.ctx, "SSE context cancelled")
			return nil
		case evt, ok := <-r.eventCh:
			if !ok {
				r.handler.logger.InfoContext(r.ctx, "Event channel closed")
				return nil
			}
			r.handler.logger.InfoContext(r.ctx, "Writing system event to SSE",
				slog.String("level", string(evt.Level)),
				slog.String("source", evt.Source))

			if err := r.handler.writeSystemSSEFrame(w, evt); err != nil {
				r.handler.logger.WarnContext(r.ctx, "write system event frame",
					slog.String("error", err.Error()))
				return err
			}
			flusher.Flush()
		}
	}
}

func (h *ApiHandler) writeSystemSSEFrame(w io.Writer, evt SystemEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal system event: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("event: system_")
	buf.WriteString(string(evt.Level))
	buf.WriteString("\ndata: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	_, err = w.Write(buf.Bytes())
	return err
}

// StreamHyperliquidPrices satisfies StrictServerInterface.
func (h *ApiHandler) StreamHyperliquidPrices(ctx context.Context, req StreamHyperliquidPricesRequestObject) (StreamHyperliquidPricesResponseObject, error) {
	if h.prices == nil {
		if h.logger != nil {
			h.logger.Warn("StreamHyperliquidPrices requested but price source not configured")
		}
		return StreamHyperliquidPrices500Response{}, nil
	}

	coins := dedupeCoins(req.Params.Coin)
	if len(coins) == 0 {
		return StreamHyperliquidPrices400Response{}, nil
	}

	type subscription struct {
		cancel context.CancelFunc
		ch     <-chan hl.BestBidOffer
	}

	subs := make([]subscription, 0, len(coins))
	for _, coin := range coins {
		subCtx, cancel := context.WithCancel(ctx)
		stream, err := h.prices.SubscribeBBO(subCtx, coin)
		if err != nil {
			cancel()
			for _, s := range subs {
				s.cancel()
			}
			if h.logger != nil {
				h.logger.Error("StreamHyperliquidPrices subscribe failed", slog.String("coin", coin), slog.String("error", err.Error()))
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPriceSourceNotReady) {
				return StreamHyperliquidPrices500Response{}, nil
			}
			return StreamHyperliquidPrices500Response{}, nil
		}
		if h.logger != nil {
			h.logger.Debug("StreamHyperliquidPrices subscribed coin", slog.String("coin", coin))
		}
		subs = append(subs, subscription{cancel: cancel, ch: stream})
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		defer func() {
			for _, s := range subs {
				s.cancel()
			}
		}()

		updates := make(chan hl.BestBidOffer, len(subs)*4)
		var wg sync.WaitGroup

		for _, sub := range subs {
			wg.Add(1)
			s := sub
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case bbo, ok := <-s.ch:
						if !ok {
							return
						}
						select {
						case updates <- bbo:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}

		go func() {
			wg.Wait()
			close(updates)
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case bbo, ok := <-updates:
				if !ok {
					return
				}
				if err := h.writeBBOFrame(pw, bbo); err != nil {
					if h.logger != nil {
						h.logger.Warn("StreamHyperliquidPrices write frame", slog.String("coin", bbo.Coin), slog.String("error", err.Error()))
					}
					return
				}
			}
		}
	}()

	return StreamHyperliquidPrices200TexteventStreamResponse{Body: pr}, nil
}

// writeSSEFrame marshals the event payload and writes an SSE-formatted frame.
func (h *ApiHandler) writeSSEFrame(ctx context.Context, w io.Writer, evt StreamEvent) error {
	ident := makeOrderIdentifiers(evt.OrderID, evt.BotEvent, evt.ObservedAt, evt.Identifier)

	entry, ok := h.makeOrderLogEntry(ctx, evt.OrderID, evt.ObservedAt, evt.Type, evt.BotEvent, evt.Submission, evt.Status, evt.ScalerConfig, evt.ScaledOrderAudit, evt.Actor, evt.Identifier, &ident, evt.Sequence)
	if !ok {
		return nil
	}

	data, err := entry.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal stream frame: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("event: ")
	buf.WriteString(string(evt.Type))
	buf.WriteString("\n")
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write SSE payload: %w", err)
	}
	return nil
}

func (h *ApiHandler) writeBBOFrame(w io.Writer, bbo hl.BestBidOffer) error {
	ts := bbo.Time
	if ts.IsZero() {
		if h.now != nil {
			ts = h.now()
		} else {
			ts = time.Now()
		}
	}

	payload := bboFramePayload{
		Coin: bbo.Coin,
		Time: ts.UTC(),
		Bid: priceLevelPayload{
			Price: bbo.Bid.Price,
			Size:  bbo.Bid.Size,
		},
		Ask: priceLevelPayload{
			Price: bbo.Ask.Price,
			Size:  bbo.Ask.Size,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal bbo frame: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("event: bbo\n")
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write bbo payload: %w", err)
	}

	return nil
}

func isCancelableStatus(status *hyperliquid.WsOrder) bool {
	if status == nil {
		return true
	}
	value := strings.TrimSpace(string(status.Status))
	if value == "" {
		return true
	}
	return strings.EqualFold(value, "open")
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	copy := s
	return &copy
}

func dedupeCoins(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToUpper(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

type priceLevelPayload struct {
	Price float64 `json:"price"`
	Size  float64 `json:"size"`
}

type bboFramePayload struct {
	Coin string            `json:"coin"`
	Time time.Time         `json:"time"`
	Bid  priceLevelPayload `json:"bid"`
	Ask  priceLevelPayload `json:"ask"`
}

func clampPageSize(v *int32) int {
	if v == nil || *v <= 0 {
		return defaultPageSize
	}
	if *v > maxPageSize {
		return maxPageSize
	}
	return int(*v)
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func marshalToMap(v interface{}) (map[string]interface{}, error) {
	if v == nil {
		return map[string]interface{}{}, nil
	}

	switch typed := v.(type) {
	case map[string]interface{}:
		return typed, nil
	case json.RawMessage:
		if len(typed) == 0 {
			return map[string]interface{}{}, nil
		}
		var out map[string]interface{}
		if err := json.Unmarshal(typed, &out); err != nil {
			return nil, err
		}
		return out, nil
	case []byte:
		if len(typed) == 0 {
			return map[string]interface{}{}, nil
		}
		var out map[string]interface{}
		if err := json.Unmarshal(typed, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return map[string]interface{}{}, nil
		}
		var out map[string]interface{}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

type ListBotsOptions struct {
	BotID       *int64
	UpdatedFrom *time.Time
	UpdatedTo   *time.Time
	Limit       int
	PageToken   string
}

type ListDealsOptions struct {
	DealID      *int64
	BotID       *int64
	UpdatedFrom *time.Time
	UpdatedTo   *time.Time
	Limit       int
	PageToken   string
}

type ListOrdersOptions struct {
	OrderIdPrefix *string
	BotID         *int64
	DealID        *int64
	BotEventID    *int64
	ObservedFrom  *time.Time
	ObservedTo    *time.Time
	IncludeLog    bool
	Limit         int
	PageToken     string
}

type ListOrderScalersOptions struct {
	OrderIdPrefix *string
	BotID         *int64
	DealID        *int64
	BotEventID    *int64
	Limit         int
	PageToken     string
}

/* ---- streaming primitives ---- */

type BotItem struct {
	Bot          tc.Bot
	LastSyncedAt time.Time
}

type OrderItem struct {
	OrderId          orderid.OrderId
	ObservedAt       time.Time
	BotEvent         *tc.BotEvent
	LatestSubmission interface{}
	LatestStatus     *hyperliquid.WsOrder
	Identifier       *recomma.OrderIdentifier
	LogEntries       []OrderLogItem
}

type OrderScalerConfigItem struct {
	OrderId    orderid.OrderId
	ObservedAt time.Time
	Actor      string
	Config     EffectiveOrderScaler
}

type OrderLogEntryType string

const (
	ThreeCommasEvent       OrderLogEntryType = "three_commas_event"
	HyperliquidSubmission  OrderLogEntryType = "hyperliquid_submission"
	HyperliquidStatus      OrderLogEntryType = "hyperliquid_status"
	OrderScalerConfigEntry OrderLogEntryType = "order_scaler_config"
	ScaledOrderAuditEntry  OrderLogEntryType = "scaled_order_audit"
)

type OrderLogItem struct {
	Type         OrderLogEntryType
	OrderId      orderid.OrderId
	ObservedAt   time.Time
	Identifier   *recomma.OrderIdentifier
	BotEvent     *tc.BotEvent
	Submission   interface{}
	Status       *hyperliquid.WsOrder
	ScalerConfig *EffectiveOrderScaler
	ScaledAudit  *ScaledOrderAudit
	Actor        *string
}

type StreamFilter struct {
	OrderIdPrefix *string
	BotID         *int64
	DealID        *int64
	BotEventID    *int64
	ObservedFrom  *time.Time
}

type StreamEvent struct {
	Type             OrderLogEntryType
	OrderID          orderid.OrderId
	Identifier       *recomma.OrderIdentifier
	ObservedAt       time.Time
	BotEvent         *tc.BotEvent
	Submission       interface{}
	Status           *hyperliquid.WsOrder
	Sequence         *int64
	ScalerConfig     *EffectiveOrderScaler
	ScaledOrderAudit *ScaledOrderAudit
	Actor            *string
}

func makeOrderIdentifiers(oid orderid.OrderId, event *tc.BotEvent, fallback time.Time, ident *recomma.OrderIdentifier) OrderIdentifiers {
	createdAt := fallback
	if event != nil && !event.CreatedAt.IsZero() {
		createdAt = event.CreatedAt
	}
	var venueID, wallet string
	if ident != nil {
		venueID = ident.Venue()
		wallet = ident.Wallet
	}
	return OrderIdentifiers{
		Hex:        oid.Hex(),
		BotId:      int64(oid.BotID),
		DealId:     int64(oid.DealID),
		BotEventId: int64(oid.BotEventID),
		CreatedAt:  createdAt,
		VenueId:    venueID,
		Wallet:     wallet,
	}
}

func convertThreeCommasBotEvent(evt *tc.BotEvent) ThreeCommasBotEvent {
	if evt == nil {
		return ThreeCommasBotEvent{}
	}

	profit := evt.Profit
	profitCurrency := evt.ProfitCurrency
	profitUSD := evt.ProfitUSD
	profitPercentage := evt.ProfitPercentage

	return ThreeCommasBotEvent{
		CreatedAt:        evt.CreatedAt,
		Action:           string(evt.Action),
		Coin:             evt.Coin,
		Type:             string(evt.Type),
		Status:           string(evt.Status),
		Price:            evt.Price,
		Size:             evt.Size,
		OrderType:        string(evt.OrderType),
		OrderSize:        evt.OrderSize,
		OrderPosition:    evt.OrderPosition,
		QuoteVolume:      evt.QuoteVolume,
		QuoteCurrency:    evt.QuoteCurrency,
		IsMarket:         evt.IsMarket,
		Text:             evt.Text,
		Profit:           &profit,
		ProfitCurrency:   &profitCurrency,
		ProfitUsd:        &profitUSD,
		ProfitPercentage: &profitPercentage,
	}
}

func buildHyperliquidOrderState(identifiers *OrderIdentifiers, submission interface{}, status *hyperliquid.WsOrder) *HyperliquidOrderState {
	var state HyperliquidOrderState

	if identifiers != nil {
		clone := *identifiers
		state.Identifier = &clone
	}

	if action, ok := convertHyperliquidAction(submission); ok {
		state.LatestSubmission = &action
	}
	if ws, ok := convertHyperliquidWsOrder(status); ok {
		state.LatestStatus = &ws
	}
	if state.LatestSubmission == nil && state.LatestStatus == nil {
		return nil
	}
	return &state
}

func convertHyperliquidAction(payload interface{}) (HyperliquidAction, bool) {
	var action HyperliquidAction
	switch v := payload.(type) {
	case *hyperliquid.CreateOrderRequest:
		if v == nil {
			return HyperliquidAction{}, false
		}
		create := convertHyperliquidCreateOrder(*v)
		if err := action.FromHyperliquidCreateAction(HyperliquidCreateAction{Order: create}); err != nil {
			return HyperliquidAction{}, false
		}
		return action, true
	case hyperliquid.CreateOrderRequest:
		create := convertHyperliquidCreateOrder(v)
		if err := action.FromHyperliquidCreateAction(HyperliquidCreateAction{Order: create}); err != nil {
			return HyperliquidAction{}, false
		}
		return action, true
	case *hyperliquid.ModifyOrderRequest:
		if v == nil {
			return HyperliquidAction{}, false
		}
		modify, ok := convertHyperliquidModify(*v)
		if !ok {
			return HyperliquidAction{}, false
		}
		if err := action.FromHyperliquidModifyAction(modify); err != nil {
			return HyperliquidAction{}, false
		}
		return action, true
	case hyperliquid.ModifyOrderRequest:
		modify, ok := convertHyperliquidModify(v)
		if !ok {
			return HyperliquidAction{}, false
		}
		if err := action.FromHyperliquidModifyAction(modify); err != nil {
			return HyperliquidAction{}, false
		}
		return action, true
	case *hyperliquid.CancelOrderRequestByCloid:
		if v == nil {
			return HyperliquidAction{}, false
		}
		cancel := convertHyperliquidCancel(*v)
		if err := action.FromHyperliquidCancelAction(cancel); err != nil {
			return HyperliquidAction{}, false
		}
		return action, true
	case hyperliquid.CancelOrderRequestByCloid:
		cancel := convertHyperliquidCancel(v)
		if err := action.FromHyperliquidCancelAction(cancel); err != nil {
			return HyperliquidAction{}, false
		}
		return action, true
	default:
		return HyperliquidAction{}, false
	}
}

func convertHyperliquidCreateOrder(req hyperliquid.CreateOrderRequest) HyperliquidCreateOrder {
	order := HyperliquidCreateOrder{
		Coin:       req.Coin,
		IsBuy:      req.IsBuy,
		Price:      req.Price,
		Size:       req.Size,
		ReduceOnly: req.ReduceOnly,
		OrderType:  convertHyperliquidOrderType(req.OrderType),
	}
	if req.ClientOrderID != nil {
		order.Cloid = req.ClientOrderID
	}
	return order
}

func convertHyperliquidOrderType(src hyperliquid.OrderType) HyperliquidOrderType {
	var out HyperliquidOrderType
	if src.Limit != nil {
		out.Limit = &HyperliquidLimitOrder{Tif: string(src.Limit.Tif)}
	}
	if src.Trigger != nil {
		out.Trigger = &HyperliquidTriggerOrder{
			TriggerPx: src.Trigger.TriggerPx,
			IsMarket:  src.Trigger.IsMarket,
			Tpsl:      string(src.Trigger.Tpsl),
		}
	}
	return out
}

func convertHyperliquidModify(req hyperliquid.ModifyOrderRequest) (HyperliquidModifyAction, bool) {
	action := HyperliquidModifyAction{
		Order: convertHyperliquidCreateOrder(req.Order),
	}

	if req.Cloid != nil {
		action.Cloid = &req.Cloid.Value
	}

	if req.Oid != nil {
		action.Oid = req.Oid
	}

	if action.Oid == nil && action.Cloid == nil && req.Order.ClientOrderID != nil {
		val := *req.Order.ClientOrderID
		action.Cloid = &val
	}

	if action.Oid == nil && action.Cloid == nil {
		return HyperliquidModifyAction{}, false
	}

	return action, true
}

func convertHyperliquidCancel(req hyperliquid.CancelOrderRequestByCloid) HyperliquidCancelAction {
	return HyperliquidCancelAction{
		Cancel: HyperliquidCancelOrder{
			Coin:  req.Coin,
			Cloid: req.Cloid,
		},
	}
}

func convertHyperliquidWsOrder(src *hyperliquid.WsOrder) (HyperliquidWsOrder, bool) {
	if src == nil {
		return HyperliquidWsOrder{}, false
	}

	order := HyperliquidWsBasicOrder{
		Coin:      src.Order.Coin,
		Side:      src.Order.Side,
		LimitPx:   src.Order.LimitPx,
		Size:      src.Order.Sz,
		Oid:       src.Order.Oid,
		Timestamp: src.Order.Timestamp,
		OrigSize:  src.Order.OrigSz,
		Cloid:     src.Order.Cloid,
	}

	return HyperliquidWsOrder{
		Order:           order,
		Status:          HyperliquidOrderStatus(src.Status),
		StatusTimestamp: src.StatusTimestamp,
	}, true
}

func cloneIdentifiers(base *OrderIdentifiers, oid orderid.OrderId, createdAt, fallback time.Time) *OrderIdentifiers {
	var ident OrderIdentifiers
	if base != nil {
		ident = *base
	}
	ident.Hex = oid.Hex()
	ident.BotId = int64(oid.BotID)
	ident.DealId = int64(oid.DealID)
	ident.BotEventId = int64(oid.BotEventID)
	if !createdAt.IsZero() {
		ident.CreatedAt = createdAt
	} else if ident.CreatedAt.IsZero() {
		ident.CreatedAt = fallback
	}
	return &ident
}

func (h *ApiHandler) makeOrderLogEntry(ctx context.Context, oid orderid.OrderId, observedAt time.Time, entryType OrderLogEntryType, botEvent *tc.BotEvent, submission interface{}, status *hyperliquid.WsOrder, config *EffectiveOrderScaler, audit *ScaledOrderAudit, actor *string, logIdentifier *recomma.OrderIdentifier, baseIdentifiers *OrderIdentifiers, sequence *int64) (OrderLogEntry, bool) {
	oidHex := oid.Hex()
	var entry OrderLogEntry
	var botEventID *int64
	if id := oid.BotEventID; id != 0 {
		val := int64(id)
		botEventID = &val
	}

	clampedConfig := clampEffectiveOrderScaler(config, h.orderScalerMaxMultiplier)

	switch entryType {
	case ThreeCommasEvent:
		if botEvent == nil {
			h.logger.WarnContext(ctx, "missing bot event for log entry",
				slog.String("orderid", oidHex))
			return OrderLogEntry{}, false
		}
		logEntry := ThreeCommasLogEntry{
			OrderId:    oidHex,
			ObservedAt: observedAt,
			Event:      convertThreeCommasBotEvent(botEvent),
			BotEventId: botEventID,
		}
		if logIdentifier != nil {
			ident := makeOrderIdentifiers(oid, botEvent, observedAt, logIdentifier)
			logEntry.Identifiers = &ident
		} else if identifiers := cloneIdentifiers(baseIdentifiers, oid, botEvent.CreatedAt, observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromThreeCommasLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal threecommas log entry",
				slog.String("orderid", oidHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	case HyperliquidSubmission:
		action, ok := convertHyperliquidAction(submission)
		if !ok {
			h.logger.WarnContext(ctx, "unexpected submission payload",
				slog.String("orderid", oidHex))
			return OrderLogEntry{}, false
		}
		logEntry := HyperliquidSubmissionLogEntry{
			OrderId:    oidHex,
			ObservedAt: observedAt,
			Action:     action,
			BotEventId: botEventID,
		}
		if logIdentifier != nil {
			ident := makeOrderIdentifiers(oid, botEvent, observedAt, logIdentifier)
			logEntry.Identifiers = &ident
		} else if identifiers := cloneIdentifiers(baseIdentifiers, oid, createdAt(botEvent), observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromHyperliquidSubmissionLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal hyperliquid submission log entry",
				slog.String("orderid", oidHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	case HyperliquidStatus:
		ws, ok := convertHyperliquidWsOrder(status)
		if !ok {
			h.logger.WarnContext(ctx, "missing hyperliquid status payload",
				slog.String("orderid", oidHex))
			return OrderLogEntry{}, false
		}
		logEntry := HyperliquidStatusLogEntry{
			OrderId:    oidHex,
			ObservedAt: observedAt,
			Status:     ws,
			BotEventId: botEventID,
		}
		if logIdentifier != nil {
			ident := makeOrderIdentifiers(oid, botEvent, observedAt, logIdentifier)
			logEntry.Identifiers = &ident
		} else if identifiers := cloneIdentifiers(baseIdentifiers, oid, createdAt(botEvent), observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromHyperliquidStatusLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal hyperliquid status log entry",
				slog.String("orderid", oidHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	case OrderScalerConfigEntry:
		if clampedConfig == nil {
			h.logger.WarnContext(ctx, "missing scaler config payload",
				slog.String("orderid", oidHex))
			return OrderLogEntry{}, false
		}
		actorVal := ""
		if actor != nil {
			actorVal = *actor
		}
		logEntry := OrderScalerConfigLogEntry{
			OrderId:    oidHex,
			ObservedAt: observedAt,
			Config:     *clampedConfig,
			Actor:      actorVal,
		}
		if logIdentifier != nil {
			ident := makeOrderIdentifiers(oid, nil, observedAt, logIdentifier)
			logEntry.Identifiers = &ident
		} else if identifiers := cloneIdentifiers(baseIdentifiers, oid, observedAt, observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromOrderScalerConfigLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal scaler config log entry",
				slog.String("orderid", oidHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	case ScaledOrderAuditEntry:
		if audit == nil {
			h.logger.WarnContext(ctx, "missing scaled order audit payload",
				slog.String("orderid", oidHex))
			return OrderLogEntry{}, false
		}
		actorVal := ""
		if actor != nil {
			actorVal = *actor
		}
		logEntry := ScaledOrderAuditLogEntry{
			OrderId:    oidHex,
			ObservedAt: observedAt,
			Audit:      *audit,
			Actor:      actorVal,
		}
		if clampedConfig != nil {
			cfgCopy := *clampedConfig
			logEntry.Effective = &cfgCopy
		}
		if logIdentifier != nil {
			ident := makeOrderIdentifiers(oid, botEvent, observedAt, logIdentifier)
			logEntry.Identifiers = &ident
		} else if identifiers := cloneIdentifiers(baseIdentifiers, oid, createdAt(botEvent), observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromScaledOrderAuditLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal scaled order audit log entry",
				slog.String("orderid", oidHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	default:
		h.logger.WarnContext(ctx, "unknown log entry type",
			slog.String("orderid", oidHex),
			slog.String("type", string(entryType)))
		return OrderLogEntry{}, false
	}

	return entry, true
}

func createdAt(evt *tc.BotEvent) time.Time {
	if evt == nil {
		return time.Time{}
	}
	return evt.CreatedAt
}
