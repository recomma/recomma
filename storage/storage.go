package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage/sqlcgen"
	hyperliquid "github.com/sonirico/go-hyperliquid"
)

const (
	defaultHyperliquidVenueID   = recomma.VenueID("hyperliquid:default")
	defaultHyperliquidVenueType = "hyperliquid"
	defaultHyperliquidName      = "Default Hyperliquid Venue"
	defaultHyperliquidWallet    = "default"

	hyperliquidCreatePayloadType = "hyperliquid.create.v1"
	hyperliquidModifyPayloadType = "hyperliquid.modify.v1"
	hyperliquidCancelPayloadType = "hyperliquid.cancel.v1"
	hyperliquidStatusPayloadType = "hyperliquid.status.v1"
)

//go:embed sqlc/schema.sql
var schemaDDL string

type Storage struct {
	db      *sql.DB
	queries *sqlcgen.Queries
	mu      sync.Mutex
	stream  api.StreamPublisher
}

// StorageOption configures Storage optional dependencies.
type StorageOption func(*Storage)

// WithStreamPublisher injects the StreamPublisher service used for SSE
func WithStreamPublisher(stream api.StreamPublisher) StorageOption {
	return func(h *Storage) {
		h.stream = stream
	}
}

func WithLogger(logger *slog.Logger) StorageOption {
	return func(s *Storage) {
		if logger != nil {
			wrapped := loggingDB{inner: s.db, logger: logger}
			s.queries = sqlcgen.New(wrapped)
		}
	}
}

func New(path string, opts ...StorageOption) (*Storage, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx := context.Background()

	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite db: %w", err)
	}

	s := &Storage{
		db:      db,
		queries: sqlcgen.New(db),
	}

	for _, opt := range opts {
		opt(s)
	}

	if err := s.ensureDefaultVenue(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ensure default venue: %w", err)
	}

	return s, nil
}

func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

func (s *Storage) ensureDefaultVenue(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	flags := json.RawMessage(`{}`)
	params := sqlcgen.UpsertVenueParams{
		ID:          string(defaultHyperliquidVenueID),
		Type:        defaultHyperliquidVenueType,
		DisplayName: defaultHyperliquidName,
		Wallet:      defaultHyperliquidWallet,
		Flags:       flags,
	}

	return s.queries.UpsertVenue(ctx, params)
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// DefaultHyperliquidIdentifier returns the default venue identifier used when
// no explicit venue configuration has been supplied. Callers that operate in a
// single-venue environment can use this helper to preserve the previous
// behaviour without hard-coding the venue metadata.
func DefaultHyperliquidIdentifier(oid orderid.OrderId) recomma.OrderIdentifier {
	return recomma.NewOrderIdentifier(defaultHyperliquidVenueID, defaultHyperliquidWallet, oid)
}

func ensureIdentifier(ident recomma.OrderIdentifier) recomma.OrderIdentifier {
	if ident.VenueID == "" {
		ident.VenueID = defaultHyperliquidVenueID
	}
	if ident.Wallet == "" {
		ident.Wallet = defaultHyperliquidWallet
	}
	return ident
}

// RecordThreeCommasBotEvent records the threecommas botevents that we acted upon
func (s *Storage) RecordThreeCommasBotEvent(ctx context.Context, oid orderid.OrderId, order tc.BotEvent) (lastInsertId int64, err error) {
	raw, err := json.Marshal(order)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.InsertThreeCommasBotEventParams{
		OrderID:      oid.Hex(),
		BotID:        int64(oid.BotID),
		DealID:       int64(oid.DealID),
		BoteventID:   int64(oid.BotEventID),
		CreatedAtUtc: order.CreatedAt.UTC().UnixMilli(),
		Payload:      raw,
	}

	lastInsertId, err = s.queries.InsertThreeCommasBotEvent(ctx, params)
	if err != nil {
		return 0, err
	}

	if lastInsertId != 0 {
		clone := order
		s.publishStreamEventLocked(api.StreamEvent{
			Type:     api.ThreeCommasEvent,
			OrderID:  oid,
			BotEvent: &clone,
		})
	}

	return lastInsertId, nil
}

// RecordThreeCommasBotEvent records all threecommas botevents, acted upon or not
func (s *Storage) RecordThreeCommasBotEventLog(ctx context.Context, oid orderid.OrderId, order tc.BotEvent) (lastInsertId int64, err error) {
	raw, err := json.Marshal(order)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.InsertThreeCommasBotEventLogParams{
		OrderID:      oid.Hex(),
		BotID:        int64(oid.BotID),
		DealID:       int64(oid.DealID),
		BoteventID:   int64(oid.BotEventID),
		CreatedAtUtc: order.CreatedAt.UTC().UnixMilli(),
		Payload:      raw,
	}

	lastInsertId, err = s.queries.InsertThreeCommasBotEventLog(ctx, params)
	if err != nil {
		return 0, err
	}

	return lastInsertId, nil
}

func (s *Storage) ListEventsForOrder(ctx context.Context, botID, dealID, botEventID uint32) ([]recomma.BotEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListThreeCommasBotEventsForOrder(ctx, sqlcgen.ListThreeCommasBotEventsForOrderParams{
		BotID:      int64(botID),
		DealID:     int64(dealID),
		BoteventID: int64(botEventID),
	})
	if err != nil {
		return nil, err
	}

	events := make([]recomma.BotEvent, 0, len(rows))
	for _, row := range rows {
		var evt tc.BotEvent
		if err := json.Unmarshal(row.Payload, &evt); err != nil {
			return nil, fmt.Errorf("decode bot event: %w", err)
		}
		events = append(events, recomma.BotEvent{
			RowID:    row.ID,
			BotEvent: evt,
		})
	}

	return events, nil
}

// ListEventsLog returns all BotEvents recorded, irrelevant if we acted on it.
func (s *Storage) ListEventsLog(ctx context.Context) ([]recomma.BotEventLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListThreeCommasBotEventLogs(ctx)
	if err != nil {
		return nil, err
	}

	events := make([]recomma.BotEventLog, 0, len(rows))
	for _, row := range rows {
		var evt tc.BotEvent
		if err := json.Unmarshal(row.Payload, &evt); err != nil {
			return nil, fmt.Errorf("decode bot event: %w", err)
		}
		oid, err := orderid.FromHexString(row.OrderID)
		if err != nil {
			return nil, fmt.Errorf("decode orderid: %w", err)
		}
		events = append(events, recomma.BotEventLog{
			RowID:      row.ID,
			BotEvent:   evt,
			BotID:      row.BotID,
			DealID:     row.DealID,
			BoteventID: row.BoteventID,
			OrderId:    *oid,
			CreatedAt:  time.UnixMilli(row.CreatedAtUtc).UTC(),
			ObservedAt: time.UnixMilli(row.ObservedAtUtc).UTC(),
		})
	}

	return events, nil
}

func (s *Storage) HasOrderId(ctx context.Context, oid orderid.OrderId) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.queries.HasThreeCommasOrderId(ctx, oid.Hex())
	if err != nil {
		return false, err
	}

	return exists == 1, nil
}

