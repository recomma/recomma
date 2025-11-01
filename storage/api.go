// API specific storage requirements
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	hyperliquid "github.com/sonirico/go-hyperliquid"

	api "github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/storage/sqlcgen"
)

const cursorSeparator = ":"

func (s *Storage) ListBots(ctx context.Context, opts api.ListBotsOptions) ([]api.BotItem, *string, error) {
	if opts.Limit <= 0 {
		return nil, nil, fmt.Errorf("limit must be positive")
	}

	var cursorSynced, cursorBotID int64
	if opts.PageToken != "" {
		var err error
		cursorSynced, cursorBotID, err = decodeCursor(opts.PageToken)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid page token: %w", err)
		}
	}

	var (
		args       []any
		conditions []string
	)

	if opts.BotID != nil {
		conditions = append(conditions, "bot_id = ?")
		args = append(args, *opts.BotID)
	}
	if opts.UpdatedFrom != nil {
		conditions = append(conditions, "last_synced_utc >= ?")
		args = append(args, opts.UpdatedFrom.UTC().UnixMilli())
	}
	if opts.UpdatedTo != nil {
		conditions = append(conditions, "last_synced_utc <= ?")
		args = append(args, opts.UpdatedTo.UTC().UnixMilli())
	}
	if opts.PageToken != "" {
		conditions = append(conditions, "(last_synced_utc < ? OR (last_synced_utc = ? AND bot_id < ?))")
		args = append(args, cursorSynced, cursorSynced, cursorBotID)
	}

	var queryBuilder strings.Builder
	queryBuilder.WriteString("SELECT bot_id, payload, last_synced_utc FROM threecommas_bots")
	if len(conditions) > 0 {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(strings.Join(conditions, " AND "))
	}
	queryBuilder.WriteString(" ORDER BY last_synced_utc DESC, bot_id DESC LIMIT ?")
	args = append(args, opts.Limit+1)

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, queryBuilder.String(), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("query bots: %w", err)
	}
	defer rows.Close()

	type rawBot struct {
		id         int64
		payload    []byte
		lastSynced int64
	}
	raw := make([]rawBot, 0, opts.Limit+1)

	for rows.Next() {
		var rb rawBot
		if err := rows.Scan(&rb.id, &rb.payload, &rb.lastSynced); err != nil {
			return nil, nil, fmt.Errorf("scan bot row: %w", err)
		}
		raw = append(raw, rb)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate bot rows: %w", err)
	}

	var nextToken *string
	if len(raw) > opts.Limit {
		last := raw[opts.Limit]
		token := encodeCursor(last.lastSynced, last.id)
		nextToken = &token
		raw = raw[:opts.Limit]
	}

	items := make([]api.BotItem, 0, len(raw))
	for _, rb := range raw {
		var bot tc.Bot
		if err := json.Unmarshal(rb.payload, &bot); err != nil {
			return nil, nil, fmt.Errorf("decode bot payload: %w", err)
		}
		items = append(items, api.BotItem{
			Bot:          bot,
			LastSyncedAt: time.UnixMilli(rb.lastSynced).UTC(),
		})
	}

	return items, nextToken, nil
}

