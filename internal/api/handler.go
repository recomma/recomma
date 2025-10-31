package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/metadata"
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
	LoadHyperliquidSubmission(ctx context.Context, md metadata.Metadata) (recomma.Action, bool, error)
	LoadHyperliquidStatus(ctx context.Context, md metadata.Metadata) (*hyperliquid.WsOrder, bool, error)
}

// StreamSource publishes live order mutations for the SSE endpoint.
type StreamSource interface {
	Subscribe(ctx context.Context, filter StreamFilter) (<-chan StreamEvent, error)
}

// ApiHandler implements api.StrictServerInterface.
type ApiHandler struct {
	store    Store
	stream   StreamSource
	logger   *slog.Logger
	now      func() time.Time
	webauthn *WebAuthnService
	vault    *vault.Controller
	session  *vaultSessionManager

	orders recomma.Emitter
	prices HyperliquidPriceSource
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
		MetadataPrefix: req.Params.Metadata,
		BotID:          req.Params.BotId,
		DealID:         req.Params.DealId,
		BotEventID:     req.Params.BotEventId,
		ObservedFrom:   req.Params.ObservedFrom,
		ObservedTo:     req.Params.ObservedTo,
		IncludeLog:     includeLog,
		Limit:          limit,
		PageToken:      deref(req.Params.PageToken),
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
			slog.String("metadata", row.Metadata.Hex()))
		return OrderRecord{}, false
	}

	identifiers := makeOrderIdentifiers(row.Metadata, row.BotEvent, row.ObservedAt)

	record := OrderRecord{
		Metadata:    row.Metadata.Hex(),
		Identifiers: identifiers,
		ObservedAt:  row.ObservedAt,
		ThreeCommas: ThreeCommasOrderState{
			Event: convertThreeCommasBotEvent(row.BotEvent),
		},
	}

	if state := buildHyperliquidOrderState(row.LatestSubmission, row.LatestStatus); state != nil {
		record.Hyperliquid = state
	}

	if includeLog && len(row.LogEntries) > 0 {
		entries := make([]OrderLogEntry, 0, len(row.LogEntries))
		identCopy := record.Identifiers
		for _, logRow := range row.LogEntries {
			entry, ok := h.makeOrderLogEntry(ctx, logRow.Metadata, logRow.ObservedAt, logRow.Type, logRow.BotEvent, logRow.Submission, logRow.Status, &identCopy, nil)
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

// CancelOrderByMetadata satisfies StrictServerInterface.
func (h *ApiHandler) CancelOrderByMetadata(ctx context.Context, req CancelOrderByMetadataRequestObject) (CancelOrderByMetadataResponseObject, error) {
	if h.orders == nil {
		if h.logger != nil {
			h.logger.Warn("CancelOrderByMetadata requested but emitter not configured")
		}
		return CancelOrderByMetadata500Response{}, nil
	}

	rawMetadata := strings.TrimSpace(req.Metadata)
	if rawMetadata == "" {
		return CancelOrderByMetadata400Response{}, nil
	}

	md, err := metadata.FromHexString(rawMetadata)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("CancelOrderByMetadata invalid metadata", slog.String("metadata", rawMetadata), slog.String("error", err.Error()))
		}
		return CancelOrderByMetadata400Response{}, nil
	}

	action, found, err := h.store.LoadHyperliquidSubmission(ctx, *md)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("CancelOrderByMetadata load submission failed", slog.String("metadata", md.Hex()), slog.String("error", err.Error()))
		}
		return CancelOrderByMetadata500Response{}, nil
	}
	if !found || (action.Create == nil && action.Modify == nil) {
		if h.logger != nil {
			h.logger.Info("CancelOrderByMetadata submission not found", slog.String("metadata", md.Hex()))
		}
		return CancelOrderByMetadata404Response{}, nil
	}
	if action.Cancel != nil {
		if h.logger != nil {
			h.logger.Info("CancelOrderByMetadata cancel already recorded", slog.String("metadata", md.Hex()))
		}
		return CancelOrderByMetadata409Response{}, nil
	}

	coin := ""
	if action.Create != nil && strings.TrimSpace(action.Create.Coin) != "" {
		coin = strings.ToUpper(strings.TrimSpace(action.Create.Coin))
	} else if action.Modify != nil && strings.TrimSpace(action.Modify.Order.Coin) != "" {
		coin = strings.ToUpper(strings.TrimSpace(action.Modify.Order.Coin))
	}
	if coin == "" {
		if h.logger != nil {
			h.logger.Info("CancelOrderByMetadata missing coin", slog.String("metadata", md.Hex()))
		}
		return CancelOrderByMetadata404Response{}, nil
	}

	status, haveStatus, err := h.store.LoadHyperliquidStatus(ctx, *md)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("CancelOrderByMetadata load status failed", slog.String("metadata", md.Hex()), slog.String("error", err.Error()))
		}
		return CancelOrderByMetadata500Response{}, nil
	}
	if haveStatus && !isCancelableStatus(status) {
		if h.logger != nil {
			h.logger.Info("CancelOrderByMetadata order not cancelable", slog.String("metadata", md.Hex()), slog.String("status", string(status.Status)))
		}
		return CancelOrderByMetadata409Response{}, nil
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
		Cloid: md.Hex(),
	}
	resp := CancelOrderByMetadataResponse{
		Metadata: md.Hex(),
		Cancel: &HyperliquidCancelOrder{
			Coin:          cancelPayload.Coin,
			ClientOrderId: cancelPayload.Cloid,
		},
	}

	if dryRun {
		resp.Status = "validated"
		resp.Message = strPtr("dry-run requested; cancel not enqueued")
		return CancelOrderByMetadata202JSONResponse(resp), nil
	}

	work := recomma.OrderWork{
		MD: *md,
		Action: recomma.Action{
			Type:   recomma.ActionCancel,
			Cancel: &cancelPayload,
		},
	}
	if reason != "" {
		work.Action.Reason = reason
	}

	if err := h.orders.Emit(ctx, work); err != nil {
		if h.logger != nil {
			h.logger.Error("CancelOrderByMetadata emit failed", slog.String("metadata", md.Hex()), slog.String("error", err.Error()))
		}
		return CancelOrderByMetadata500Response{}, nil
	}

	resp.Status = "queued"
	if reason != "" {
		resp.Message = strPtr(reason)
	} else if haveStatus && status != nil {
		resp.Message = strPtr(fmt.Sprintf("latest status: %s", status.Status))
	}

	if h.logger != nil {
		h.logger.Info("CancelOrderByMetadata queued", slog.String("metadata", md.Hex()), slog.String("coin", coin))
	}

	return CancelOrderByMetadata202JSONResponse(resp), nil
}

