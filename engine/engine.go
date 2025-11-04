package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/adapter"
	"github.com/recomma/recomma/engine/orderscaler"
	"github.com/recomma/recomma/filltracker"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"golang.org/x/sync/errgroup"
)

// WorkKey is comparable (just ints), safe to use as a queue key.
type WorkKey struct {
	DealID uint32
	BotID  uint32
}

type ThreeCommasAPI interface {
	ListBots(ctx context.Context, opts ...tc.ListBotsParamsOption) ([]tc.Bot, error)
	GetListOfDeals(ctx context.Context, opts ...tc.ListDealsParamsOption) ([]tc.Deal, error)
	GetDealForID(ctx context.Context, dealId tc.DealPathId) (*tc.Deal, error)
	// GetMarketOrdersForDeal(ctx context.Context, id tc.DealPathId) ([]tc.MarketOrder, error)
}

type Queue interface {
	Add(item WorkKey)
}

type Engine struct {
	client  ThreeCommasAPI
	store   *storage.Storage
	emitter recomma.Emitter
	logger  *slog.Logger
	tracker *filltracker.Service
	scaler  *orderscaler.Service
}

type EngineOption func(*Engine)

func WithStorage(store *storage.Storage) EngineOption {
	return func(h *Engine) {
		h.store = store
	}
}

func WithEmitter(emitter recomma.Emitter) EngineOption {
	return func(h *Engine) {
		h.emitter = emitter
	}
}

func WithFillTracker(tracker *filltracker.Service) EngineOption {
	return func(h *Engine) {
		h.tracker = tracker
	}
}

func WithOrderScaler(scaler *orderscaler.Service) EngineOption {
	return func(h *Engine) {
		h.scaler = scaler
	}
}