func (s *Storage) LoadThreeCommasBotEvent(ctx context.Context, oid orderid.OrderId) (*tc.BotEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := s.queries.FetchThreeCommasBotEvent(ctx, oid.Hex())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var order tc.BotEvent
	if err := json.Unmarshal(payload, &order); err != nil {
		return nil, err
	}

	return &order, nil
}

func (s *Storage) RecordHyperliquidOrderRequest(ctx context.Context, ident recomma.OrderIdentifier, req hyperliquid.CreateOrderRequest, boteventRowId int64) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	params := sqlcgen.UpsertHyperliquidCreateParams{
		VenueID:       ident.Venue(),
		Wallet:        ident.Wallet,
		OrderID:       oidHex,
		CreatePayload: raw,
		PayloadType:   hyperliquidCreatePayloadType,
		PayloadBlob:   raw,
		BoteventRowID: boteventRowId,
	}

	if err := s.queries.UpsertHyperliquidCreate(ctx, params); err != nil {
		return err
	}

	s.publishHyperliquidSubmissionLocked(ctx, ident, req, boteventRowId)
	return nil
}

func (s *Storage) AppendHyperliquidModify(ctx context.Context, ident recomma.OrderIdentifier, req hyperliquid.ModifyOrderRequest, boteventRowId int64) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	params := sqlcgen.AppendHyperliquidModifyParams{
		VenueID:       ident.Venue(),
		Wallet:        ident.Wallet,
		OrderID:       oidHex,
		ModifyPayload: raw,
		PayloadType:   hyperliquidModifyPayloadType,
		PayloadBlob:   raw,
		BoteventRowID: boteventRowId,
	}

	if err := s.queries.AppendHyperliquidModify(ctx, params); err != nil {
		return err
	}

	s.publishHyperliquidSubmissionLocked(ctx, ident, req, boteventRowId)
	return nil
}