// StreamOrders satisfies StrictServerInterface.
func (h *ApiHandler) StreamOrders(ctx context.Context, req StreamOrdersRequestObject) (StreamOrdersResponseObject, error) {
	if h.stream == nil {
		return nil, fmt.Errorf("order streaming not configured")
	}

	filter := StreamFilter{
		MetadataPrefix: req.Params.Metadata,
		BotID:          req.Params.BotId,
		DealID:         req.Params.DealId,
		BotEventID:     req.Params.BotEventId,
		ObservedFrom:   req.Params.ObservedFrom,
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
	ident := makeOrderIdentifiers(evt.Metadata, evt.BotEvent, evt.ObservedAt)

	entry, ok := h.makeOrderLogEntry(ctx, evt.Metadata, evt.ObservedAt, evt.Type, evt.BotEvent, evt.Submission, evt.Status, &ident, evt.Sequence)
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
	MetadataPrefix *string
	BotID          *int64
	DealID         *int64
	BotEventID     *int64
	ObservedFrom   *time.Time
	ObservedTo     *time.Time
	IncludeLog     bool
	Limit          int
	PageToken      string
}

/* ---- streaming primitives ---- */

type BotItem struct {
	Bot          tc.Bot
	LastSyncedAt time.Time
}

type OrderItem struct {
	Metadata         metadata.Metadata
	ObservedAt       time.Time
	BotEvent         *tc.BotEvent
	LatestSubmission interface{}
	LatestStatus     *hyperliquid.WsOrder
	LogEntries       []OrderLogItem
}

type OrderLogEntryType string

const (
	ThreeCommasEvent      OrderLogEntryType = "three_commas_event"
	HyperliquidSubmission OrderLogEntryType = "hyperliquid_submission"
	HyperliquidStatus     OrderLogEntryType = "hyperliquid_status"
	OrderScalerUpdate     OrderLogEntryType = "order_scaler_update"
)

type OrderLogItem struct {
	Type       OrderLogEntryType
	Metadata   metadata.Metadata
	ObservedAt time.Time
	BotEvent   *tc.BotEvent
	Submission interface{}
	Status     *hyperliquid.WsOrder
}

type StreamFilter struct {
	MetadataPrefix *string
	BotID          *int64
	DealID         *int64
	BotEventID     *int64
	ObservedFrom   *time.Time
}

type StreamEvent struct {
	Type       OrderLogEntryType
	Metadata   metadata.Metadata
	ObservedAt time.Time
	BotEvent   *tc.BotEvent
	Submission interface{}
	Status     *hyperliquid.WsOrder
	Sequence   *int64
}

func makeOrderIdentifiers(md metadata.Metadata, event *tc.BotEvent, fallback time.Time) OrderIdentifiers {
	createdAt := fallback
	if event != nil && !event.CreatedAt.IsZero() {
		createdAt = event.CreatedAt
	}
	return OrderIdentifiers{
		Hex:        md.Hex(),
		BotId:      int64(md.BotID),
		DealId:     int64(md.DealID),
		BotEventId: int64(md.BotEventID),
		CreatedAt:  createdAt,
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

func buildHyperliquidOrderState(submission interface{}, status *hyperliquid.WsOrder) *HyperliquidOrderState {
	var state HyperliquidOrderState

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
		order.ClientOrderId = req.ClientOrderID
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

	switch oid := req.Oid.(type) {
	case nil:
	case int64:
		action.Oid = &oid
	case int:
		v := int64(oid)
		action.Oid = &v
	case float64:
		v := int64(oid)
		action.Oid = &v
	case string:
		val := oid
		action.ClientOrderId = &val
	case *string:
		if oid != nil {
			val := *oid
			action.ClientOrderId = &val
		}
	case fmt.Stringer:
		val := oid.String()
		action.ClientOrderId = &val
	default:
		// unknown identifier shape
	}

	if action.Oid == nil && action.ClientOrderId == nil && req.Order.ClientOrderID != nil {
		val := *req.Order.ClientOrderID
		action.ClientOrderId = &val
	}

	if action.Oid == nil && action.ClientOrderId == nil {
		return HyperliquidModifyAction{}, false
	}

	return action, true
}

func convertHyperliquidCancel(req hyperliquid.CancelOrderRequestByCloid) HyperliquidCancelAction {
	return HyperliquidCancelAction{
		Cancel: HyperliquidCancelOrder{
			Coin:          req.Coin,
			ClientOrderId: req.Cloid,
		},
	}
}

func convertHyperliquidWsOrder(src *hyperliquid.WsOrder) (HyperliquidWsOrder, bool) {
	if src == nil {
		return HyperliquidWsOrder{}, false
	}

	order := HyperliquidWsBasicOrder{
		Coin:          src.Order.Coin,
		Side:          src.Order.Side,
		LimitPx:       src.Order.LimitPx,
		Size:          src.Order.Sz,
		Oid:           src.Order.Oid,
		Timestamp:     src.Order.Timestamp,
		OrigSize:      src.Order.OrigSz,
		ClientOrderId: src.Order.Cloid,
	}

	return HyperliquidWsOrder{
		Order:           order,
		Status:          HyperliquidOrderStatus(src.Status),
		StatusTimestamp: src.StatusTimestamp,
	}, true
}

func cloneIdentifiers(base *OrderIdentifiers, md metadata.Metadata, createdAt, fallback time.Time) *OrderIdentifiers {
	var ident OrderIdentifiers
	if base != nil {
		ident = *base
	}
	ident.Hex = md.Hex()
	ident.BotId = int64(md.BotID)
	ident.DealId = int64(md.DealID)
	ident.BotEventId = int64(md.BotEventID)
	if !createdAt.IsZero() {
		ident.CreatedAt = createdAt
	} else if ident.CreatedAt.IsZero() {
		ident.CreatedAt = fallback
	}
	return &ident
}

func (h *ApiHandler) makeOrderLogEntry(ctx context.Context, md metadata.Metadata, observedAt time.Time, entryType OrderLogEntryType, botEvent *tc.BotEvent, submission interface{}, status *hyperliquid.WsOrder, baseIdentifiers *OrderIdentifiers, sequence *int64) (OrderLogEntry, bool) {
	metadataHex := md.Hex()
	var entry OrderLogEntry
	var botEventID *int64
	if id := md.BotEventID; id != 0 {
		val := int64(id)
		botEventID = &val
	}

	switch entryType {
	case ThreeCommasEvent:
		if botEvent == nil {
			h.logger.WarnContext(ctx, "missing bot event for log entry",
				slog.String("metadata", metadataHex))
			return OrderLogEntry{}, false
		}
		logEntry := ThreeCommasLogEntry{
			Metadata:   metadataHex,
			ObservedAt: observedAt,
			Event:      convertThreeCommasBotEvent(botEvent),
			BotEventId: botEventID,
		}
		if identifiers := cloneIdentifiers(baseIdentifiers, md, botEvent.CreatedAt, observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromThreeCommasLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal threecommas log entry",
				slog.String("metadata", metadataHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	case HyperliquidSubmission:
		action, ok := convertHyperliquidAction(submission)
		if !ok {
			h.logger.WarnContext(ctx, "unexpected submission payload",
				slog.String("metadata", metadataHex))
			return OrderLogEntry{}, false
		}
		logEntry := HyperliquidSubmissionLogEntry{
			Metadata:   metadataHex,
			ObservedAt: observedAt,
			Action:     action,
			BotEventId: botEventID,
		}
		if identifiers := cloneIdentifiers(baseIdentifiers, md, createdAt(botEvent), observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromHyperliquidSubmissionLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal hyperliquid submission log entry",
				slog.String("metadata", metadataHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	case HyperliquidStatus:
		ws, ok := convertHyperliquidWsOrder(status)
		if !ok {
			h.logger.WarnContext(ctx, "missing hyperliquid status payload",
				slog.String("metadata", metadataHex))
			return OrderLogEntry{}, false
		}
		logEntry := HyperliquidStatusLogEntry{
			Metadata:   metadataHex,
			ObservedAt: observedAt,
			Status:     ws,
			BotEventId: botEventID,
		}
		if identifiers := cloneIdentifiers(baseIdentifiers, md, createdAt(botEvent), observedAt); identifiers != nil {
			logEntry.Identifiers = identifiers
		}
		if sequence != nil {
			logEntry.Sequence = sequence
		}
		if err := entry.FromHyperliquidStatusLogEntry(logEntry); err != nil {
			h.logger.WarnContext(ctx, "marshal hyperliquid status log entry",
				slog.String("metadata", metadataHex),
				slog.String("error", err.Error()))
			return OrderLogEntry{}, false
		}
	default:
		h.logger.WarnContext(ctx, "unknown log entry type",
			slog.String("metadata", metadataHex),
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