func (s *Storage) ListDeals(ctx context.Context, opts api.ListDealsOptions) ([]tc.Deal, *string, error) {
	if opts.Limit <= 0 {
		return nil, nil, fmt.Errorf("limit must be positive")
	}

	var cursorUpdatedAt, cursorDealID int64
	if opts.PageToken != "" {
		var err error
		cursorUpdatedAt, cursorDealID, err = decodeCursor(opts.PageToken)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid page token: %w", err)
		}
	}

	var (
		args       []any
		conditions []string
	)

	if opts.DealID != nil {
		conditions = append(conditions, "deal_id = ?")
		args = append(args, *opts.DealID)
	}
	if opts.BotID != nil {
		conditions = append(conditions, "bot_id = ?")
		args = append(args, *opts.BotID)
	}
	if opts.UpdatedFrom != nil {
		conditions = append(conditions, "updated_at_utc >= ?")
		args = append(args, opts.UpdatedFrom.UTC().UnixMilli())
	}
	if opts.UpdatedTo != nil {
		conditions = append(conditions, "updated_at_utc <= ?")
		args = append(args, opts.UpdatedTo.UTC().UnixMilli())
	}
	if opts.PageToken != "" {
		conditions = append(conditions, "(updated_at_utc < ? OR (updated_at_utc = ? AND deal_id < ?))")
		args = append(args, cursorUpdatedAt, cursorUpdatedAt, cursorDealID)
	}

	var queryBuilder strings.Builder
	queryBuilder.WriteString("SELECT deal_id, bot_id, created_at_utc, updated_at_utc, payload FROM threecommas_deals")
	if len(conditions) > 0 {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(strings.Join(conditions, " AND "))
	}
	queryBuilder.WriteString(" ORDER BY updated_at_utc DESC, deal_id DESC LIMIT ?")
	args = append(args, opts.Limit+1)

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, queryBuilder.String(), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("query deals: %w", err)
	}
	defer rows.Close()

	type rawDeal struct {
		dealID    int64
		updatedAt int64
		payload   []byte
	}
	raw := make([]rawDeal, 0, opts.Limit+1)

	for rows.Next() {
		var rd rawDeal
		var (
			botID   int64
			created int64
		)
		if err := rows.Scan(&rd.dealID, &botID, &created, &rd.updatedAt, &rd.payload); err != nil {
			return nil, nil, fmt.Errorf("scan deal row: %w", err)
		}
		raw = append(raw, rd)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate deal rows: %w", err)
	}

	var nextToken *string
	if len(raw) > opts.Limit {
		last := raw[opts.Limit]
		token := encodeCursor(last.updatedAt, last.dealID)
		nextToken = &token
		raw = raw[:opts.Limit]
	}

	items := make([]tc.Deal, 0, len(raw))
	for _, rd := range raw {
		var deal tc.Deal
		if err := json.Unmarshal(rd.payload, &deal); err != nil {
			return nil, nil, fmt.Errorf("decode deal payload: %w", err)
		}
		items = append(items, deal)
	}

	return items, nextToken, nil
}

