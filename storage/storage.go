package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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
	primaryHyperliquidFlagKey   = "is_primary"

	hyperliquidCreatePayloadType = "hyperliquid.create.v1"
	hyperliquidModifyPayloadType = "hyperliquid.modify.v1"
	hyperliquidCancelPayloadType = "hyperliquid.cancel.v1"
	hyperliquidStatusPayloadType = "hyperliquid.status.v1"
)

var errPrimaryHyperliquidVenueNotFound = errors.New("storage: primary hyperliquid venue not found")

//go:embed sqlc/schema.sql
var schemaDDL string

type Storage struct {
	db      *sql.DB
	queries *sqlcgen.Queries
	mu      sync.Mutex
	stream  api.StreamPublisher
	logger  *slog.Logger
}

// VenueAssignment represents a venue configured for a bot.
type VenueAssignment struct {
	VenueID   recomma.VenueID
	Wallet    string
	IsPrimary bool
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
			s.logger = logger.WithGroup("storage")
		}
	}
}

func WithQueryLogger(logger *slog.Logger) StorageOption {
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
		logger:  slog.New(slog.DiscardHandler),
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
	logger := s.logger.WithGroup("Close")

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.Close(); err != nil {
		logger.Warn("close failed", slog.String("error", err.Error()))
		return err
	}

	logger.Debug("closed")
	return nil
}

func (s *Storage) ensureDefaultVenue(ctx context.Context) error {
	logger := s.logger.WithGroup("ensureDefaultVenue")

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.upsertDefaultVenueLocked(ctx, defaultHyperliquidWallet); err != nil {
		logger.Warn("ensure default venue failed", slog.String("error", err.Error()))
		return err
	}

	logger.Debug("default venue ensured")
	return nil
}

// EnsureDefaultVenueWallet updates the default Hyperliquid venue to reference the
// provided wallet address. Callers should invoke this once the runtime secrets
// are available so downstream lookups return identifiers that align with the
// live emitter/websocket clients.
func (s *Storage) EnsureDefaultVenueWallet(ctx context.Context, wallet string) error {
	logger := s.logger.WithGroup("EnsureDefaultVenueWallet").With(slog.String("wallet", wallet))

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.upsertDefaultVenueLocked(ctx, wallet); err != nil {
		logger.Warn("ensure default wallet failed", slog.String("error", err.Error()))
		return err
	}

	logger.Debug("default wallet ensured")
	return nil
}