func (s *Storage) RecordHyperliquidCancel(ctx context.Context, ident recomma.OrderIdentifier, req hyperliquid.CancelOrderRequestByCloid, boteventRowId int64) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	params := sqlcgen.UpsertHyperliquidCancelParams{
		VenueID:       ident.Venue(),
		Wallet:        ident.Wallet,
		OrderID:       oidHex,
		CancelPayload: raw,
		PayloadType:   hyperliquidCancelPayloadType,
		PayloadBlob:   raw,
		BoteventRowID: boteventRowId,
	}

	if err := s.queries.UpsertHyperliquidCancel(ctx, params); err != nil {
		return err
	}

	s.publishHyperliquidSubmissionLocked(ctx, ident, req, boteventRowId)
	return nil
}

func (s *Storage) RecordHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier, status hyperliquid.WsOrder) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	baseRecorded := time.Now().UTC().UnixMilli()
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		params := sqlcgen.InsertHyperliquidStatusParams{
			VenueID:       ident.Venue(),
			Wallet:        ident.Wallet,
			OrderID:       oidHex,
			PayloadType:   hyperliquidStatusPayloadType,
			PayloadBlob:   raw,
			RecordedAtUtc: baseRecorded + int64(attempt),
		}

		if err := s.queries.InsertHyperliquidStatus(ctx, params); err != nil {
			lastErr = err
			if isUniqueConstraintError(err) {
				continue
			}
			return err
		}

		lastErr = nil
		break
	}
	if lastErr != nil {
		return lastErr
	}

	copy := status
	s.publishStreamEventLocked(api.StreamEvent{
		Type:    api.HyperliquidStatus,
		OrderID: ident.OrderId,
		VenueID: ident.Venue(),
		Status:  &copy,
	})

	return nil
}