func (s *Storage) ListOrderScalers(ctx context.Context, opts api.ListOrderScalersOptions) ([]api.OrderScalerConfigItem, *string, error) {
	if opts.Limit <= 0 {
		return nil, nil, fmt.Errorf("limit must be positive")
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

func (s *Storage) ListOrders(ctx context.Context, opts api.ListOrdersOptions) ([]api.OrderItem, *string, error) {
	if opts.Limit <= 0 {
		return nil, nil, fmt.Errorf("limit must be positive")
	}

	var cursorObservedAt, cursorID int64
	if opts.PageToken != "" {
		var err error
		cursorObservedAt, cursorID, err = decodeCursor(opts.PageToken)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid page token: %w", err)
		}
	}

	var (
		args       []any
		conditions []string
	)

	logFrom := int64(math.MinInt64)
	if opts.ObservedFrom != nil {
		logFrom = opts.ObservedFrom.UTC().UnixMilli()
		conditions = append(conditions, "observed_at_utc >= ?")
		args = append(args, logFrom)
	}

	logTo := int64(math.MaxInt64)
	if opts.ObservedTo != nil {
		logTo = opts.ObservedTo.UTC().UnixMilli()
		conditions = append(conditions, "observed_at_utc <= ?")
		args = append(args, logTo)
	}

	if opts.OrderIdPrefix != nil {
		if prefix := strings.TrimSpace(*opts.OrderIdPrefix); prefix != "" {
			conditions = append(conditions, "LOWER(order_id) LIKE ?")
			args = append(args, strings.ToLower(prefix)+"%")
		}
	}
	if opts.BotID != nil {
		conditions = append(conditions, "bot_id = ?")
		args = append(args, *opts.BotID)
	}
	if opts.DealID != nil {
		conditions = append(conditions, "deal_id = ?")
		args = append(args, *opts.DealID)
	}
	if opts.BotEventID != nil {
		conditions = append(conditions, "botevent_id = ?")
		args = append(args, *opts.BotEventID)
	}
	if opts.PageToken != "" {
		conditions = append(conditions, "(observed_at_utc < ? OR (observed_at_utc = ? AND id < ?))")
		args = append(args, cursorObservedAt, cursorObservedAt, cursorID)
	}

	// TODO: tear out this hardcoded query -> move to sqlc
	var queryBuilder strings.Builder
	queryBuilder.WriteString(`
SELECT id, order_id, bot_id, deal_id, botevent_id, created_at_utc, observed_at_utc, payload
FROM threecommas_botevents`)
	if len(conditions) > 0 {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(strings.Join(conditions, " AND "))
	}
	queryBuilder.WriteString(" ORDER BY observed_at_utc DESC, id DESC LIMIT ?")
	args = append(args, opts.Limit+1)

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, queryBuilder.String(), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()

	type rawOrder struct {
		id         int64
		oid        string
		botID      int64
		dealID     int64
		botEventID int64
		createdAt  int64
		observedAt int64
		payload    []byte
	}
	raw := make([]rawOrder, 0, opts.Limit+1)

	for rows.Next() {
		var ro rawOrder
		if err := rows.Scan(
			&ro.id,
			&ro.oid,
			&ro.botID,
			&ro.dealID,
			&ro.botEventID,
			&ro.createdAt,
			&ro.observedAt,
			&ro.payload,
		); err != nil {
			return nil, nil, fmt.Errorf("scan order row: %w", err)
		}
		raw = append(raw, ro)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate order rows: %w", err)
	}

	if len(raw) == 0 {
		return nil, nil, nil
	}

	var nextToken *string
	if len(raw) > opts.Limit {
		last := raw[opts.Limit]
		token := encodeCursor(last.observedAt, last.id)
		nextToken = &token
		raw = raw[:opts.Limit]
	}

	items := make([]api.OrderItem, 0, len(raw))
	byOrderId := make(map[string][]int, len(raw))

	for _, ro := range raw {
		oid, err := orderid.FromHexString(ro.oid)
		if err != nil {
			return nil, nil, fmt.Errorf("decode orderid %q: %w", ro.oid, err)
		}

		var event tc.BotEvent
		if err := json.Unmarshal(ro.payload, &event); err != nil {
			return nil, nil, fmt.Errorf("decode bot event payload: %w", err)
		}
		eventCopy := event

		item := api.OrderItem{
			OrderId:    *oid,
			ObservedAt: time.UnixMilli(ro.observedAt).UTC(),
			BotEvent:   &eventCopy,
		}
		items = append(items, item)
		idx := len(items) - 1
		byOrderId[ro.oid] = append(byOrderId[ro.oid], idx)
	}

	for oidHex, indexes := range byOrderId {
		if len(indexes) == 0 {
			continue
		}
		oidCopy := items[indexes[0]].OrderId

		var (
			actionKind     sql.NullString
			createPayload  []byte
			modifyPayloads []byte
			cancelPayload  []byte
			updatedAtUTC   sql.NullInt64
		)

		// TODO: remove hardcoded query!

		err := s.db.QueryRowContext(ctx, `
SELECT action_kind,
       create_payload,
       modify_payloads,
       cancel_payload,
       updated_at_utc
FROM hyperliquid_submissions
WHERE order_id = ?
`, oidHex).Scan(&actionKind, &createPayload, &modifyPayloads, &cancelPayload, &updatedAtUTC)

		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("fetch submission for %s: %w", oidHex, err)
		}

		var submission interface{}
		if err == nil && actionKind.Valid {
			switch actionKind.String {
			case "create":
				if len(createPayload) > 0 {
					var decoded hyperliquid.CreateOrderRequest
					if err := json.Unmarshal(createPayload, &decoded); err != nil {
						return nil, nil, fmt.Errorf("decode create submission for %s: %w", oidHex, err)
					}
					submission = &decoded
				}
			case "modify":
				if len(modifyPayloads) > 0 {
					var decoded []hyperliquid.ModifyOrderRequest
					if err := json.Unmarshal(modifyPayloads, &decoded); err != nil {
						return nil, nil, fmt.Errorf("decode modify submission for %s: %w", oidHex, err)
					}
					if len(decoded) > 0 {
						last := decoded[len(decoded)-1]
						submission = &last
					}
				}
			case "cancel":
				if len(cancelPayload) > 0 {
					var decoded hyperliquid.CancelOrderRequestByCloid
					if err := json.Unmarshal(cancelPayload, &decoded); err != nil {
						return nil, nil, fmt.Errorf("decode cancel submission for %s: %w", oidHex, err)
					}
					submission = &decoded
				}
			}
		}
		if submission != nil {
			for _, idx := range indexes {
				items[idx].LatestSubmission = submission
			}
		}

		statusRow, err := s.queries.FetchLatestHyperliquidStatus(ctx, oidHex)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("fetch latest status for %s: %w", oidHex, err)
		}
		var latestStatus *hyperliquid.WsOrder
		if err == nil && len(statusRow) > 0 {
			var decoded hyperliquid.WsOrder
			if err := json.Unmarshal(statusRow, &decoded); err != nil {
				return nil, nil, fmt.Errorf("decode latest status for %s: %w", oidHex, err)
			}
			statusCopy := decoded
			latestStatus = &statusCopy
		}

		if opts.IncludeLog {
			logRows, err := s.queries.ListThreeCommasBotEventLogsForOrderId(ctx, sqlcgen.ListThreeCommasBotEventLogsForOrderIdParams{
				OrderID:      oidHex,
				ObservedFrom: logFrom,
				ObservedTo:   logTo,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("list bot event log for %s: %w", oidHex, err)
			}

			statusRows, err := s.queries.ListHyperliquidStatusesForOrderId(ctx, sqlcgen.ListHyperliquidStatusesForOrderIdParams{
				OrderID:      oidHex,
				ObservedFrom: logFrom,
				ObservedTo:   logTo,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("list status history for %s: %w", oidHex, err)
			}

			auditRows, err := s.queries.ListScaledOrderAuditsForOrderId(ctx, sqlcgen.ListScaledOrderAuditsForOrderIdParams{
				OrderID:      oidHex,
				ObservedFrom: logFrom,
				ObservedTo:   logTo,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("list scaled order audits for %s: %w", oidHex, err)
			}

			entries := make([]api.OrderLogItem, 0, len(logRows)+len(statusRows)+len(auditRows)+1)

			for _, logRow := range logRows {
				var evt tc.BotEvent
				if err := json.Unmarshal(logRow.Payload, &evt); err != nil {
					return nil, nil, fmt.Errorf("decode log bot event for %s: %w", oidHex, err)
				}
				evtCopy := evt
				entries = append(entries, api.OrderLogItem{
					Type:       api.ThreeCommasEvent,
					OrderId:    oidCopy,
					ObservedAt: time.UnixMilli(logRow.ObservedAtUtc).UTC(),
					BotEvent:   &evtCopy,
				})
			}

			for _, statusRow := range statusRows {
				var decoded hyperliquid.WsOrder
				if err := json.Unmarshal(statusRow.Status, &decoded); err != nil {
					return nil, nil, fmt.Errorf("decode status history for %s: %w", oidHex, err)
				}
				statusCopy := decoded
				entries = append(entries, api.OrderLogItem{
					Type:       api.HyperliquidStatus,
					OrderId:    oidCopy,
					ObservedAt: time.UnixMilli(statusRow.RecordedAtUtc).UTC(),
					Status:     &statusCopy,
				})
				latestStatus = &statusCopy
			}

			for _, auditRow := range auditRows {
				audit, err := convertScaledOrder(auditRow)
				if err != nil {
					return nil, nil, fmt.Errorf("decode scaled order audit for %s: %w", oidHex, err)
				}
				actor := audit.MultiplierUpdatedBy
				entries = append(entries, api.OrderLogItem{
					Type:        api.ScaledOrderAuditEntry,
					OrderId:     oidCopy,
					ObservedAt:  audit.CreatedAt,
					ScaledAudit: toAPIScaledOrderAudit(audit),
					Actor:       &actor,
				})
			}

			if submission != nil && updatedAtUTC.Valid {
				entries = append(entries, api.OrderLogItem{
					Type:       api.HyperliquidSubmission,
					OrderId:    oidCopy,
					ObservedAt: time.UnixMilli(updatedAtUTC.Int64).UTC(),
					Submission: submission,
				})
			}

			sort.Slice(entries, func(i, j int) bool {
				return entries[i].ObservedAt.Before(entries[j].ObservedAt)
			})

			for _, idx := range indexes {
				copied := make([]api.OrderLogItem, len(entries))
				copy(copied, entries)
				items[idx].LogEntries = copied
			}
		}

		if latestStatus != nil {
			for _, idx := range indexes {
				statusCopy := *latestStatus
				items[idx].LatestStatus = &statusCopy
			}
		}
	}

	return items, nextToken, nil
}

func encodeCursor(primary, secondary int64) string {
	return fmt.Sprintf("%d%s%d", primary, cursorSeparator, secondary)
}

func decodeCursor(token string) (int64, int64, error) {
	parts := strings.Split(token, cursorSeparator)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("cursor must contain two parts")
	}

	primary, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse primary cursor: %w", err)
	}

	secondary, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse secondary cursor: %w", err)
	}

	return primary, secondary, nil
}
