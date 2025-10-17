package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	hyperliquid "github.com/sonirico/go-hyperliquid"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/metadata"
	"github.com/terwey/recomma/recomma"
	"github.com/terwey/recomma/storage/sqlcgen"
)

//go:generate sqlc generate

//go:embed sqlc/schema.sql
var schemaDDL string

type Storage struct {
	db      *sql.DB
	queries *sqlcgen.Queries
	mu      sync.Mutex
}

func New(path string, logger *slog.Logger) (*Storage, error) {
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

	if logger != nil {
		wrapped := loggingDB{inner: db, logger: logger}
		return &Storage{
			db:      db,
			queries: sqlcgen.New(wrapped),
		}, nil
	}

	return &Storage{
		db:      db,
		queries: sqlcgen.New(db),
	}, nil
}

func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

// RecordThreeCommasBotEvent records the threecommas botevents that we acted upon
func (s *Storage) RecordThreeCommasBotEvent(md metadata.Metadata, order tc.BotEvent) (lastInsertId int64, err error) {
	raw, err := json.Marshal(order)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.InsertThreeCommasBotEventParams{
		Md:           md.String(),
		BotID:        int64(md.BotID),
		DealID:       int64(md.DealID),
		BoteventID:   int64(md.BotEventID),
		CreatedAtUtc: order.CreatedAt.UTC().UnixMilli(),
		Payload:      raw,
	}

	lastInsertId, err = s.queries.InsertThreeCommasBotEvent(ctx, params)
	if err != nil {
		return 0, err
	}

	return lastInsertId, nil
}

// RecordThreeCommasBotEvent records all threecommas botevents, acted upon or not
func (s *Storage) RecordThreeCommasBotEventLog(md metadata.Metadata, order tc.BotEvent) (lastInsertId int64, err error) {
	raw, err := json.Marshal(order)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.InsertThreeCommasBotEventLogParams{
		Md:           md.String(),
		BotID:        int64(md.BotID),
		DealID:       int64(md.DealID),
		BoteventID:   int64(md.BotEventID),
		CreatedAtUtc: order.CreatedAt.UTC().UnixMilli(),
		Payload:      raw,
	}

	lastInsertId, err = s.queries.InsertThreeCommasBotEventLog(ctx, params)
	if err != nil {
		return 0, err
	}

	return lastInsertId, nil
}

func (s *Storage) ListEventsForOrder(botID, dealID, botEventID uint32) ([]recomma.BotEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
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

func (s *Storage) HasMetadata(md metadata.Metadata) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	exists, err := s.queries.HasThreeCommasMetadata(ctx, md.String())
	if err != nil {
		return false, err
	}

	return exists == 1, nil
}

func (s *Storage) LoadThreeCommasBotEvent(md metadata.Metadata) (*tc.BotEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	payload, err := s.queries.FetchThreeCommasBotEvent(ctx, md.String())
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

func (s *Storage) RecordHyperliquidOrderRequest(md metadata.Metadata, req hyperliquid.CreateOrderRequest, boteventRowId int64) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.UpsertHyperliquidCreateParams{
		Md:            md.String(),
		CreatePayload: raw,
		BoteventRowID: boteventRowId,
	}

	return s.queries.UpsertHyperliquidCreate(ctx, params)
}

func (s *Storage) AppendHyperliquidModify(md metadata.Metadata, req hyperliquid.ModifyOrderRequest, boteventRowId int64) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.AppendHyperliquidModifyParams{
		Md:            md.String(),
		ModifyPayload: raw,
		BoteventRowID: boteventRowId,
	}

	return s.queries.AppendHyperliquidModify(ctx, params)
}

func (s *Storage) RecordHyperliquidCancel(md metadata.Metadata, req hyperliquid.CancelOrderRequestByCloid, boteventRowId int64) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.UpsertHyperliquidCancelParams{
		Md:            md.String(),
		CancelPayload: raw,
		BoteventRowID: boteventRowId,
	}

	return s.queries.UpsertHyperliquidCancel(ctx, params)
}

func (s *Storage) RecordHyperliquidStatus(md metadata.Metadata, status hyperliquid.WsOrder) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.InsertHyperliquidStatusParams{
		Md:            md.String(),
		Status:        raw,
		RecordedAtUtc: time.Now().UTC().UnixMilli(),
	}

	return s.queries.InsertHyperliquidStatus(ctx, params)
}

func (s *Storage) loadLatestHyperliquidStatusLocked(ctx context.Context, md metadata.Metadata) (*hyperliquid.WsOrder, bool, error) {
	payload, err := s.queries.FetchLatestHyperliquidStatus(ctx, md.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var decoded hyperliquid.WsOrder
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, false, err
	}

	return &decoded, true, nil
}