func NewEngine(client ThreeCommasAPI, opts ...EngineOption) *Engine {
	e := &Engine{
		client: client,
		logger: slog.Default().WithGroup("engine"),
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

func (e *Engine) ProduceActiveDeals(ctx context.Context, q Queue) error {
	// Producer: list enabled bots → list deals per bot → enqueue each deal (by comparable key)
	bots, err := e.client.ListBots(ctx, tc.WithScopeForListBots(tc.Enabled))
	if err != nil {
		// unwrap nice API error if present
		var apiErr *tc.APIError
		if errors.As(err, &apiErr) {
			return fmt.Errorf("list bots: %v %s", err, apiErr.ErrorPayload)
		}
		return fmt.Errorf("list bots: %v", err)
	}

	e.logger.Info("Checking for new deals from bots", slog.Int("bots", len(bots)))

	// Fetch deals per bot concurrently with a reasonable cap.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(32)

	for _, bot := range bots {
		b := bot // capture loop var
		g.Go(func() error {
			logger := e.logger.With("bot-id", b.Id)

			// default if we don't have it yet
			// the lastReq should be the `updated_at` from the deal
			// not a time later else we don't show that deal anymore
			lastReq := time.Now().Add(-time.Hour * 24)

			_, syncedAt, found, err := e.store.LoadBot(gctx, b.Id)
			if found && err == nil {
				lastReq = syncedAt
			}

			logger = logger.With("lastReq", lastReq)

			// bound each call; client honors context
			// we need a longer wait time cause we might get blocked by the rate limiter
			callCtx, cancel := context.WithTimeout(gctx, 90*time.Second)
			defer cancel()

			start := time.Now()
			deals, err := e.client.GetListOfDeals(callCtx, tc.WithBotIdForListDeals(b.Id),
				tc.WithFromForListDeals(lastReq),
			)
			if err != nil {
				logger.Error("list deals for bot", slog.String("error", err.Error()))
				return nil // keep other bots going
			}

			// NB: the reason we are doing a `min` here instead of the usual `max`
			// is because the 3commas API does not update the Deals updated_at
			// even if the events for it are updated

			var minUpdatedAt time.Time
			for _, d := range deals {
				if minUpdatedAt.IsZero() {
					minUpdatedAt = d.UpdatedAt
				}
				minUpdatedAt = time.UnixMilli(min(minUpdatedAt.UnixMilli(), d.UpdatedAt.UnixMilli()))
				q.Add(WorkKey{DealID: uint32(d.Id), BotID: uint32(d.BotId)})
				err := e.store.RecordThreeCommasDeal(gctx, d)
				if err != nil {
					logger.Warn("could not store deal", slog.String("error", err.Error()))
				}
			}

			latest := time.UnixMilli(min(lastReq.UnixMilli(), minUpdatedAt.UnixMilli()))

			// if no deals were found for the bot, let's just set the lastReq time to now-24h
			if len(deals) == 0 {
				// we can use the syncedAt time we had before
				if found {
					latest = syncedAt
				} else {
					latest = time.Now().Add(-time.Hour * 24)
				}
			}

			if err := e.store.RecordBot(gctx, b, b.UpdatedAt); err != nil {
				logger.Warn("could not record bot", slog.String("error", err.Error()))
			}

			e.store.TouchBot(gctx, b.Id, latest)
			if len(deals) > 0 {
				logger.Info("Enqueued Deals", slog.Int("deals", len(deals)), slog.Duration("elapsed", time.Since(start)))
			}
			return nil
		})
	}

	return g.Wait()
}

var ErrDealNotCached = errors.New("deal not cached")

func (e *Engine) HandleDeal(ctx context.Context, wi WorkKey) error {
	logger := e.logger.With("deal-id", wi.DealID).With("bot-id", wi.BotID)
	dealID := wi.DealID
	if dealID == 0 {
		return nil
	}

	deal, err := e.client.GetDealForID(ctx, tc.DealPathId(dealID))
	if err != nil {
		logger.Warn("error on getting deal", slog.String("error", err.Error()))
		return err
	}

	return e.processDeal(ctx, wi, deal.ToCurrency, deal.Events())
}

func (e *Engine) processDeal(ctx context.Context, wi WorkKey, currency string, events []tc.BotEvent) error {
	logger := e.logger.With("deal-id", wi.DealID).With("bot-id", wi.BotID)
	seen := make(map[uint32]orderid.OrderId)

	var fillSnapshot *filltracker.DealSnapshot
	if e.tracker != nil {
		if snapshot, ok := e.tracker.Snapshot(wi.DealID); ok {
			fillSnapshot = &snapshot
			logger.Debug("fill snapshot",
				slog.Float64("net_qty", snapshot.Position.NetQty),
				slog.Float64("avg_entry", snapshot.Position.AverageEntry),
				slog.Bool("all_buys_filled", snapshot.AllBuysFilled),
				slog.Float64("outstanding_buys", snapshot.OutstandingBuyQty),
				slog.Float64("outstanding_sells", snapshot.OutstandingSellQty),
			)
		} else {
			logger.Debug("fill snapshot unavailable")
		}
	}

	for _, event := range events {
		oid := orderid.OrderId{
			BotID:      wi.BotID,
			DealID:     wi.DealID,
			BotEventID: event.FingerprintAsID(),
		}
		// we want to store all incoming as a log
		_, err := e.store.RecordThreeCommasBotEventLog(ctx, oid, event)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("record bot event log: %w", err)
			}
		}

		if event.OrderType == tc.MarketOrderDealOrderTypeTakeProfit {
			if event.Action == tc.BotEventActionCancel || event.Action == tc.BotEventActionCancelled {
				// we ignore Take Profit cancellations, we cancel TP's ourselves based on the combined orders for the deal
				// TODO: figure out that the Take Profit SHOULD be cancelled because the price changed!!
				continue
			}
		}
		// we only want to act on PLACING, CANCEL and MODIFY
		// we assume here that when within the span of 15s (our poll time) a botevent went from PLACING to CANCEL we can ignore it
		if event.Action == tc.BotEventActionPlace || event.Action == tc.BotEventActionCancel || event.Action == tc.BotEventActionModify {
			lastInsertedId, err := e.store.RecordThreeCommasBotEvent(ctx, oid, event)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					// this means we saw it before, no error
					continue
				}
				return fmt.Errorf("record bot event: %w", err)
			}
			if lastInsertedId != 0 {
				seen[oid.BotEventID] = oid
			}
		}
	}

	for _, oid := range seen {
		orderLogger := logger.With("botevent-id", oid.BotEventID)

		storedIdents, err := e.store.ListSubmissionIdentifiersForOrder(ctx, oid)
		if err != nil {
			orderLogger.Warn("could not load submission identifiers", slog.String("error", err.Error()))
		}

		hasLocalOrder := len(storedIdents) > 0

		action, latestEvent, shouldEmit, err := e.reduceOrderEvents(ctx, currency, oid, hasLocalOrder, orderLogger)
		if err != nil {
			return fmt.Errorf("reduce order %d: %w", oid.BotEventID, err)
		}

		assignments, err := e.store.ListVenuesForBot(ctx, oid.BotID)
		if err != nil {
			orderLogger.Warn("could not load bot venues", slog.String("error", err.Error()))
			assignments = nil
		}

		missingTargets := missingAssignmentTargets(oid, assignments, storedIdents)
		replayMissingTargets := !shouldEmit && len(missingTargets) > 0 && latestEvent != nil && latestEvent.OrderType == tc.MarketOrderDealOrderTypeTakeProfit

		if !shouldEmit {
			if latestEvent == nil || latestEvent.Status != tc.Active || len(missingTargets) == 0 {
				continue
			}
			req := adapter.ToCreateOrderRequest(currency, *latestEvent, oid)
			orderLogger.Info("replaying create for venues missing submissions", slog.Int("venues", len(missingTargets)))
			action = recomma.Action{Type: recomma.ActionCreate, Create: &req}
			shouldEmit = true
		}
		if fillSnapshot != nil && latestEvent != nil {
			action, shouldEmit = e.adjustActionWithTracker(currency, oid, *latestEvent, action, fillSnapshot, orderLogger, replayMissingTargets)
			if !shouldEmit {
				continue
			}
		}

		targets := resolveOrderTargets(oid, assignments, storedIdents, action.Type)
		if len(targets) == 0 {
			orderLogger.Debug("no venue targets resolved; skipping emission")
			continue
		}

		var scaleResult *orderscaler.Result
		if latestEvent != nil {
			var err error
			action, scaleResult, shouldEmit, err = e.applyScaling(ctx, oid, latestEvent, action, orderLogger)
			if err != nil {
				return fmt.Errorf("scale order %d: %w", oid.BotEventID, err)
			}
			if !shouldEmit {
				continue
			}
		}
		if e.tracker != nil && scaleResult != nil {
			for _, ident := range targets {
				e.tracker.ApplyScaledOrder(ident, scaleResult.Size, scaleResult.Price)
			}
		}

		for _, ident := range targets {
			work := recomma.OrderWork{
				Identifier: ident,
				OrderId:    oid,
				Action:     cloneAction(action),
			}
			if latestEvent != nil {
				work.BotEvent = *latestEvent
			}
			if err := e.emitter.Emit(ctx, work); err != nil {
				e.logger.Warn("could not submit order", slog.Any("orderid", oid), slog.String("venue", ident.Venue()), slog.Any("action", work.Action), slog.String("error", err.Error()))
			}
		}
	}

	return nil
}

