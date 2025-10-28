package emitter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
)

type OrderQueue interface {
	Add(item recomma.OrderWork)
}

type QueueEmitter struct {
	q      OrderQueue
	logger *slog.Logger
}

func NewQueueEmitter(q OrderQueue) *QueueEmitter {
	return &QueueEmitter{
		q:      q,
		logger: slog.Default().WithGroup("emitter"),
	}
}

func (e *QueueEmitter) Emit(ctx context.Context, w recomma.OrderWork) error {
	e.logger.Debug("emit", slog.Any("order-work", w))
	e.q.Add(w)
	return nil
}

type HyperLiquidEmitter struct {
	exchange    *hyperliquid.Exchange
	ws          *ws.Client
	store       *storage.Storage
	mu          sync.Mutex
	nextAllowed time.Time
	minSpacing  time.Duration
	logger      *slog.Logger
}

func NewHyperLiquidEmitter(exchange *hyperliquid.Exchange, ws *ws.Client, store *storage.Storage) *HyperLiquidEmitter {
	return &HyperLiquidEmitter{
		exchange:    exchange,
		ws:          ws,
		store:       store,
		nextAllowed: time.Now(),
		minSpacing:  300 * time.Millisecond,
		logger:      slog.Default().WithGroup("hl-emitter"),
	}
}