func (s *Storage) ListHyperliquidStatuses(md metadata.Metadata) ([]hyperliquid.WsOrder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	rows, err := s.queries.ListHyperliquidStatuses(ctx, md.String())
	if err != nil {
		return nil, err
	}

	out := make([]hyperliquid.WsOrder, 0, len(rows))
	for _, row := range rows {
		var decoded hyperliquid.WsOrder
		if err := json.Unmarshal(row.Status, &decoded); err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}

	return out, nil
}

// HyperliquidSafetyStatus captures the latest Hyperliquid state for a single
// averaging (safety) order fingerprint.
type HyperliquidSafetyStatus struct {
	Metadata         metadata.Metadata
	BotID            uint32
	DealID           uint32
	OrderType        string
	OrderPosition    int
	OrderSize        int
	HLStatus         hyperliquid.OrderStatusValue
	HLStatusRecorded time.Time
	HLEventTime      time.Time
}

func (s *Storage) ListLatestHyperliquidSafetyStatuses(dealID uint32) ([]HyperliquidSafetyStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	rows, err := s.queries.ListLatestHyperliquidSafetyStatuses(ctx, int64(dealID))
	if err != nil {
		return nil, err
	}

	result := make([]HyperliquidSafetyStatus, 0, len(rows))
	for _, row := range rows {
		md, err := metadata.FromHexString(row.Md)
		if err != nil {
			return nil, fmt.Errorf("decode metadata %q: %w", row.Md, err)
		}

		status := HyperliquidSafetyStatus{
			Metadata:         *md,
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
func (s *Storage) DealSafetiesFilled(dealID uint32) (bool, error) {
	statuses, err := s.ListLatestHyperliquidSafetyStatuses(dealID)
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

func (s *Storage) LoadHyperliquidSubmission(md metadata.Metadata) (recomma.Action, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	row, err := s.queries.FetchHyperliquidSubmission(ctx, md.String())
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

func (s *Storage) LoadHyperliquidStatus(md metadata.Metadata) (*hyperliquid.WsOrder, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLatestHyperliquidStatusLocked(context.Background(), md)
}

func (s *Storage) LoadHyperliquidRequest(md metadata.Metadata) (*hyperliquid.CreateOrderRequest, bool, error) {
	action, found, err := s.LoadHyperliquidSubmission(md)
	return action.Create, found, err
}

func (s *Storage) RecordBot(bot tc.Bot, syncedAt time.Time) error {
	raw, err := json.Marshal(bot)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.UpsertBotParams{
		BotID:         int64(bot.Id),
		Payload:       raw,
		LastSyncedUtc: syncedAt.UTC().UnixMilli(),
	}

	return s.queries.UpsertBot(ctx, params)
}

func (s *Storage) LoadBot(botID int) (*tc.Bot, time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
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

func (s *Storage) TouchBot(botID int, syncedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.UpdateBotSyncParams{
		LastSyncedUtc: syncedAt.UTC().UnixMilli(),
		BotID:         int64(botID),
	}

	return s.queries.UpdateBotSync(ctx, params)
}

func (s *Storage) RecordThreeCommasDeal(deal tc.Deal) error {
	raw, err := json.Marshal(deal)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	params := sqlcgen.UpsertDealParams{
		DealID:       int64(deal.Id),
		BotID:        int64(deal.BotId),
		CreatedAtUtc: deal.CreatedAt.UTC().UnixMilli(),
		UpdatedAtUtc: deal.UpdatedAt.UTC().UnixMilli(),
		Payload:      raw,
	}

	return s.queries.UpsertDeal(ctx, params)
}

func (s *Storage) LoadThreeCommasDeal(dealID int) (*tc.Deal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
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

func (s *Storage) LoadTakeProfitForDeal(dealID uint32) (*metadata.Metadata, *tc.BotEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	row, err := s.queries.GetTPForDeal(ctx, int64(dealID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load take profit for deal %d: %w", dealID, err)
	}

	md, err := metadata.FromHexString(row.Md)
	if err != nil {
		return nil, nil, fmt.Errorf("decode metadata %q: %w", row.Md, err)
	}

	var evt tc.BotEvent
	if err := json.Unmarshal(row.Payload, &evt); err != nil {
		return nil, nil, fmt.Errorf("decode take profit bot event: %w", err)
	}
	// backfill CreatedAt from the DB if the payload didn’t include it
	if evt.CreatedAt == nil && row.CreatedAtUtc != 0 {
		ts := time.UnixMilli(row.CreatedAtUtc).UTC()
		evt.CreatedAt = &ts
	}

	return md, &evt, nil
}