// reduceOrderEvents inspects every 3Commas snapshot we have stored for a single
// BotEvent fingerprint and projects it into "the next thing we still need to do
// on Hyperliquid". It returns the action plus a flag telling the caller whether
// anything needs to be emitted.
func (e *Engine) reduceOrderEvents(
	ctx context.Context,
	currency string,
	oid orderid.OrderId,
	hasLocalOrder bool,
	logger *slog.Logger,
) (recomma.Action, *recomma.BotEvent, bool, error) {

	// NB: we actually only care about the PLACING one's

	// rows are already sorted by CreatedAt ASC in ListEventsForOrder.
	events, err := e.store.ListEventsForOrder(ctx, oid.BotID, oid.DealID, oid.BotEventID)
	if err != nil {
		return recomma.Action{}, nil, false, fmt.Errorf("load event history: %w", err)
	}
	if len(events) == 0 {
		return recomma.Action{}, nil, false, nil
	}

	latest := events[len(events)-1]
	latestCopy := latest
	prev := previousDistinct(events)

	// Did we already create anything for this CLOID on Hyperliquid?
	// If Hyperliquid hasn’t seen this order yet, we pretend there is no “previous”
	// snapshot so BuildAction can only choose between Create or None.
	if !hasLocalOrder {
		prev = nil
	}

	action := adapter.BuildAction(currency, prev, latest, oid)

	switch action.Type {
	case recomma.ActionNone:
		// Nothing new to do (e.g. a filled order) – just keep the history.
		attrs := []any{slog.String("decision", "no-op")}
		if action.Reason != "" {
			attrs = append(attrs, slog.String("reason", action.Reason))
		}
		logger.Debug("no action required", attrs...)
		return action, &latestCopy, false, nil

	case recomma.ActionModify:
		// Guard against modify-before-create (initial backfill case).
		// At this point BuildAction only chose Modify because latest+prev
		// differ. If HL never saw the create we fall back to a create using
		// the freshest snapshot so the venue ends up with the right values.
		if !hasLocalOrder {
			req := adapter.ToCreateOrderRequest(currency, latest, oid)
			logger.Warn("modify requested before create; falling back", slog.Any("latest", latest))
			logger.Debug("emit create", slog.Any("request", req))
			return recomma.Action{Type: recomma.ActionCreate, Create: &req}, &latestCopy, true, nil
		}
		logger.Info("emit modify", slog.Any("latest", latest))
		return action, &latestCopy, true, nil

	case recomma.ActionCancel:
		// Only emit if HL still thinks the order exists. If we never managed to
		// create it locally there’s nothing to cancel, so we just persist the
		// 3C event and move on.
		if !hasLocalOrder {
			logger.Info("skip cancel: order never created locally", slog.Any("latest", latest))
			return recomma.Action{Type: recomma.ActionNone, Reason: "cancel skipped: order never created locally"}, &latestCopy, false, nil
		}
		logger.Debug("emit cancel", slog.Any("latest", latest))
		return action, &latestCopy, true, nil

	case recomma.ActionCreate:
		logger.Debug("emit create", slog.Any("latest", latest))
		return action, &latestCopy, true, nil

	default:
		return recomma.Action{}, nil, false, nil
	}
}

