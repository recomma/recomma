package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
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

type priceLevelPayload struct {
	Price float64 `json:"price"`
	Size  float64 `json:"size"`
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
