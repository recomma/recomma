package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	hyperliquid "github.com/sonirico/go-hyperliquid"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/internal/vault"
	"github.com/terwey/recomma/metadata"
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
}

// NewHandler wires everything together.
func NewHandler(store Store, stream StreamSource, opts ...HandlerOption) *ApiHandler {
	h := &ApiHandler{
		store:   store,
		stream:  stream,
		now:     time.Now,
		session: newVaultSessionManager(),
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
	eventPayload, err := marshalToMap(row.BotEvent)
	if err != nil {
		h.logger.WarnContext(ctx, "marshal bot event payload",
			slog.String("metadata_hex", row.Metadata.Hex()),
			slog.String("error", err.Error()))
		return OrderRecord{}, false
	}

	var latestSubmission *map[string]interface{}
	if row.LatestSubmission != nil {
		if v, err := marshalToMap(row.LatestSubmission); err == nil {
			latestSubmission = &v
		} else {
			h.logger.WarnContext(ctx, "marshal latest submission",
				slog.String("metadata_hex", row.Metadata.Hex()),
				slog.String("error", err.Error()))
		}
	}

	var latestStatus *map[string]interface{}
	if row.LatestStatus != nil {
		if v, err := marshalToMap(row.LatestStatus); err == nil {
			latestStatus = &v
		} else {
			h.logger.WarnContext(ctx, "marshal latest status",
				slog.String("metadata_hex", row.Metadata.Hex()),
				slog.String("error", err.Error()))
		}
	}

	record := OrderRecord{
		MetadataHex:      row.Metadata.Hex(),
		BotId:            int64(row.Metadata.BotID),
		DealId:           int64(row.Metadata.DealID),
		BotEventId:       int64(row.Metadata.BotEventID),
		CreatedAt:        row.BotEvent.CreatedAt,
		ObservedAt:       row.ObservedAt,
		BotEventPayload:  eventPayload,
		LatestSubmission: latestSubmission,
		LatestStatus:     latestStatus,
	}

	if includeLog && len(row.LogEntries) > 0 {
		entries := make([]OrderLogEntry, 0, len(row.LogEntries))
		for _, logRow := range row.LogEntries {
			payload, err := payloadForEntry(logRow.Type, logRow.BotEvent, logRow.Submission, logRow.Status)
			if err != nil {
				h.logger.WarnContext(ctx, "marshal log payload",
					slog.String("metadata_hex", row.Metadata.Hex()),
					slog.String("error", err.Error()))
				continue
			}
			entry := OrderLogEntry{
				Type:        logRow.Type,
				MetadataHex: logRow.Metadata.Hex(),
				ObservedAt:  logRow.ObservedAt,
				Payload:     payload,
			}
			if id := logRow.Metadata.BotEventID; id != 0 {
				val := int64(id)
				entry.BotEventId = &val
			}
			entries = append(entries, entry)
		}
		if len(entries) > 0 {
			record.LogEntries = &entries
		}
	}

	return record, true
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
				if err := h.writeSSEFrame(pw, evt); err != nil {
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

// writeSSEFrame marshals the event payload and writes an SSE-formatted frame.
func (h *ApiHandler) writeSSEFrame(w io.Writer, evt StreamEvent) error {
	payload, err := payloadForEntry(evt.Type, evt.BotEvent, evt.Submission, evt.Status)
	if err != nil {
		return fmt.Errorf("marshal stream payload: %w", err)
	}

	frame := map[string]interface{}{
		"type":         string(evt.Type),
		"metadata_hex": evt.Metadata.Hex(),
		"observed_at":  evt.ObservedAt.UTC(),
		"payload":      payload,
	}
	if id := evt.Metadata.BotEventID; id != 0 {
		frame["bot_event_id"] = int64(id)
	}
	if evt.Sequence != nil {
		frame["sequence"] = *evt.Sequence
	}

	data, err := json.Marshal(frame)
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

func payloadForEntry(entryType OrderLogEntryType, botEvent *tc.BotEvent, submission interface{}, status *hyperliquid.WsOrder) (map[string]interface{}, error) {
	switch entryType {
	case ThreeCommasEvent:
		return marshalToMap(botEvent)
	case HyperliquidSubmission:
		return marshalToMap(submission)
	case HyperliquidStatus:
		return marshalToMap(status)
	default:
		return map[string]interface{}{}, nil
	}
}