// previousDistinct walks backward and returns the most recent snapshot that
// differs materially from the final one; duplicates (same status/price/size)
// are ignored so we don’t emit a Modify for exact repeats.
func previousDistinct(events []recomma.BotEvent) *recomma.BotEvent {
	if len(events) < 2 {
		return nil
	}
	last := events[len(events)-1]
	for i := len(events) - 2; i >= 0; i-- {
		evt := events[i]
		if !sameSnapshot(&evt, &last) {
			return &evt
		}
	}
	return nil
}

func sameSnapshot(a, b *recomma.BotEvent) bool {
	return a.Status == b.Status &&
		a.Price == b.Price &&
		a.Size == b.Size &&
		a.OrderType == b.OrderType &&
		a.Type == b.Type &&
		a.IsMarket == b.IsMarket
}

const qtyTolerance = 1e-6

func nearlyEqual(a, b float64) bool {
	return math.Abs(a-b) <= qtyTolerance
}

func (e *Engine) adjustActionWithTracker(
	currency string,
	oid orderid.OrderId,
	latest recomma.BotEvent,
	action recomma.Action,
	snapshot *filltracker.DealSnapshot,
	logger *slog.Logger,
	skipExisting bool,
) (recomma.Action, bool) {
	if snapshot == nil {
		return action, true
	}
	if latest.OrderType != tc.MarketOrderDealOrderTypeTakeProfit {
		return action, true
	}

	desiredQty := snapshot.Position.NetQty
	if desiredQty <= qtyTolerance {
		logger.Info("skipping take profit placement: position flat",
			slog.Float64("net_qty", desiredQty),
			slog.Any("action_type", action.Type),
		)
		return recomma.Action{Type: recomma.ActionNone, Reason: "skip take-profit: position flat"}, false
	}

	active := snapshot.ActiveTakeProfit
	if !skipExisting && active != nil && active.ReduceOnly && nearlyEqual(active.RemainingQty, desiredQty) {
		logger.Debug("take profit already matches position",
			slog.Float64("desired_qty", desiredQty),
			slog.Float64("existing_qty", active.RemainingQty),
		)
		return recomma.Action{Type: recomma.ActionNone, Reason: "take-profit already matches position"}, false
	}

	switch action.Type {
	case recomma.ActionNone:
		req := adapter.ToCreateOrderRequest(currency, latest, oid)
		req.Size = desiredQty
		req.ReduceOnly = true
		logger.Info("placing take profit to match position",
			slog.Float64("desired_qty", desiredQty),
			slog.Float64("price", req.Price),
		)
		return recomma.Action{Type: recomma.ActionCreate, Create: &req}, true
	case recomma.ActionCreate:
		if action.Create == nil {
			req := adapter.ToCreateOrderRequest(currency, latest, oid)
			action.Create = &req
		}
		action.Create.Size = desiredQty
		action.Create.ReduceOnly = true
		logger.Info("creating take profit with tracked size",
			slog.Float64("desired_qty", desiredQty),
			slog.Float64("price", action.Create.Price),
		)
		return action, true
	case recomma.ActionModify:
		if action.Modify == nil {
			req := adapter.ToCreateOrderRequest(currency, latest, oid)
			req.Size = desiredQty
			req.ReduceOnly = true
			logger.Warn("modify without prior request; emitting create instead",
				slog.Float64("desired_qty", desiredQty),
			)
			return recomma.Action{Type: recomma.ActionCreate, Create: &req}, true
		}
		action.Modify.Order.Size = desiredQty
		action.Modify.Order.ReduceOnly = true
		logger.Info("modifying take profit to tracked size",
			slog.Float64("desired_qty", desiredQty),
			slog.Float64("price", action.Modify.Order.Price),
		)
		return action, true
	case recomma.ActionCancel:
		logger.Debug("take profit cancel requested", slog.Float64("net_qty", desiredQty))
		return action, true
	default:
		return action, true
	}
}