// UpsertBotVenueAssignment associates a bot with a venue. Repeated calls update
// the primary flag while preserving the latest assignment timestamp.
func (s *Storage) UpsertBotVenueAssignment(ctx context.Context, botID uint32, venueID recomma.VenueID, isPrimary bool) error {
	logger := s.logger.WithGroup("UpsertBotVenueAssignment").With(
		slog.Uint64("bot_id", uint64(botID)),
		slog.String("venue_id", string(venueID)),
		slog.Bool("is_primary", isPrimary),
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpsertBotVenueAssignmentParams{
		BotID:   int64(botID),
		VenueID: string(venueID),
	}
	if isPrimary {
		params.IsPrimary = 1
	}

	if err := s.queries.UpsertBotVenueAssignment(ctx, params); err != nil {
		logger.Warn("upsert failed", slog.String("error", err.Error()))
		return err
	}

	logger.Debug("assignment updated")
	return nil
}

// HasPrimaryVenueAssignment reports whether a bot already has a stored primary
// venue assignment. Callers can use this to avoid overwriting operator
// preferences when syncing secrets-derived defaults.
func (s *Storage) HasPrimaryVenueAssignment(ctx context.Context, botID uint32) (bool, error) {
	logger := s.logger.WithGroup("HasPrimaryVenueAssignment").With(slog.Uint64("bot_id", uint64(botID)))

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.queries.GetPrimaryVenueForBot(ctx, int64(botID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			logger.Debug("no primary venue assignment")
			return false, nil
		}
		logger.Warn("query failed", slog.String("error", err.Error()))
		return false, err
	}
	logger.Debug("primary venue assignment exists")
	return true, nil
}

func (s *Storage) upsertDefaultVenueLocked(ctx context.Context, wallet string) error {
	if wallet == "" {
		wallet = defaultHyperliquidWallet
	}
	logger := s.logger.WithGroup("upsertDefaultVenueLocked").With(slog.String("wallet", wallet))

	// Check if a venue with this (type, wallet) already exists with a different ID.
	// This can happen if a user created a custom venue before the default was established.
	// We'll attempt the upsert and handle any constraint violations gracefully.
	existingVenue, err := s.queries.GetVenueByTypeAndWallet(ctx, sqlcgen.GetVenueByTypeAndWalletParams{
		Type:   defaultHyperliquidVenueType,
		Wallet: wallet,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		logger.Warn("lookup venue by wallet failed", slog.String("error", err.Error()))
		return err
	}
	hasConflictingVenue := err == nil && existingVenue.ID != string(defaultHyperliquidVenueID)
	if hasConflictingVenue {
		logger = logger.With(slog.String("conflicting_venue_id", existingVenue.ID))
	}

	currentWallet := defaultHyperliquidWallet
	row, err := s.queries.GetVenue(ctx, string(defaultHyperliquidVenueID))
	switch {
	case err == nil:
		if row.Wallet != "" {
			currentWallet = row.Wallet
		}
	case errors.Is(err, sql.ErrNoRows):
		// No existing venue, fall back to defaults.
	default:
		logger.Warn("load default venue failed", slog.String("error", err.Error()))
		return err
	}
	logger = logger.With(slog.String("current_wallet", currentWallet))

	flags := json.RawMessage(`{}`)
	params := sqlcgen.UpsertVenueParams{
		ID:          string(defaultHyperliquidVenueID),
		Type:        defaultHyperliquidVenueType,
		DisplayName: defaultHyperliquidName,
		Wallet:      wallet,
		Flags:       flags,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		logger.Warn("begin tx failed", slog.String("error", err.Error()))
		return err
	}

	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	qtx := s.queries.WithTx(tx)

	if err := qtx.UpsertVenue(ctx, params); err != nil {
		// If we have a conflicting venue and hit a unique constraint on (type, wallet),
		// this is expected. The existing venue serves the same purpose.
		if hasConflictingVenue && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			logger.Info("default venue not created: wallet already assigned to different venue")
			return nil
		}
		logger.Warn("upsert default venue failed", slog.String("error", err.Error()))
		return err
	}

	if currentWallet != wallet {
		if err := s.migrateDefaultVenueWalletLocked(ctx, qtx, currentWallet, wallet); err != nil {
			logger.Warn("migrate default wallet failed", slog.String("error", err.Error()))
			return fmt.Errorf("migrate default hyperliquid wallet: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		logger.Warn("commit failed", slog.String("error", err.Error()))
		return err
	}

	rollback = false
	logger.Debug("default venue upserted")
	return nil
}

func (s *Storage) migrateDefaultVenueWalletLocked(ctx context.Context, qtx *sqlcgen.Queries, fromWallet, toWallet string) error {
	logger := s.logger.WithGroup("migrateDefaultVenueWalletLocked").With(
		slog.String("from_wallet", fromWallet),
		slog.String("to_wallet", toWallet),
	)

	if fromWallet == toWallet {
		logger.Debug("wallets already aligned")
		return nil
	}

	cloneSubmissionsParams := sqlcgen.CloneHyperliquidSubmissionsToWalletParams{
		ToWallet:   toWallet,
		VenueID:    string(defaultHyperliquidVenueID),
		FromWallet: fromWallet,
	}
	if err := qtx.CloneHyperliquidSubmissionsToWallet(ctx, cloneSubmissionsParams); err != nil {
		logger.Warn("clone submissions failed", slog.String("error", err.Error()))
		return err
	}

	cloneStatusesParams := sqlcgen.CloneHyperliquidStatusesToWalletParams{
		ToWallet:   toWallet,
		VenueID:    string(defaultHyperliquidVenueID),
		FromWallet: fromWallet,
	}
	if err := qtx.CloneHyperliquidStatusesToWallet(ctx, cloneStatusesParams); err != nil {
		logger.Warn("clone statuses failed", slog.String("error", err.Error()))
		return err
	}

	cloneScaledOrdersParams := sqlcgen.CloneScaledOrdersToWalletParams{
		ToWallet:   toWallet,
		VenueID:    string(defaultHyperliquidVenueID),
		FromWallet: fromWallet,
	}
	if err := qtx.CloneScaledOrdersToWallet(ctx, cloneScaledOrdersParams); err != nil {
		logger.Warn("clone scaled orders failed", slog.String("error", err.Error()))
		return err
	}

	deleteStatusesParams := sqlcgen.DeleteHyperliquidStatusesForWalletParams{
		VenueID: string(defaultHyperliquidVenueID),
		Wallet:  fromWallet,
	}
	if err := qtx.DeleteHyperliquidStatusesForWallet(ctx, deleteStatusesParams); err != nil {
		logger.Warn("delete statuses failed", slog.String("error", err.Error()))
		return err
	}

	deleteSubmissionsParams := sqlcgen.DeleteHyperliquidSubmissionsForWalletParams{
		VenueID: string(defaultHyperliquidVenueID),
		Wallet:  fromWallet,
	}
	if err := qtx.DeleteHyperliquidSubmissionsForWallet(ctx, deleteSubmissionsParams); err != nil {
		logger.Warn("delete submissions failed", slog.String("error", err.Error()))
		return err
	}

	deleteScaledOrdersParams := sqlcgen.DeleteScaledOrdersForWalletParams{
		VenueID: string(defaultHyperliquidVenueID),
		Wallet:  fromWallet,
	}
	if err := qtx.DeleteScaledOrdersForWallet(ctx, deleteScaledOrdersParams); err != nil {
		logger.Warn("delete scaled orders failed", slog.String("error", err.Error()))
		return err
	}

	logger.Debug("wallet data migrated")
	return nil
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
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

func (s *Storage) defaultVenueAssignmentLocked(ctx context.Context) (VenueAssignment, error) {
	logger := s.logger.WithGroup("defaultVenueAssignmentLocked")

	row, err := s.queries.GetVenue(ctx, string(defaultHyperliquidVenueID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VenueAssignment{
				VenueID:   defaultHyperliquidVenueID,
				Wallet:    defaultHyperliquidWallet,
				IsPrimary: true,
			}, nil
		}
		logger.Warn("load default venue failed", slog.String("error", err.Error()))
		return VenueAssignment{}, err
	}

	wallet := row.Wallet
	if wallet == "" {
		wallet = defaultHyperliquidWallet
	}

	assignment := VenueAssignment{
		VenueID:   recomma.VenueID(row.ID),
		Wallet:    wallet,
		IsPrimary: true,
	}

	if wallet != defaultHyperliquidWallet {
		logger.Debug("resolved default assignment", slog.String("venue_id", string(assignment.VenueID)), slog.String("wallet", wallet))
		return assignment, nil
	}

	primary, err := s.findPrimaryHyperliquidVenueLocked(ctx)
	if err != nil {
		if errors.Is(err, errPrimaryHyperliquidVenueNotFound) {
			logger.Debug("no primary venue flagged; using default",
				slog.String("venue_id", string(assignment.VenueID)),
				slog.String("wallet", wallet),
			)
			return assignment, nil
		}
		logger.Warn("lookup primary venue failed", slog.String("error", err.Error()))
		return VenueAssignment{}, err
	}

	logger.Debug("resolved default assignment from primary",
		slog.String("venue_id", string(primary.VenueID)),
		slog.String("wallet", primary.Wallet),
	)
	return primary, nil
}

// ResolveDefaultAlias returns the venue assignment that should act as the
// default Hyperliquid alias. The assignment may point to the sentinel
// `hyperliquid:default` venue or any user-configured venue flagged as the
// primary Hyperliquid destination.
func (s *Storage) ResolveDefaultAlias(ctx context.Context) (VenueAssignment, error) {
	logger := s.logger.WithGroup("ResolveDefaultAlias")

	s.mu.Lock()
	defer s.mu.Unlock()

	assignment, err := s.defaultVenueAssignmentLocked(ctx)
	if err != nil {
		logger.Warn("resolve default alias failed", slog.String("error", err.Error()))
		return VenueAssignment{}, err
	}

	logger.Debug("default alias resolved",
		slog.String("venue_id", string(assignment.VenueID)),
		slog.String("wallet", assignment.Wallet),
	)
	return assignment, nil
}

func (s *Storage) findPrimaryHyperliquidVenueLocked(ctx context.Context) (VenueAssignment, error) {
	logger := s.logger.WithGroup("findPrimaryHyperliquidVenueLocked")

	rows, err := s.queries.ListVenues(ctx)
	if err != nil {
		logger.Warn("list venues failed", slog.String("error", err.Error()))
		return VenueAssignment{}, err
	}

	for _, row := range rows {
		if !strings.EqualFold(row.Type, defaultHyperliquidVenueType) {
			continue
		}

		flags, err := decodeVenueFlags(row.Flags)
		if err != nil {
			logger.Warn("decode venue flags failed", slog.String("venue_id", row.ID), slog.String("error", err.Error()))
			return VenueAssignment{}, fmt.Errorf("decode venue flags: %w", err)
		}

		if !isPrimaryVenueFlagged(flags) {
			continue
		}

		wallet := row.Wallet
		if wallet == "" {
			wallet = defaultHyperliquidWallet
		}

		assignment := VenueAssignment{
			VenueID:   recomma.VenueID(row.ID),
			Wallet:    wallet,
			IsPrimary: true,
		}
		logger.Debug("primary venue found", slog.String("venue_id", string(assignment.VenueID)), slog.String("wallet", wallet))
		return assignment, nil
	}

	logger.Debug("primary venue not found")
	return VenueAssignment{}, errPrimaryHyperliquidVenueNotFound
}

func decodeVenueFlags(raw []byte) (map[string]interface{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var flags map[string]interface{}
	if err := json.Unmarshal(raw, &flags); err != nil {
		return nil, err
	}
	if len(flags) == 0 {
		return nil, nil
	}
	return flags, nil
}

func isPrimaryVenueFlagged(flags map[string]interface{}) bool {
	if len(flags) == 0 {
		return false
	}

	raw, ok := flags[primaryHyperliquidFlagKey]
	if !ok {
		return false
	}

	switch v := raw.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		if v == "" {
			return false
		}
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
	}

	return false
}

// ListVenuesForBot returns the configured venue assignments for the given bot.
// If no explicit assignments exist, the default Hyperliquid venue is returned.
func (s *Storage) ListVenuesForBot(ctx context.Context, botID uint32) ([]VenueAssignment, error) {
	logger := s.logger.WithGroup("ListVenuesForBot").With(slog.Uint64("bot_id", uint64(botID)))

	s.mu.Lock()
	defer s.mu.Unlock()

	assignments, err := s.queries.ListBotVenueAssignments(ctx, int64(botID))
	if err != nil {
		logger.Warn("list assignments failed", slog.String("error", err.Error()))
		return nil, err
	}

	if len(assignments) == 0 {
		defaultAssignment, err := s.defaultVenueAssignmentLocked(ctx)
		if err != nil {
			logger.Warn("default assignment lookup failed", slog.String("error", err.Error()))
			return nil, err
		}
		logger.Debug("returning default assignment", slog.String("venue_id", string(defaultAssignment.VenueID)), slog.String("wallet", defaultAssignment.Wallet))
		return []VenueAssignment{defaultAssignment}, nil
	}

	venues := make([]VenueAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		row, err := s.queries.GetVenue(ctx, assignment.VenueID)
		if errors.Is(err, sql.ErrNoRows) {
			// Skip dangling assignments; callers will fall back to defaults
			// if no valid venues remain.
			continue
		}
		if err != nil {
			logger.Warn("load venue failed", slog.String("venue_id", assignment.VenueID), slog.String("error", err.Error()))
			return nil, err
		}

		wallet := row.Wallet
		if wallet == "" {
			wallet = defaultHyperliquidWallet
		}

		venues = append(venues, VenueAssignment{
			VenueID:   recomma.VenueID(assignment.VenueID),
			Wallet:    wallet,
			IsPrimary: assignment.IsPrimary != 0,
		})
	}

	if len(venues) == 0 {
		defaultAssignment, err := s.defaultVenueAssignmentLocked(ctx)
		if err != nil {
			logger.Warn("default assignment fallback failed", slog.String("error", err.Error()))
			return nil, err
		}
		venues = append(venues, defaultAssignment)
	}

	logger.Debug("resolved venues", slog.Int("count", len(venues)))
	return venues, nil
}

// ListSubmissionIdentifiersForOrder returns the venue-aware identifiers that
// have recorded submissions for the given order fingerprint.
func (s *Storage) ListSubmissionIdentifiersForOrder(ctx context.Context, oid orderid.OrderId) ([]recomma.OrderIdentifier, error) {
	logger := s.logger.WithGroup("ListSubmissionIdentifiersForOrder").With(slog.String("order_id", oid.Hex()))

	s.mu.Lock()
	defer s.mu.Unlock()

	hex := strings.ToLower(oid.Hex())
	seen := make(map[recomma.OrderIdentifier]struct{})
	var identifiers []recomma.OrderIdentifier

	const submissionsQuery = "SELECT venue_id, wallet FROM hyperliquid_submissions WHERE lower(order_id) = ?"
	const statusesQuery = "SELECT venue_id, wallet FROM hyperliquid_status_history WHERE lower(order_id) = ?"

	appendIdentifiers := func(rows *sql.Rows, source string) error {
		defer rows.Close()
		for rows.Next() {
			var venueID, wallet string
			if err := rows.Scan(&venueID, &wallet); err != nil {
				logger.Warn("scan identifier failed", slog.String("source", source), slog.String("error", err.Error()))
				return err
			}
			ident := ensureIdentifier(recomma.NewOrderIdentifier(recomma.VenueID(venueID), wallet, oid))
			if _, ok := seen[ident]; ok {
				continue
			}
			seen[ident] = struct{}{}
			identifiers = append(identifiers, ident)
		}
		if err := rows.Err(); err != nil {
			logger.Warn("iterate identifiers failed", slog.String("source", source), slog.String("error", err.Error()))
			return err
		}
		return nil
	}

	subRows, err := s.db.QueryContext(ctx, submissionsQuery, hex)
	if err != nil {
		logger.Warn("query submissions failed", slog.String("error", err.Error()))
		return nil, err
	}
	if err := appendIdentifiers(subRows, "submissions"); err != nil {
		return nil, err
	}

	statusRows, err := s.db.QueryContext(ctx, statusesQuery, hex)
	if err != nil {
		logger.Warn("query statuses failed", slog.String("error", err.Error()))
		return nil, err
	}
	if err := appendIdentifiers(statusRows, "statuses"); err != nil {
		return nil, err
	}

	logger.Debug("resolved identifiers", slog.Int("count", len(identifiers)))
	return identifiers, nil
}

// RecordThreeCommasBotEvent records the threecommas botevents that we acted upon
func (s *Storage) RecordThreeCommasBotEvent(ctx context.Context, oid orderid.OrderId, order tc.BotEvent) (lastInsertId int64, err error) {
	logger := s.logger.WithGroup("RecordThreeCommasBotEvent").With(
		slog.String("order_id", oid.Hex()),
		slog.Uint64("bot_id", uint64(oid.BotID)),
		slog.Uint64("deal_id", uint64(oid.DealID)),
	)

	raw, err := json.Marshal(order)
	if err != nil {
		logger.Warn("marshal failed", slog.String("error", err.Error()))
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
		logger.Warn("insert botevent failed", slog.String("error", err.Error()))
		return 0, err
	}

	if lastInsertId != 0 {
		logger.Debug("bot event recorded", slog.Int64("row_id", lastInsertId))
		clone := order
		ident := ensureIdentifier(recomma.NewOrderIdentifier("", "", oid))
		identCopy := ident
		s.publishStreamEventLocked(api.StreamEvent{
			Type:       api.ThreeCommasEvent,
			OrderID:    oid,
			Identifier: &identCopy,
			BotEvent:   &clone,
		})
	}

	return lastInsertId, nil
}

// RecordThreeCommasBotEvent records all threecommas botevents, acted upon or not
func (s *Storage) RecordThreeCommasBotEventLog(ctx context.Context, oid orderid.OrderId, order tc.BotEvent) (lastInsertId int64, err error) {
	logger := s.logger.WithGroup("RecordThreeCommasBotEventLog").With(
		slog.String("order_id", oid.Hex()),
		slog.Uint64("bot_id", uint64(oid.BotID)),
		slog.Uint64("deal_id", uint64(oid.DealID)),
	)

	raw, err := json.Marshal(order)
	if err != nil {
		logger.Warn("marshal failed", slog.String("error", err.Error()))
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
		logger.Warn("insert botevent log failed", slog.String("error", err.Error()))
		return 0, err
	}

	logger.Debug("bot event log recorded", slog.Int64("row_id", lastInsertId))
	return lastInsertId, nil
}

func (s *Storage) ListEventsForOrder(ctx context.Context, botID, dealID, botEventID uint32) ([]recomma.BotEvent, error) {
	logger := s.logger.WithGroup("ListEventsForOrder").With(
		slog.Uint64("bot_id", uint64(botID)),
		slog.Uint64("deal_id", uint64(dealID)),
		slog.Uint64("botevent_id", uint64(botEventID)),
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListThreeCommasBotEventsForOrder(ctx, sqlcgen.ListThreeCommasBotEventsForOrderParams{
		BotID:      int64(botID),
		DealID:     int64(dealID),
		BoteventID: int64(botEventID),
	})
	if err != nil {
		logger.Warn("list botevents failed", slog.String("error", err.Error()))
		return nil, err
	}

	events := make([]recomma.BotEvent, 0, len(rows))
	for _, row := range rows {
		var evt tc.BotEvent
		if err := json.Unmarshal(row.Payload, &evt); err != nil {
			logger.Warn("decode botevent failed", slog.Int64("row_id", row.ID), slog.String("error", err.Error()))
			return nil, fmt.Errorf("decode bot event: %w", err)
		}
		events = append(events, recomma.BotEvent{
			RowID:    row.ID,
			BotEvent: evt,
		})
	}

	logger.Debug("listed events", slog.Int("count", len(events)))
	return events, nil
}

// ListEventsLog returns all BotEvents recorded, irrelevant if we acted on it.
func (s *Storage) ListEventsLog(ctx context.Context) ([]recomma.BotEventLog, error) {
	logger := s.logger.WithGroup("ListEventsLog")

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListThreeCommasBotEventLogs(ctx)
	if err != nil {
		logger.Warn("list botevent logs failed", slog.String("error", err.Error()))
		return nil, err
	}

	events := make([]recomma.BotEventLog, 0, len(rows))
	for _, row := range rows {
		var evt tc.BotEvent
		if err := json.Unmarshal(row.Payload, &evt); err != nil {
			logger.Warn("decode botevent log failed", slog.Int64("row_id", row.ID), slog.String("error", err.Error()))
			return nil, fmt.Errorf("decode bot event: %w", err)
		}
		oid, err := orderid.FromHexString(row.OrderID)
		if err != nil {
			logger.Warn("decode order id failed", slog.Int64("row_id", row.ID), slog.String("error", err.Error()))
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

	logger.Debug("listed botevent logs", slog.Int("count", len(events)))
	return events, nil
}

func (s *Storage) HasOrderId(ctx context.Context, oid orderid.OrderId) (bool, error) {
	logger := s.logger.WithGroup("HasOrderId").With(slog.String("order_id", oid.Hex()))

	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.queries.HasThreeCommasOrderId(ctx, oid.Hex())
	if err != nil {
		logger.Warn("query failed", slog.String("error", err.Error()))
		return false, err
	}

	found := exists == 1
	logger.Debug("checked order id", slog.Bool("exists", found))
	return found, nil
}

func (s *Storage) LoadThreeCommasBotEvent(ctx context.Context, oid orderid.OrderId) (*tc.BotEvent, error) {
	logger := s.logger.WithGroup("LoadThreeCommasBotEvent").With(slog.String("order_id", oid.Hex()))

	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := s.queries.FetchThreeCommasBotEvent(ctx, oid.Hex())
	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug("bot event not found")
		return nil, nil
	}
	if err != nil {
		logger.Warn("fetch bot event failed", slog.String("error", err.Error()))
		return nil, err
	}

	var order tc.BotEvent
	if err := json.Unmarshal(payload, &order); err != nil {
		logger.Warn("decode bot event failed", slog.String("error", err.Error()))
		return nil, err
	}

	logger.Debug("bot event loaded")
	return &order, nil
}

func (s *Storage) RecordHyperliquidOrderRequest(ctx context.Context, ident recomma.OrderIdentifier, req hyperliquid.CreateOrderRequest, boteventRowId int64) error {
	logger := s.logger.WithGroup("RecordHyperliquidOrderRequest").With(slog.Any("ident", ident))
	logger.Debug("request", slog.Any("req", req))
	raw, err := json.Marshal(req)
	if err != nil {
		logger.Warn("could not marshal req", slog.String("error", err.Error()))
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
		logger.Warn("Could not upsert", slog.String("error", err.Error()))
		return err
	}

	s.publishHyperliquidSubmissionLocked(ctx, ident, req, boteventRowId)
	logger.Debug("order upserted")
	return nil
}

func (s *Storage) AppendHyperliquidModify(ctx context.Context, ident recomma.OrderIdentifier, req hyperliquid.ModifyOrderRequest, boteventRowId int64) error {
	logger := s.logger.WithGroup("AppendHyperliquidModify").With(slog.Any("ident", ident))
	logger.Debug("request", slog.Any("req", req))
	raw, err := json.Marshal(req)
	if err != nil {
		logger.Warn("could not marshal req", slog.String("error", err.Error()))
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
		logger.Warn("Could not upsert", slog.String("error", err.Error()))
		return err
	}

	s.publishHyperliquidSubmissionLocked(ctx, ident, req, boteventRowId)
	logger.Debug("order upserted")
	return nil
}

func (s *Storage) RecordHyperliquidCancel(ctx context.Context, ident recomma.OrderIdentifier, req hyperliquid.CancelOrderRequestByCloid, boteventRowId int64) error {
	logger := s.logger.WithGroup("RecordHyperliquidCancel").With(slog.Any("ident", ident))
	logger.Debug("request", slog.Any("req", req))

	raw, err := json.Marshal(req)
	if err != nil {
		logger.Warn("could not marshal req", slog.String("error", err.Error()))
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
		logger.Warn("could not upsert", slog.String("error", err.Error()))
		return err
	}

	s.publishHyperliquidSubmissionLocked(ctx, ident, req, boteventRowId)
	return nil
}

func (s *Storage) RecordHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier, status hyperliquid.WsOrder) error {
	if ident.VenueID == defaultHyperliquidVenueID {
		// we should not persist data for the default venue, it's an alias
		return nil
	}
	logger := s.logger.WithGroup("RecordHyperliquidStatus").With(slog.Any("ident", ident))
	logger.Debug("status", slog.Any("payload", status))

	raw, err := json.Marshal(status)
	if err != nil {
		logger.Warn("marshal status failed", slog.String("error", err.Error()))
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
				logger.Debug("unique constraint on status insert; retrying", slog.Int("attempt", attempt+1))
				continue
			}
			logger.Warn("insert status failed", slog.String("error", err.Error()))
			return err
		}

		lastErr = nil
		break
	}
	if lastErr != nil {
		logger.Warn("insert status exhausted retries", slog.String("error", lastErr.Error()))
		return lastErr
	}

	copy := status
	identCopy := ident
	s.publishStreamEventLocked(api.StreamEvent{
		Type:       api.HyperliquidStatus,
		OrderID:    ident.OrderId,
		Identifier: &identCopy,
		Status:     &copy,
	})

	logger.Debug("status recorded")
	return nil
}

func (s *Storage) loadLatestHyperliquidStatusLocked(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	logger := s.logger.WithGroup("loadLatestHyperliquidStatusLocked").With(slog.Any("ident", ident))

	ident = ensureIdentifier(ident)
	oidHex := ident.Hex()

	row, err := s.queries.FetchLatestHyperliquidStatus(ctx, sqlcgen.FetchLatestHyperliquidStatusParams{
		VenueID: ident.Venue(),
		Wallet:  ident.Wallet,
		OrderID: oidHex,
	})
	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug("status not found")
		return nil, false, nil
	}
	if err != nil {
		logger.Warn("fetch latest status failed", slog.String("error", err.Error()))
		return nil, false, err
	}

	var decoded hyperliquid.WsOrder
	if err := json.Unmarshal(row.PayloadBlob, &decoded); err != nil {
		logger.Warn("decode latest status failed", slog.String("error", err.Error()))
		return nil, false, err
	}

	logger.Debug("latest status loaded")
	return &decoded, true, nil
}

func (s *Storage) publishHyperliquidSubmissionLocked(ctx context.Context, ident recomma.OrderIdentifier, submission interface{}, boteventRowID int64) {
	if s.stream == nil || submission == nil {
		return
	}

	ident = ensureIdentifier(ident)
	var botEvent *tc.BotEvent
	if boteventRowID > 0 {
		if evt, err := s.fetchBotEventByRowIDLocked(ctx, boteventRowID); err == nil {
			botEvent = evt
		}
	}

	identCopy := ident
	s.publishStreamEventLocked(api.StreamEvent{
		Type:       api.HyperliquidSubmission,
		OrderID:    ident.OrderId,
		Identifier: &identCopy,
		BotEvent:   botEvent,
		Submission: submission,
	})
}

func (s *Storage) fetchBotEventByRowIDLocked(ctx context.Context, rowID int64) (*tc.BotEvent, error) {
	logger := s.logger.WithGroup("fetchBotEventByRowIDLocked").With(slog.Int64("row_id", rowID))

	var payload []byte
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM threecommas_botevents WHERE id = ?", rowID).Scan(&payload)
	if err != nil {
		logger.Warn("lookup bot event failed", slog.String("error", err.Error()))
		return nil, err
	}

	var evt tc.BotEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		logger.Warn("decode bot event failed", slog.String("error", err.Error()))
		return nil, err
	}

	logger.Debug("bot event loaded by row id")
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
	logger := s.logger.WithGroup("ListHyperliquidStatuses").With(slog.Any("ident", ident))

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
		logger.Warn("list statuses failed", slog.String("error", err.Error()))
		return nil, err
	}

	out := make([]hyperliquid.WsOrder, 0, len(rows))
	for _, row := range rows {
		var decoded hyperliquid.WsOrder
		if err := json.Unmarshal(row.PayloadBlob, &decoded); err != nil {
			logger.Warn("decode status failed", slog.String("error", err.Error()))
			return nil, err
		}
		out = append(out, decoded)
	}

	logger.Debug("listed statuses", slog.Int("count", len(out)))
	return out, nil
}