// waitTurn enforces a simple global pacing for all Hyperliquid actions
// to avoid bursting into HL rate limits. It spaces calls by minSpacing,
// and can be tightened by applying a longer cooldown when 429s are seen.
func (e *HyperLiquidEmitter) waitTurn(ctx context.Context) error {
	for {
		e.mu.Lock()
		wait := time.Until(e.nextAllowed)
		if wait <= 0 {
			// reserve our slot and release the lock
			e.nextAllowed = time.Now().Add(e.minSpacing)
			e.mu.Unlock()
			return nil
		}
		e.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (e *HyperLiquidEmitter) applyCooldown(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	next := now.Add(d)
	if next.After(e.nextAllowed) {
		e.nextAllowed = next
	}
}

func (e *HyperLiquidEmitter) setMarketPrice(ctx context.Context, order hyperliquid.CreateOrderRequest, increase bool) hyperliquid.CreateOrderRequest {
	e.ws.EnsureBBO(order.Coin)
	if order.Price == 0 {
		bboCtx, bboCancel := context.WithTimeout(ctx, time.Second*30)
		defer bboCancel()
		bbo := e.ws.WaitForBestBidOffer(bboCtx, order.Coin)
		if bbo != nil {
			if order.IsBuy {
				order.Price = bbo.Ask.Price
				if increase {
					order.Price = bbo.Ask.Price + bbo.Ask.Price*0.0005
				}
			} else {
				order.Price = bbo.Bid.Price
			}
		}
	}

	return order
}

func (e *HyperLiquidEmitter) Emit(ctx context.Context, w recomma.OrderWork) error {
	logger := e.logger.With("md", w.MD.Hex()).With("bot-event", w.BotEvent)
	logger.Debug("emit", slog.Any("orderwork", w))
	if err := e.waitTurn(ctx); err != nil {
		return err
	}

	// TODO: decide if we want to persist the result we get back here, it's not interesting ususally as it just states `resting`
	switch w.Action.Type {
	case recomma.ActionCreate:
		if e.ws.Exists(ctx, w.MD) {
			logger.Debug("order already exists on Hyperliquid")
			return nil
		}

		order := e.setMarketPrice(ctx, *w.Action.Create, false)
		w.Action.Create = &order
		_, err := e.exchange.Order(ctx, *w.Action.Create, nil)
		if err != nil {
			// HL rejected the IOC order, let's fetch a new price, slightly increase and try again
			// TODO: maybe we can attach some data to the error we return so it can be price increased on next queue
			if strings.Contains(err.Error(), "Order could not immediately match against any resting orders") {
				if err := e.waitTurn(ctx); err != nil {
					return err
				}
				order := e.setMarketPrice(ctx, *w.Action.Create, true)
				logger = logger.With("increased", true)
				w.Action.Create = &order
				_, err := e.exchange.Order(ctx, *w.Action.Create, nil)
				// TODO: figure out if we want a proper retry logic for errors
				if err != nil {
					return err
				}
			}

			// we cannot submit these but we must ignore them
			if strings.Contains(err.Error(), "Order must have minimum value") {
				if err := e.store.RecordHyperliquidOrderRequest(ctx, w.MD, *w.Action.Create, w.BotEvent.RowID); err != nil {
					logger.Warn("could not add to store", slog.String("error", err.Error()))
				}
				logger.Warn("could not submit (order value), ignoring", slog.String("error", err.Error()))
				return nil
			}

			if strings.Contains(err.Error(), "Reduce only order would increase position") {
				if err := e.store.RecordHyperliquidOrderRequest(ctx, w.MD, *w.Action.Create, w.BotEvent.RowID); err != nil {
					logger.Warn("could not add to store", slog.String("error", err.Error()))
				}
				logger.Warn("could not submit (reduce only order, increase position), ignoring", slog.String("error", err.Error()))
				return nil
			}

			// If HL rate limits (address-based or IP-based), apply a cooldown.
			if strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "rate limit") {
				logger.Debug("hit ratelimit, cooldown of 10s applied")
				// HL allows ~1 action per 10s when address-limited.
				e.applyCooldown(10 * time.Second)
			}
			logger.Warn("could not place order", slog.String("error", err.Error()), slog.Any("action", w.Action.Create))
			return fmt.Errorf("could not place order: %w", err)
		}

		if err := e.store.RecordHyperliquidOrderRequest(ctx, w.MD, *w.Action.Create, w.BotEvent.RowID); err != nil {
			logger.Warn("could not add to store", slog.String("error", err.Error()))
		}
	case recomma.ActionCancel:
		logger.Info("Cancelling order", slog.Any("cancel", w.Action.Cancel))
		_, err := e.exchange.CancelByCloid(ctx, w.Action.Cancel.Coin, w.Action.Cancel.Cloid)
		if err != nil {

			// If HL rate limits (address-based or IP-based), apply a cooldown.
			if strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "rate limit") {
				logger.Debug("hit ratelimit, cooldown of 10s applied")
				// HL allows ~1 action per 10s when address-limited.
				e.applyCooldown(10 * time.Second)
			}
			logger.Warn("could not cancel order", slog.String("error", err.Error()), slog.Any("action", w.Action.Cancel))
			return fmt.Errorf("could not cancel order: %w", err)
		}
		if err := e.store.RecordHyperliquidCancel(ctx, w.MD, *w.Action.Cancel, w.BotEvent.RowID); err != nil {
			logger.Warn("could not add to store", slog.String("error", err.Error()))
		}
	case recomma.ActionNone:
		// order filled
		return nil
	case recomma.ActionModify:
		order := e.setMarketPrice(ctx, w.Action.Modify.Order, false)
		w.Action.Modify.Order = order
		_, err := e.exchange.ModifyOrder(ctx, *w.Action.Modify)
		if err != nil {
			// If HL rate limits (address-based or IP-based), apply a cooldown.
			if strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "rate limit") {
				logger.Debug("hit ratelimit, cooldown of 10s applied")
				// HL allows ~1 action per 10s when address-limited.
				e.applyCooldown(10 * time.Second)
			}
			logger.Warn("could not modify order", slog.String("error", err.Error()), slog.Any("action", w.Action.Modify))
			return fmt.Errorf("could not modify order: %w", err)
		}
		if err := e.store.AppendHyperliquidModify(ctx, w.MD, *w.Action.Modify, w.BotEvent.RowID); err != nil {
			logger.Warn("could not add to store", slog.String("error", err.Error()))
		}
	}

	logger.Info("Order sent", slog.Any("action", w.Action))

	return nil
}