func (e *Engine) applyScaling(
	ctx context.Context,
	oid orderid.OrderId,
	latest *recomma.BotEvent,
	action recomma.Action,
	logger *slog.Logger,
) (recomma.Action, *orderscaler.Result, bool, error) {
	if e.scaler == nil || latest == nil {
		return action, nil, true, nil
	}

	switch action.Type {
	case recomma.ActionCreate:
		if action.Create == nil {
			return action, nil, true, nil
		}
		req := orderscaler.BuildRequest(oid, latest.BotEvent, *action.Create)
		result, err := e.scaler.Scale(ctx, req, action.Create)
		if err != nil {
			if errors.Is(err, orderscaler.ErrBelowMinimum) {
				reason := "scaled order below minimum"
				if result.Audit.SkipReason != nil {
					reason = *result.Audit.SkipReason
				}
				logger.Warn("skipping scaled order below venue minimum", slog.Float64("price", req.Price), slog.Float64("size", req.OriginalSize), slog.Float64("scaled_size", result.Size), slog.String("reason", reason))
				return recomma.Action{Type: recomma.ActionNone, Reason: reason}, nil, false, nil
			}
			return action, nil, false, err
		}
		latest.Size = result.Size
		latest.BotEvent.Size = result.Size
		if result.Price > 0 {
			latest.Price = result.Price
			latest.BotEvent.Price = result.Price
		}
		return action, &result, true, nil
	case recomma.ActionModify:
		if action.Modify == nil {
			return action, nil, true, nil
		}
		req := orderscaler.BuildRequest(oid, latest.BotEvent, action.Modify.Order)
		result, err := e.scaler.Scale(ctx, req, &action.Modify.Order)
		if err != nil {
			if errors.Is(err, orderscaler.ErrBelowMinimum) {
				reason := "scaled order below minimum"
				if result.Audit.SkipReason != nil {
					reason = *result.Audit.SkipReason
				}
				logger.Warn("skipping scaled modify below venue minimum", slog.Float64("price", req.Price), slog.Float64("size", req.OriginalSize), slog.Float64("scaled_size", result.Size), slog.String("reason", reason))
				return recomma.Action{Type: recomma.ActionNone, Reason: reason}, nil, false, nil
			}
			return action, nil, false, err
		}
		latest.Size = result.Size
		latest.BotEvent.Size = result.Size
		if result.Price > 0 {
			latest.Price = result.Price
			latest.BotEvent.Price = result.Price
		}
		return action, &result, true, nil
	default:
		return action, nil, true, nil
	}
}