func (s *Storage) loadLatestHyperliquidStatusLocked(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	row, err := s.queries.FetchLatestHyperliquidStatus(ctx, sqlcgen.FetchLatestHyperliquidStatusParams{
		VenueID: ident.Venue(),
		Wallet:  ident.Wallet,
		OrderID: oidHex,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var decoded hyperliquid.WsOrder
	if err := json.Unmarshal(row.PayloadBlob, &decoded); err != nil {
		return nil, false, err
	}

	return &decoded, true, nil
}

func (s *Storage) publishHyperliquidSubmissionLocked(ctx context.Context, ident recomma.OrderIdentifier, submission interface{}, boteventRowID int64) {
	if s.stream == nil || submission == nil {
		return
	}

	var botEvent *tc.BotEvent
	if boteventRowID > 0 {
		if evt, err := s.fetchBotEventByRowIDLocked(ctx, boteventRowID); err == nil {
			botEvent = evt
		}
	}

	s.publishStreamEventLocked(api.StreamEvent{
		Type:       api.HyperliquidSubmission,
		OrderID:    ident.OrderId,
		VenueID:    ident.Venue(),
		BotEvent:   botEvent,
		Submission: submission,
	})
}

func (s *Storage) fetchBotEventByRowIDLocked(ctx context.Context, rowID int64) (*tc.BotEvent, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM threecommas_botevents WHERE id = ?", rowID).Scan(&payload)
	if err != nil {
		return nil, err
	}

	var evt tc.BotEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil, err
	}

	return &evt, nil
}

func (s *Storage) publishStreamEventLocked(evt api.StreamEvent) {
	if s.stream == nil {
		return
	}
	if evt.ObservedAt.IsZero() {
		evt.ObservedAt = time.Now().UTC()
	}
	s.stream.Publish(evt)
}

func (s *Storage) ListHyperliquidStatuses(ctx context.Context, ident recomma.OrderIdentifier) ([]hyperliquid.WsOrder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	rows, err := s.queries.ListHyperliquidStatuses(ctx, sqlcgen.ListHyperliquidStatusesParams{
		VenueID: ident.Venue(),
		Wallet:  ident.Wallet,
		OrderID: oidHex,
	})
	if err != nil {
		return nil, err
	}

	out := make([]hyperliquid.WsOrder, 0, len(rows))
	for _, row := range rows {
		var decoded hyperliquid.WsOrder
		if err := json.Unmarshal(row.PayloadBlob, &decoded); err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}

	return out, nil
}

// ListHyperliquidOrderIds returns the venue-aware identifiers we have submitted to Hyperliquid.
func (s *Storage) ListHyperliquidOrderIds(ctx context.Context) ([]recomma.OrderIdentifier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	venues, err := s.queries.ListVenues(ctx)
	if err != nil {
		return nil, err
	}

	if len(venues) == 0 {
		venues = append(venues, sqlcgen.ListVenuesRow{ID: string(defaultHyperliquidVenueID), Wallet: defaultHyperliquidWallet})
	}

	var identifiers []recomma.OrderIdentifier
	for _, venue := range venues {
		rows, err := s.queries.ListHyperliquidOrderIds(ctx, venue.ID)
		if err != nil {
			return nil, err
		}

		for _, row := range rows {
			if row.OrderID == "" {
				continue
			}
			oid, err := orderid.FromHexString(row.OrderID)
			if err != nil {
				return nil, fmt.Errorf("decode metadata %q: %w", row.OrderID, err)
			}
			identifiers = append(identifiers, recomma.NewOrderIdentifier(recomma.VenueID(row.VenueID), row.Wallet, *oid))
		}
	}

	return identifiers, nil
}

// ListOrderIdsForDeal returns the distinct order identifiers observed for a deal.
func (s *Storage) ListOrderIdsForDeal(ctx context.Context, dealID uint32) ([]orderid.OrderId, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.queries.GetOrderIdForDeal(ctx, int64(dealID))
	if err != nil {
		return nil, err
	}

	var list []orderid.OrderId
	for _, oidHex := range result {
		if oidHex == "" {
			continue
		}
		oid, err := orderid.FromHexString(oidHex)
		if err != nil {
			return nil, fmt.Errorf("decode orderid %q: %w", oidHex, err)
		}
		list = append(list, *oid)
	}

	return list, nil
}

// HyperliquidSafetyStatus captures the latest Hyperliquid state for a single
// averaging (safety) order fingerprint.
type HyperliquidSafetyStatus struct {
	OrderId          orderid.OrderId
	BotID            uint32
	DealID           uint32
	OrderType        string
	OrderPosition    int
	OrderSize        int
	HLStatus         hyperliquid.OrderStatusValue
	HLStatusRecorded time.Time
	HLEventTime      time.Time
}

func (s *Storage) ListLatestHyperliquidSafetyStatuses(ctx context.Context, dealID uint32) ([]HyperliquidSafetyStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListLatestHyperliquidSafetyStatuses(ctx, sqlcgen.ListLatestHyperliquidSafetyStatusesParams{
		VenueID: string(defaultHyperliquidVenueID),
		DealID:  int64(dealID),
		Wallet:  nil,
	})
	if err != nil {
		return nil, err
	}

	result := make([]HyperliquidSafetyStatus, 0, len(rows))
	for _, row := range rows {
		oid, err := orderid.FromHexString(row.OrderID)
		if err != nil {
			return nil, fmt.Errorf("decode orderid %q: %w", row.OrderID, err)
		}

		status := HyperliquidSafetyStatus{
			OrderId:          *oid,
			BotID:            uint32(row.BotID),
			DealID:           uint32(row.DealID),
			HLStatusRecorded: time.UnixMilli(row.RecordedAtUtc).UTC(),
			OrderType:        row.OrderType,
			OrderPosition:    int(row.OrderPosition),
			OrderSize:        int(row.OrderSize),
			HLStatus:         hyperliquid.OrderStatusValue(row.HlStatus),
			HLEventTime:      time.UnixMilli(row.HlStatusTimestamp).UTC(),
		}

		result = append(result, status)
	}

	return result, nil
}

// DealSafetiesFilled checks if all safety orders for the deal id have been filled.
// Only returns an error when an inconsistency has occured, e.g. ordersize is set to
// 2 but only 1 status is available
func (s *Storage) DealSafetiesFilled(ctx context.Context, dealID uint32) (bool, error) {
	statuses, err := s.ListLatestHyperliquidSafetyStatuses(ctx, dealID)
	if err != nil {
		return false, err
	}

	if len(statuses) == 0 {
		return false, fmt.Errorf("no statuses available for deal %d", dealID)
	}

	if statuses[0].OrderSize != len(statuses) {
		return false, fmt.Errorf("ordersize does not match amount of statuses: %d / %d", statuses[0].OrderSize, len(statuses))
	}

	if len(statuses) > 1 {
		// sizes should be equal across all
		var sizes []int
		for _, s := range statuses {
			sizes = append(sizes, s.OrderSize)
		}

		for _, s := range sizes {
			if sizes[0] != s {
				return false, fmt.Errorf("sizes should be equal across safety orders: %v", sizes)
			}
		}
	}

	for _, s := range statuses {
		if s.HLStatus != hyperliquid.OrderStatusValueFilled {
			// no need to return an error, false is a valid exit
			return false, nil
		}
	}

	return true, nil
}

func (s *Storage) LoadHyperliquidSubmission(ctx context.Context, ident recomma.OrderIdentifier) (recomma.Action, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	row, err := s.queries.FetchHyperliquidSubmission(ctx, sqlcgen.FetchHyperliquidSubmissionParams{
		VenueID: ident.Venue(),
		Wallet:  ident.Wallet,
		OrderID: oidHex,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return recomma.Action{}, false, nil
	}
	if err != nil {
		return recomma.Action{}, false, err
	}

	var action recomma.Action
	switch row.ActionKind {
	case "create":
		action.Type = recomma.ActionCreate
	case "modify":
		action.Type = recomma.ActionModify
	case "cancel":
		action.Type = recomma.ActionCancel
	default:
		action.Type = recomma.ActionNone
	}

	if len(row.CreatePayload) > 0 {
		var decoded hyperliquid.CreateOrderRequest
		if err := json.Unmarshal(row.CreatePayload, &decoded); err != nil {
			return recomma.Action{}, false, err
		}
		action.Create = &decoded
	}

	if len(row.ModifyPayloads) > 0 {
		var decoded []hyperliquid.ModifyOrderRequest
		if err := json.Unmarshal(row.ModifyPayloads, &decoded); err != nil {
			return recomma.Action{}, false, err
		}
		if len(decoded) > 0 {
			last := decoded[len(decoded)-1]
			action.Modify = &last
		}
	}

	if len(row.CancelPayload) > 0 {
		var decoded hyperliquid.CancelOrderRequestByCloid
		if err := json.Unmarshal(row.CancelPayload, &decoded); err != nil {
			return recomma.Action{}, false, err
		}
		action.Cancel = &decoded
	}

	if action.Type == recomma.ActionModify && action.Modify == nil {
		action.Type = recomma.ActionNone
		action.Reason = "modify history empty"
	}

	return action, true, nil
}

func (s *Storage) LoadHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLatestHyperliquidStatusLocked(ctx, ident)
}

func (s *Storage) LoadHyperliquidRequest(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.CreateOrderRequest, bool, error) {
	action, found, err := s.LoadHyperliquidSubmission(ctx, ident)
	return action.Create, found, err
}

func (s *Storage) RecordBot(ctx context.Context, bot tc.Bot, syncedAt time.Time) error {
	raw, err := json.Marshal(bot)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpsertBotParams{
		BotID:         int64(bot.Id),
		Payload:       raw,
		LastSyncedUtc: syncedAt.UTC().UnixMilli(),
	}

	return s.queries.UpsertBot(ctx, params)
}

func (s *Storage) LoadBot(ctx context.Context, botID int) (*tc.Bot, time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := s.queries.FetchBot(ctx, int64(botID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}

	var decoded tc.Bot
	if err := json.Unmarshal(row.Payload, &decoded); err != nil {
		return nil, time.Time{}, false, err
	}

	return &decoded, time.UnixMilli(row.LastSyncedUtc).UTC(), true, nil
}

func (s *Storage) TouchBot(ctx context.Context, botID int, syncedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpdateBotSyncParams{
		LastSyncedUtc: syncedAt.UTC().UnixMilli(),
		BotID:         int64(botID),
	}

	return s.queries.UpdateBotSync(ctx, params)
}

func (s *Storage) RecordThreeCommasDeal(ctx context.Context, deal tc.Deal) error {
	raw, err := json.Marshal(deal)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpsertDealParams{
		DealID:       int64(deal.Id),
		BotID:        int64(deal.BotId),
		CreatedAtUtc: deal.CreatedAt.UTC().UnixMilli(),
		UpdatedAtUtc: deal.UpdatedAt.UTC().UnixMilli(),
		Payload:      raw,
	}

	return s.queries.UpsertDeal(ctx, params)
}

func (s *Storage) LoadThreeCommasDeal(ctx context.Context, dealID int) (*tc.Deal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := s.queries.FetchDeal(ctx, int64(dealID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var decoded tc.Deal
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, false, err
	}

	return &decoded, true, nil
}

func (s *Storage) ListDealIDs(ctx context.Context) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.queries.ListDealIDs(ctx)
}

func (s *Storage) LoadTakeProfitForDeal(ctx context.Context, dealID uint32) (*orderid.OrderId, *tc.BotEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := s.queries.GetTPForDeal(ctx, int64(dealID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load take profit for deal %d: %w", dealID, err)
	}

	oid, err := orderid.FromHexString(row.OrderID)
	if err != nil {
		return nil, nil, fmt.Errorf("decode orderid %q: %w", row.OrderID, err)
	}

	var evt tc.BotEvent
	if err := json.Unmarshal(row.Payload, &evt); err != nil {
		return nil, nil, fmt.Errorf("decode take profit bot event: %w", err)
	}

	return oid, &evt, nil
}
