package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/adapter"
	"github.com/terwey/recomma/metadata"
	"github.com/terwey/recomma/recomma"
	"github.com/terwey/recomma/storage"
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
}

func NewEngine(client ThreeCommasAPI, store *storage.Storage, emitter recomma.Emitter) *Engine {
	return &Engine{
		client:  client,
		store:   store,
		emitter: emitter,
		logger:  slog.Default().WithGroup("engine"),
	}
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

			_, syncedAt, found, err := e.store.LoadBot(b.Id)
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
				err := e.store.RecordThreeCommasDeal(d)
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

			if err := e.store.RecordBot(b, b.UpdatedAt); err != nil {
				logger.Warn("could not record bot", slog.String("error", err.Error()))
			}

			e.store.TouchBot(b.Id, latest)
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
	seen := make(map[uint32]metadata.Metadata)

	for _, event := range events {
		md := metadata.Metadata{
			BotID:      wi.BotID,
			DealID:     wi.DealID,
			BotEventID: event.FingerprintAsID(),
		}
		// we want to store all incoming as a log
		_, err := e.store.RecordThreeCommasBotEventLog(md, event)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("record bot event log: %w", err)
			}
		}

		if event.OrderType == tc.MarketOrderDealOrderTypeTakeProfit {
			if event.Action == tc.BotEventActionCancel || event.Action == tc.BotEventActionCancelled {
				// we ignore Take Profit cancellations, we cancel TP's ourselves based on the combined orders for the deal
				continue
			}
		}
		// we only want to act on PLACING, CANCEL and MODIFY
		// we assume here that when within the span of 15s (our poll time) a botevent went from PLACING to CANCEL we can ignore it
		if event.Action == tc.BotEventActionPlace || event.Action == tc.BotEventActionCancel || event.Action == tc.BotEventActionModify {
			lastInsertedId, err := e.store.RecordThreeCommasBotEvent(md, event)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					// this means we saw it before, no error
					continue
				}
				return fmt.Errorf("record bot event: %w", err)
			}
			if lastInsertedId != 0 {
				seen[md.BotEventID] = md
			}
		}
	}

	for _, md := range seen {
		action, latestEvent, shouldEmit, err := e.reduceOrderEvents(currency, md, logger.With("botevent-id", md.BotEventID))
		if err != nil {
			return fmt.Errorf("reduce order %d: %w", md.BotEventID, err)
		}
		if !shouldEmit {
			continue
		}
		work := recomma.OrderWork{MD: md, Action: action}
		if latestEvent != nil {
			work.BotEvent = *latestEvent
		}
		if err := e.emitter.Emit(ctx, work); err != nil {
			e.logger.Warn("could not submit order", slog.Any("md", md), slog.Any("action", action), slog.String("error", err.Error()))
		}
	}

	return nil
}

// reduceOrderEvents inspects every 3Commas snapshot we have stored for a single
// BotEvent fingerprint and projects it into "the next thing we still need to do
// on Hyperliquid". It returns the action plus a flag telling the caller whether
// anything needs to be emitted.
func (e *Engine) reduceOrderEvents(
	currency string,
	md metadata.Metadata,
	logger *slog.Logger,
) (recomma.Action, *recomma.BotEvent, bool, error) {

	// NB: we actually only care about the PLACING one's

	// rows are already sorted by CreatedAt ASC in ListEventsForOrder.
	events, err := e.store.ListEventsForOrder(md.BotID, md.DealID, md.BotEventID)
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
	submitted, haveSubmission, err := e.store.LoadHyperliquidSubmission(md)
	if err != nil {
		return recomma.Action{}, nil, false, fmt.Errorf("load submission: %w", err)
	}
	hasLocalOrder := haveSubmission && submitted.Create != nil

	// If Hyperliquid hasn’t seen this order yet, we pretend there is no “previous”
	// snapshot so BuildAction can only choose between Create or None.
	if !hasLocalOrder {
		prev = nil
	}

	action := adapter.BuildAction(currency, prev, latest, md)

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
			req := adapter.ToCreateOrderRequest(currency, latest, md)
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