func resolveOrderTargets(
	oid orderid.OrderId,
	assignments []storage.VenueAssignment,
	stored []recomma.OrderIdentifier,
	actionType recomma.ActionType,
) []recomma.OrderIdentifier {
	targets := make(map[recomma.OrderIdentifier]bool)
	for _, ident := range stored {
		targets[ident] = true
	}

	for _, assignment := range assignments {
		ident := recomma.NewOrderIdentifier(assignment.VenueID, assignment.Wallet, oid)
		if _, ok := targets[ident]; !ok {
			targets[ident] = false
		}
	}

	if len(targets) == 0 {
		ident := storage.DefaultHyperliquidIdentifier(oid)
		targets[ident] = len(stored) > 0
	}

	list := make([]recomma.OrderIdentifier, 0, len(targets))
	for ident, hadSubmission := range targets {
		switch actionType {
		case recomma.ActionCreate:
			if hadSubmission {
				continue
			}
		case recomma.ActionModify, recomma.ActionCancel:
			if !hadSubmission {
				continue
			}
		}
		list = append(list, ident)
	}

	slices.SortFunc(list, func(a, b recomma.OrderIdentifier) int {
		if cmp := strings.Compare(a.Venue(), b.Venue()); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a.Wallet, b.Wallet); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Hex(), b.Hex())
	})

	return list
}

func missingAssignmentTargets(
	oid orderid.OrderId,
	assignments []storage.VenueAssignment,
	stored []recomma.OrderIdentifier,
) []recomma.OrderIdentifier {
	if len(assignments) == 0 {
		return nil
	}

	seen := make(map[recomma.OrderIdentifier]struct{}, len(stored))
	for _, ident := range stored {
		seen[ident] = struct{}{}
	}

	missing := make([]recomma.OrderIdentifier, 0, len(assignments))
	for _, assignment := range assignments {
		ident := recomma.NewOrderIdentifier(assignment.VenueID, assignment.Wallet, oid)
		if _, ok := seen[ident]; ok {
			continue
		}
		missing = append(missing, ident)
	}

	slices.SortFunc(missing, compareIdentifiers)
	return missing
}

func compareIdentifiers(a, b recomma.OrderIdentifier) int {
	if cmp := strings.Compare(a.Venue(), b.Venue()); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(a.Wallet, b.Wallet); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.Hex(), b.Hex())
}

func cloneAction(action recomma.Action) recomma.Action {
	clone := action
	if action.Create != nil {
		req := *action.Create
		clone.Create = &req
	}
	if action.Modify != nil {
		req := *action.Modify
		clone.Modify = &req
	}
	if action.Cancel != nil {
		req := *action.Cancel
		clone.Cancel = &req
	}
	return clone
}