// ListHyperliquidOrderIds returns the venue-aware identifiers we have submitted to Hyperliquid.
func (s *Storage) ListHyperliquidOrderIds(ctx context.Context) ([]recomma.OrderIdentifier, error) {
	logger := s.logger.WithGroup("ListHyperliquidOrderIds")

	s.mu.Lock()
	defer s.mu.Unlock()

	venues, err := s.queries.ListVenues(ctx)
	if err != nil {
		logger.Warn("list venues failed", slog.String("error", err.Error()))
		return nil, err
	}

	if len(venues) == 0 {
		defaultAssignment, err := s.defaultVenueAssignmentLocked(ctx)
		if err != nil {
			logger.Warn("default assignment lookup failed", slog.String("error", err.Error()))
			return nil, err
		}
		venues = append(venues, sqlcgen.ListVenuesRow{ID: string(defaultAssignment.VenueID), Wallet: defaultAssignment.Wallet})
	}

	var identifiers []recomma.OrderIdentifier
	for _, venue := range venues {
		rows, err := s.queries.ListHyperliquidOrderIds(ctx, venue.ID)
		if err != nil {
			logger.Warn("list order ids failed", slog.String("venue_id", venue.ID), slog.String("error", err.Error()))
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

	logger.Debug("listed hyperliquid order ids", slog.Int("count", len(identifiers)))
	return identifiers, nil
}

// ListOrderIdsForDeal returns the distinct order identifiers observed for a deal.
func (s *Storage) ListOrderIdsForDeal(ctx context.Context, dealID uint32) ([]orderid.OrderId, error) {
	logger := s.logger.WithGroup("ListOrderIdsForDeal").With(slog.Uint64("deal_id", uint64(dealID)))

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.queries.GetOrderIdForDeal(ctx, int64(dealID))
	if err != nil {
		logger.Warn("get order ids for deal failed", slog.String("error", err.Error()))
		return nil, err
	}

	var list []orderid.OrderId
	for _, oidHex := range result {
		if oidHex == "" {
			continue
		}
		oid, err := orderid.FromHexString(oidHex)
		if err != nil {
			logger.Warn("decode order id failed", slog.String("order_id", oidHex), slog.String("error", err.Error()))
			return nil, fmt.Errorf("decode orderid %q: %w", oidHex, err)
		}
		list = append(list, *oid)
	}

	logger.Debug("listed order ids for deal", slog.Int("count", len(list)))
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

// TODO: make multi-venue aware
func (s *Storage) ListLatestHyperliquidSafetyStatuses(ctx context.Context, dealID uint32) ([]HyperliquidSafetyStatus, error) {
	logger := s.logger.WithGroup("ListLatestHyperliquidSafetyStatuses").With(slog.Uint64("deal_id", uint64(dealID)))

	assignment, err := s.ResolveDefaultAlias(ctx)
	if err != nil {
		logger.Warn("could not resolve default alias", slog.String("error", err.Error()))
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.queries.ListLatestHyperliquidSafetyStatuses(ctx, sqlcgen.ListLatestHyperliquidSafetyStatusesParams{
		VenueID: string(assignment.VenueID),
		DealID:  int64(dealID),
		Wallet:  nil,
	})
	if err != nil {
		logger.Warn("list safety statuses failed", slog.String("error", err.Error()))
		return nil, err
	}

	result := make([]HyperliquidSafetyStatus, 0, len(rows))
	for _, row := range rows {
		oid, err := orderid.FromHexString(row.OrderID)
		if err != nil {
			logger.Warn("decode order id failed", slog.String("order_id", row.OrderID), slog.String("error", err.Error()))
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

	logger.Debug("listed safety statuses", slog.Int("count", len(result)))
	return result, nil
}

// DealSafetiesFilled checks if all safety orders for the deal id have been filled.
// Only returns an error when an inconsistency has occured, e.g. ordersize is set to
// 2 but only 1 status is available
func (s *Storage) DealSafetiesFilled(ctx context.Context, dealID uint32) (bool, error) {
	logger := s.logger.WithGroup("DealSafetiesFilled").With(slog.Uint64("deal_id", uint64(dealID)))

	statuses, err := s.ListLatestHyperliquidSafetyStatuses(ctx, dealID)
	if err != nil {
		logger.Warn("list safety statuses failed", slog.String("error", err.Error()))
		return false, err
	}

	if len(statuses) == 0 {
		logger.Warn("no statuses available")
		return false, fmt.Errorf("no statuses available for deal %d", dealID)
	}

	if statuses[0].OrderSize != len(statuses) {
		logger.Warn("order size mismatch", slog.Int("order_size", statuses[0].OrderSize), slog.Int("status_count", len(statuses)))
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
				logger.Warn("inconsistent order sizes", slog.Any("sizes", sizes))
				return false, fmt.Errorf("sizes should be equal across safety orders: %v", sizes)
			}
		}
	}

	for _, s := range statuses {
		if s.HLStatus != hyperliquid.OrderStatusValueFilled {
			// no need to return an error, false is a valid exit
			logger.Debug("status not filled", slog.String("order_id", s.OrderId.Hex()), slog.String("status", string(s.HLStatus)))
			return false, nil
		}
	}

	logger.Debug("deal safeties filled")
	return true, nil
}

func (s *Storage) LoadHyperliquidSubmission(ctx context.Context, ident recomma.OrderIdentifier) (recomma.Action, bool, error) {
	logger := s.logger.WithGroup("LoadHyperliquidSubmission").With(slog.Any("ident", ident))

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
		logger.Debug("submission not found")
		return recomma.Action{}, false, nil
	}
	if err != nil {
		logger.Warn("fetch submission failed", slog.String("error", err.Error()))
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
			logger.Warn("decode create payload failed", slog.String("error", err.Error()))
			return recomma.Action{}, false, err
		}
		action.Create = decoded
	}

	if len(row.ModifyPayloads) > 0 {
		var decoded []hyperliquid.ModifyOrderRequest
		if err := json.Unmarshal(row.ModifyPayloads, &decoded); err != nil {
			logger.Warn("decode modify payload failed", slog.String("error", err.Error()))
			return recomma.Action{}, false, err
		}
		if len(decoded) > 0 {
			last := decoded[len(decoded)-1]
			action.Modify = last
		}
	}

	if len(row.CancelPayload) > 0 {
		var decoded hyperliquid.CancelOrderRequestByCloid
		if err := json.Unmarshal(row.CancelPayload, &decoded); err != nil {
			logger.Warn("decode cancel payload failed", slog.String("error", err.Error()))
			return recomma.Action{}, false, err
		}
		action.Cancel = decoded
	}

	if action.Type == recomma.ActionModify && len(row.ModifyPayloads) == 0 {
		action.Type = recomma.ActionNone
		action.Reason = "modify history empty"
	}

	logger.Debug("loaded submission", slog.Any("action_type", action.Type))
	return action, true, nil
}

func (s *Storage) LoadHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	logger := s.logger.WithGroup("LoadHyperliquidStatus").With(slog.Any("ident", ident))

	s.mu.Lock()
	defer s.mu.Unlock()
	status, found, err := s.loadLatestHyperliquidStatusLocked(ctx, ident)
	if err != nil {
		logger.Warn("load status failed", slog.String("error", err.Error()))
		return nil, false, err
	}
	if !found {
		logger.Debug("status not found")
		return nil, false, nil
	}
	logger.Debug("status loaded")
	return status, true, nil
}

func (s *Storage) LoadHyperliquidRequest(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.CreateOrderRequest, bool, error) {
	logger := s.logger.WithGroup("LoadHyperliquidRequest").With(slog.Any("ident", ident))

	action, found, err := s.LoadHyperliquidSubmission(ctx, ident)
	if err != nil {
		logger.Warn("load submission failed", slog.String("error", err.Error()))
		return nil, false, err
	}
	if !found {
		logger.Debug("submission not found")
		return nil, false, nil
	}
	logger.Debug("request loaded")
	return &action.Create, true, nil
}

func (s *Storage) RecordBot(ctx context.Context, bot tc.Bot, syncedAt time.Time) error {
	logger := s.logger.WithGroup("RecordBot").With(
		slog.Int("bot_id", bot.Id),
		slog.Time("synced_at", syncedAt),
	)

	raw, err := json.Marshal(bot)
	if err != nil {
		logger.Warn("marshal bot failed", slog.String("error", err.Error()))
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpsertBotParams{
		BotID:         int64(bot.Id),
		Payload:       raw,
		LastSyncedUtc: syncedAt.UTC().UnixMilli(),
	}

	if err := s.queries.UpsertBot(ctx, params); err != nil {
		logger.Warn("upsert bot failed", slog.String("error", err.Error()))
		return err
	}
	logger.Debug("bot recorded")
	return nil
}

func (s *Storage) LoadBot(ctx context.Context, botID int) (*tc.Bot, time.Time, bool, error) {
	logger := s.logger.WithGroup("LoadBot").With(slog.Int("bot_id", botID))

	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := s.queries.FetchBot(ctx, int64(botID))
	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug("bot not found")
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		logger.Warn("fetch bot failed", slog.String("error", err.Error()))
		return nil, time.Time{}, false, err
	}

	var decoded tc.Bot
	if err := json.Unmarshal(row.Payload, &decoded); err != nil {
		logger.Warn("decode bot failed", slog.String("error", err.Error()))
		return nil, time.Time{}, false, err
	}

	logger.Debug("bot loaded")
	return &decoded, time.UnixMilli(row.LastSyncedUtc).UTC(), true, nil
}

func (s *Storage) TouchBot(ctx context.Context, botID int, syncedAt time.Time) error {
	logger := s.logger.WithGroup("TouchBot").With(
		slog.Int("bot_id", botID),
		slog.Time("synced_at", syncedAt),
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	params := sqlcgen.UpdateBotSyncParams{
		LastSyncedUtc: syncedAt.UTC().UnixMilli(),
		BotID:         int64(botID),
	}

	if err := s.queries.UpdateBotSync(ctx, params); err != nil {
		logger.Warn("update bot sync failed", slog.String("error", err.Error()))
		return err
	}
	logger.Debug("bot sync touched")
	return nil
}

func (s *Storage) RecordThreeCommasDeal(ctx context.Context, deal tc.Deal) error {
	logger := s.logger.WithGroup("RecordThreeCommasDeal").With(
		slog.Int("deal_id", deal.Id),
		slog.Int("bot_id", deal.BotId),
	)

	raw, err := json.Marshal(deal)
	if err != nil {
		logger.Warn("marshal deal failed", slog.String("error", err.Error()))
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

	if err := s.queries.UpsertDeal(ctx, params); err != nil {
		logger.Warn("upsert deal failed", slog.String("error", err.Error()))
		return err
	}

	logger.Debug("deal recorded")
	return nil
}

func (s *Storage) LoadThreeCommasDeal(ctx context.Context, dealID int) (*tc.Deal, bool, error) {
	logger := s.logger.WithGroup("LoadThreeCommasDeal").With(slog.Int("deal_id", dealID))

	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := s.queries.FetchDeal(ctx, int64(dealID))
	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug("deal not found")
		return nil, false, nil
	}
	if err != nil {
		logger.Warn("fetch deal failed", slog.String("error", err.Error()))
		return nil, false, err
	}

	var decoded tc.Deal
	if err := json.Unmarshal(payload, &decoded); err != nil {
		logger.Warn("decode deal failed", slog.String("error", err.Error()))
		return nil, false, err
	}

	logger.Debug("deal loaded")
	return &decoded, true, nil
}

func (s *Storage) ListDealIDs(ctx context.Context) ([]int64, error) {
	logger := s.logger.WithGroup("ListDealIDs")

	s.mu.Lock()
	defer s.mu.Unlock()

	ids, err := s.queries.ListDealIDs(ctx)
	if err != nil {
		logger.Warn("list deal ids failed", slog.String("error", err.Error()))
		return nil, err
	}
	logger.Debug("listed deal ids", slog.Int("count", len(ids)))
	return ids, nil
}

func (s *Storage) LoadTakeProfitForDeal(ctx context.Context, dealID uint32) (*orderid.OrderId, *tc.BotEvent, error) {
	logger := s.logger.WithGroup("LoadTakeProfitForDeal").With(slog.Uint64("deal_id", uint64(dealID)))

	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := s.queries.GetTPForDeal(ctx, int64(dealID))
	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug("take profit not found")
		return nil, nil, sql.ErrNoRows
	}
	if err != nil {
		logger.Warn("query take profit failed", slog.String("error", err.Error()))
		return nil, nil, fmt.Errorf("load take profit for deal %d: %w", dealID, err)
	}

	oid, err := orderid.FromHexString(row.OrderID)
	if err != nil {
		logger.Warn("decode order id failed", slog.String("order_id", row.OrderID), slog.String("error", err.Error()))
		return nil, nil, fmt.Errorf("decode orderid %q: %w", row.OrderID, err)
	}

	var evt tc.BotEvent
	if err := json.Unmarshal(row.Payload, &evt); err != nil {
		logger.Warn("decode bot event failed", slog.String("error", err.Error()))
		return nil, nil, fmt.Errorf("decode take profit bot event: %w", err)
	}

	logger.Debug("take profit loaded")
	return oid, &evt, nil
}
