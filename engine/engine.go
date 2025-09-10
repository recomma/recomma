package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/terwey/3commas-sdk-go/threecommas"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/adapter"
	"github.com/terwey/recomma/emitter"
	"github.com/terwey/recomma/metadata"
	"github.com/terwey/recomma/storage"
)

// WorkKey is comparable (just ints), safe to use as a queue key.
type WorkKey struct {
	DealID int
	BotID  int
}

type ThreeCommasAPI interface {
	ListBots(ctx context.Context, opts ...tc.ListBotsParamsOption) ([]tc.Bot, error)
	GetListOfDeals(ctx context.Context, opts ...tc.ListDealsParamsOption) ([]tc.Deal, error)
	GetMarketOrdersForDeal(ctx context.Context, id tc.DealPathId) ([]tc.MarketOrder, error)
}

type Queue interface {
	Add(item WorkKey)
}

type Engine struct {
	client    ThreeCommasAPI
	store     storage.Storer
	dealCache sync.Map
	emitter   emitter.Emitter
	bots      *sync.Map
}

func NewEngine(client ThreeCommasAPI, store storage.Storer, emitter emitter.Emitter) *Engine {
	return &Engine{
		client:  client,
		store:   store,
		emitter: emitter,
		bots:    &sync.Map{},
	}
}

func (e *Engine) wasSubmitted(md metadata.Metadata) bool {
	return e.store.SeenKey([]byte(md.String()))
}

func (e *Engine) markSubmitted(md metadata.Metadata, order threecommas.MarketOrder) error {
	raw, err := json.Marshal(order)
	if err != nil {
		return fmt.Errorf("cannot marshal MarketOrder: %w", err)
	}
	// now we can add the Order to our storage that we processed it
	if err := e.store.Add(md.String(), raw); err != nil {
		log.Printf("could not add to store: %v", err)
	}

	return nil
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

	for _, b := range bots {
		if _, ok := e.bots.Load(b.Id); !ok {
			log.Printf("bot not seen before: %d", b.Id)
			e.bots.Store(b.Id, b)
		}
		deals, err := e.client.GetListOfDeals(ctx, tc.WithBotIdForListDeals(b.Id))
		if err != nil {
			log.Printf("list deals for bot %d: %v", b.Id, err)
			continue
		}
		for _, d := range deals {
			e.dealCache.Store(d.Id, d) // so workers can access full Deal when adapting
			q.Add(WorkKey{DealID: d.Id, BotID: d.BotId})
		}
	}
	return nil
}

var ErrDealNotCached = errors.New("deal not cached")

func (e *Engine) HandleDeal(ctx context.Context, wi WorkKey) error {
	// log.Printf("handleDeal dealID %d botId %d", wi.DealID, wi.BotID)
	dealID := wi.DealID
	if dealID == 0 {
		return nil
	}

	orders, err := e.client.GetMarketOrdersForDeal(ctx, tc.DealPathId(dealID))
	if err != nil {
		return err
	}

	botID := wi.BotID

	// fetch full deal from cache (populated by producer) for the adapter
	var deal tc.Deal
	v, ok := e.dealCache.Load(dealID)
	if !ok {
		log.Printf("deal %d not in cache (will resync)", dealID)
		return fmt.Errorf("%w: %d", ErrDealNotCached, dealID)
	}
	deal = v.(tc.Deal)

	for _, order := range orders {
		md := metadata.Metadata{
			BotID:     botID,
			DealID:    dealID,
			CreatedAt: order.CreatedAt,
		}

		err = md.SetOrderIDFromString(order.OrderId)
		if err != nil {
			log.Println(err)
			continue
		}

		if e.wasSubmitted(md) {
			// log.Printf("seen: %s", md.String())
			continue
		}

		if !shouldReplay(order) {
			// log.Printf("should not replay: %v", order)
			continue
		}

		out := adapter.ToCreateOrderRequest(deal, order, md)
		payload, err := json.Marshal(out)
		if err != nil {
			log.Printf("marshal outgoing: %v", err)
			continue
		}
		// Here you would call Hyperliquid; for MVP we just log the payload
		w := emitter.OrderWork{
			MD:    md,
			Req:   out,
			Order: order,
		}
		err = e.emitter.Emit(ctx, w)
		if err != nil {
			log.Printf("payload: \n%s\n", payload)
			log.Printf("could not submit order: %s", err)
			continue
		}

		err = e.markSubmitted(md, order)
		if err != nil {
			log.Printf("could not mark as submitted: %s", err)
		}
	}

	return nil
}

func shouldReplay(order tc.MarketOrder) bool {
	if order.StatusString != tc.Active {
		return false
	}
	if order.OrderType != tc.BUY {
		return false
	}
	return true
}
