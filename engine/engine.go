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
	"github.com/recomma/recomma/ratelimit"
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
	client             ThreeCommasAPI
	store              *storage.Storage
	emitter            recomma.Emitter
	logger             *slog.Logger
	tracker            *filltracker.Service
	scaler             *orderscaler.Service
	limiter            *ratelimit.Limiter
	produceConcurrency int
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

func WithRateLimiter(limiter *ratelimit.Limiter) EngineOption {
	return func(h *Engine) {
		h.limiter = limiter
	}
}

func WithProduceConcurrency(concurrency int) EngineOption {
	return func(h *Engine) {
		h.produceConcurrency = concurrency
	}
}

func NewEngine(client ThreeCommasAPI, opts ...EngineOption) *Engine {
	e := &Engine{
		client:             client,
		logger:             slog.Default().WithGroup("engine"),
		produceConcurrency: 32, // Default to current behavior
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

type emissionPlan struct {
	action       recomma.Action
	targets      []recomma.OrderIdentifier
	latest       *recomma.BotEvent
	skipExisting bool
}

func (e *Engine) ProduceActiveDeals(ctx context.Context, q Queue) error {
	// Rate limiting workflow: Reserve → Consume → AdjustDown → SignalComplete → Release
	workflowID := "produce:all-bots"

	// If rate limiter is configured, use the reservation pattern
	if e.limiter != nil {
		// Get current stats to determine pessimistic reservation
		_, limit, _, _ := e.limiter.Stats()

		// Reserve entire quota pessimistically (we don't know how many bots yet)
		if err := e.limiter.Reserve(ctx, workflowID, limit); err != nil {
			return fmt.Errorf("rate limit reserve: %w", err)
		}

		// Ensure we release the reservation even if there's an error
		defer e.limiter.Release(workflowID)
		defer e.limiter.SignalComplete(workflowID)
	}

	// Add workflow ID to context for consumption tracking
	ctx = ratelimit.WithWorkflowID(ctx, workflowID)

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

	// Now we know how many bots we have, adjust down the reservation
	// We need: 1 (ListBots) + len(bots) (GetListOfDeals per bot)
	if e.limiter != nil {
		neededSlots := 1 + len(bots)
		if err := e.limiter.AdjustDown(workflowID, neededSlots); err != nil {
			e.logger.Warn("rate limit adjust down failed", slog.String("error", err.Error()))
		}
	}

	// Fetch deals per bot concurrently with tier-specific concurrency cap
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(e.produceConcurrency)

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

	// Rate limiting workflow: Reserve → Consume → AdjustDown → SignalComplete → Release
	workflowID := fmt.Sprintf("deal:%d:%d", wi.DealID, wi.BotID)

	// If rate limiter is configured, use the reservation pattern
	if e.limiter != nil {
		// Reserve conservatively: 1 for GetDealForID + 1 buffer for potential future calls
		if err := e.limiter.Reserve(ctx, workflowID, 2); err != nil {
			return fmt.Errorf("rate limit reserve: %w", err)
		}

		// Ensure we release the reservation even if there's an error
		defer e.limiter.Release(workflowID)
		defer e.limiter.SignalComplete(workflowID)
	}

	// Add workflow ID to context for consumption tracking
	ctx = ratelimit.WithWorkflowID(ctx, workflowID)

	deal, err := e.client.GetDealForID(ctx, tc.DealPathId(dealID))
	if err != nil {
		logger.Warn("error on getting deal", slog.String("error", err.Error()))
		return err
	}

	// Adjust down to actual consumption (only needed 1 slot)
	if e.limiter != nil {
		if err := e.limiter.AdjustDown(workflowID, 1); err != nil {
			logger.Warn("rate limit adjust down failed", slog.String("error", err.Error()))
		}
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

		assignments, err := e.store.ListVenuesForBot(ctx, oid.BotID)
		if err != nil {
			orderLogger.Warn("could not load bot venues", slog.String("error", err.Error()))
			assignments = nil
		}

		storedIdents, err := e.store.ListSubmissionIdentifiersForOrder(ctx, oid)
		if err != nil {
			orderLogger.Warn("could not load submission identifiers", slog.String("error", err.Error()))
		}
		storedIdents = normalizeSubmissionIdentifiers(assignments, storedIdents)

		hasLocalOrder := len(storedIdents) > 0

		action, latestEvent, shouldEmit, err := e.reduceOrderEvents(ctx, currency, oid, hasLocalOrder, orderLogger)
		if err != nil {
			return fmt.Errorf("reduce order %d: %w", oid.BotEventID, err)
		}

		// Replay logic: if a venue was added after a deal started and this order is still active,
		// we'll replay the creation to the new venues. This handles most cases where venues are
		// added mid-deal. Edge case: If no new events arrive for an order after a venue is added,
		// this replay won't trigger until the next event. For production use, consider a periodic
		// reconciliation pass to catch such cases (similar to take-profit reconciliation).
		missingTargets := missingAssignmentTargets(oid, assignments, storedIdents)
		replayAvailable := hasLocalOrder && len(missingTargets) > 0 && latestEvent != nil && latestEvent.Status == tc.Active

		forceSkipExisting := replayAvailable && action.Type == recomma.ActionModify
		if forceSkipExisting {
			orderLogger.Debug("skipping modify emission for existing venues; replay pending")
			shouldEmit = false
		}

		if fillSnapshot != nil && latestEvent != nil {
			adjusted, emit := e.adjustActionWithTracker(currency, oid, *latestEvent, action, fillSnapshot, orderLogger, forceSkipExisting)
			action = adjusted
			shouldEmit = emit
			if forceSkipExisting {
				shouldEmit = false
			}
		}

		var emissions []emissionPlan

		if shouldEmit {
			var defaultAssignment *storage.VenueAssignment
			if len(assignments) == 0 && len(storedIdents) == 0 {
				assignment, err := e.store.ResolveDefaultAlias(ctx)
				if err != nil {
					orderLogger.Warn("resolve default alias failed", slog.String("error", err.Error()))
				} else {
					defaultAssignment = &assignment
				}
			}

			targets := resolveOrderTargets(oid, assignments, storedIdents, action.Type, defaultAssignment)
			if len(targets) == 0 && (action.Type == recomma.ActionModify || action.Type == recomma.ActionCancel) {
				orderLogger.Warn("no submission targets for action",
					slog.String("action_type", action.Type.String()),
					slog.Int("stored_identifiers", len(storedIdents)),
					slog.Int("assignments", len(assignments)),
				)
			}
			if len(targets) > 0 {
				var latestCopy *recomma.BotEvent
				if latestEvent != nil {
					copy := *latestEvent
					latestCopy = &copy
				}
				emissions = append(emissions, emissionPlan{
					action:       action,
					targets:      targets,
					latest:       latestCopy,
					skipExisting: false,
				})
			} else if !replayAvailable {
				orderLogger.Debug("no venue targets resolved; skipping emission")
				continue
			}
		}

		if replayAvailable {
			req := adapter.ToCreateOrderRequest(currency, *latestEvent, oid)
			orderLogger.Info("replaying create for venues missing submissions", slog.Int("count", len(missingTargets)),
				slog.Any("missingTargets", missingTargets))
			targets := append([]recomma.OrderIdentifier(nil), missingTargets...)
			var latestCopy *recomma.BotEvent
			if latestEvent != nil {
				copy := *latestEvent
				latestCopy = &copy
			}
			emissions = append(emissions, emissionPlan{
				action:       recomma.Action{Type: recomma.ActionCreate, Create: req},
				targets:      targets,
				latest:       latestCopy,
				skipExisting: true,
			})
		}

		if len(emissions) == 0 {
			continue
		}

		for _, emission := range emissions {
			action := emission.action
			latestForEmission := emission.latest
			emit := true

			if fillSnapshot != nil && latestForEmission != nil {
				action, emit = e.adjustActionWithTracker(currency, oid, *latestForEmission, action, fillSnapshot, orderLogger, emission.skipExisting)
				if !emit {
					continue
				}
			}

			// Scale and emit per-identifier to ensure each venue gets its own audit record
			for _, ident := range emission.targets {
				identAction := action
				var scaleResult *orderscaler.Result

				if latestForEmission != nil {
					var err error
					latestCopy := *latestForEmission
					identAction, scaleResult, emit, err = e.applyScaling(ctx, ident, &latestCopy, identAction, orderLogger)
					if err != nil {
						orderLogger.Warn("cannot apply scaling", slog.Int("bot-event-id", int(oid.BotEventID)), slog.String("venue", ident.Venue()), slog.String("error", err.Error()))
						return fmt.Errorf("scale order %d for venue %s: %w", oid.BotEventID, ident.Venue(), err)
					}
					if !emit {
						continue
					}
				}

				if e.tracker != nil && scaleResult != nil {
					e.tracker.ApplyScaledOrder(ident, scaleResult.Size, scaleResult.Price)
				}

				work := recomma.OrderWork{
					Identifier: ident,
					OrderId:    oid,
					Action:     identAction,
				}
				if latestForEmission != nil && scaleResult != nil {
					// Use the scaled BotEvent
					work.BotEvent = *latestForEmission
					work.BotEvent.Size = scaleResult.Size
					work.BotEvent.Price = scaleResult.Price
				} else if latestForEmission != nil {
					work.BotEvent = *latestForEmission
				}
				orderLogger.Debug("queueing order work",
					slog.String("action_type", identAction.Type.String()),
					slog.String("venue", ident.Venue()),
					slog.String("wallet", ident.Wallet),
				)
				if err := e.emitter.Emit(ctx, work); err != nil {
					e.logger.Warn("could not submit order", slog.Any("orderid", oid), slog.String("venue", ident.Venue()), slog.Any("action", work.Action), slog.String("error", err.Error()))
				}
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
			return recomma.Action{Type: recomma.ActionCreate, Create: req}, &latestCopy, true, nil
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
const priceTolerance = 1e-6

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

	// Multi-venue scenario: defer to ReconcileTakeProfits for per-venue sizing
	// If multiple active TPs exist, this indicates multi-venue and we should not
	// use global net qty for sizing individual venue TPs
	if len(snapshot.ActiveTakeProfits) > 1 {
		logger.Debug("multi-venue take-profit detected; deferring sizing to reconciliation",
			slog.Int("active_tps", len(snapshot.ActiveTakeProfits)),
		)
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

	// Single-venue scenario: check if the one TP already matches global position
	if !skipExisting && len(snapshot.ActiveTakeProfits) == 1 {
		active := snapshot.ActiveTakeProfits[0]
		targetPrice := latest.Price
		switch action.Type {
		case recomma.ActionCreate:
			if targetPrice == 0 {
				targetPrice = action.Create.Price
			}
		case recomma.ActionModify:
			if targetPrice == 0 {
				targetPrice = action.Modify.Order.Price
			}
		}
		priceMatches := targetPrice > 0 && math.Abs(active.LimitPrice-targetPrice) <= priceTolerance
		if active.ReduceOnly && nearlyEqual(active.RemainingQty, desiredQty) && priceMatches {
			logger.Debug("take profit already matches position",
				slog.Float64("desired_qty", desiredQty),
				slog.Float64("existing_qty", active.RemainingQty),
				slog.Float64("existing_price", active.LimitPrice),
				slog.Float64("desired_price", targetPrice),
				slog.String("venue", active.Identifier.Venue()),
			)
			return recomma.Action{Type: recomma.ActionNone, Reason: "take-profit already matches position"}, false
		}
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
		return recomma.Action{Type: recomma.ActionCreate, Create: req}, true
	case recomma.ActionCreate:
		action.Create.Size = desiredQty
		action.Create.ReduceOnly = true
		logger.Info("creating take profit with tracked size",
			slog.Float64("desired_qty", desiredQty),
			slog.Float64("price", action.Create.Price),
		)
		return action, true
	case recomma.ActionModify:
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
	ident recomma.OrderIdentifier,
	latest *recomma.BotEvent,
	action recomma.Action,
	logger *slog.Logger,
) (recomma.Action, *orderscaler.Result, bool, error) {
	if e.scaler == nil || latest == nil {
		return action, nil, true, nil
	}

	switch action.Type {
	case recomma.ActionCreate:
		req := orderscaler.BuildRequest(ident, latest.BotEvent, action.Create)
		result, err := e.scaler.Scale(ctx, req, &action.Create)
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
		req := orderscaler.BuildRequest(ident, latest.BotEvent, action.Modify.Order)
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
	defaultAssignment *storage.VenueAssignment,
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

	if len(targets) == 0 && defaultAssignment != nil {
		ident := recomma.NewOrderIdentifier(defaultAssignment.VenueID, defaultAssignment.Wallet, oid)
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

func normalizeSubmissionIdentifiers(
	assignments []storage.VenueAssignment,
	stored []recomma.OrderIdentifier,
) []recomma.OrderIdentifier {
	if len(stored) == 0 {
		return nil
	}

	filtering := len(assignments) > 0
	venueWallets := make(map[recomma.VenueID]string, len(assignments))
	walletLookup := make(map[string]recomma.VenueID, len(assignments))
	if filtering {
		for _, assignment := range assignments {
			venueWallets[assignment.VenueID] = assignment.Wallet
			if assignment.Wallet != "" {
				walletLookup[strings.ToLower(assignment.Wallet)] = assignment.VenueID
			}
		}
	}

	type key struct {
		venue  string
		wallet string
	}
	seen := make(map[key]struct{}, len(stored))
	filtered := make([]recomma.OrderIdentifier, 0, len(stored))

	for _, ident := range stored {
		normalized := ident
		if filtering {
			if _, ok := venueWallets[normalized.VenueID]; !ok {
				if mapped, ok := walletLookup[strings.ToLower(normalized.Wallet)]; ok {
					normalized.VenueID = mapped
					if wallet := venueWallets[mapped]; wallet != "" {
						normalized.Wallet = wallet
					}
				} else {
					continue
				}
			} else if wallet := venueWallets[normalized.VenueID]; wallet != "" {
				normalized.Wallet = wallet
			}
		}

		k := key{
			venue:  normalized.Venue(),
			wallet: strings.ToLower(normalized.Wallet),
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		filtered = append(filtered, normalized)
	}

	return filtered
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
