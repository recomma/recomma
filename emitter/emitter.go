package emitter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/sonirico/go-hyperliquid"
	"github.com/terwey/3commas-sdk-go/threecommas"
	"github.com/terwey/recomma/hl"
	"github.com/terwey/recomma/metadata"
	"github.com/terwey/recomma/storage"
)

type OrderWork struct {
	MD    metadata.Metadata
	Req   hyperliquid.CreateOrderRequest
	Order threecommas.MarketOrder
}

type Emitter interface {
	Emit(ctx context.Context, w OrderWork) error
}

type OrderQueue interface {
	Add(item OrderWork)
}

type QueueEmitter struct {
	q OrderQueue
}

func NewQueueEmitter(q OrderQueue) *QueueEmitter {
	return &QueueEmitter{q: q}
}

func (e *QueueEmitter) Emit(ctx context.Context, w OrderWork) error {
	log.Printf("QueueEmitter.Emit: %v", w)
	e.q.Add(w)
	return nil
}

type HyperLiquidEmitter struct {
	exchange *hyperliquid.Exchange
	info     *hl.Info
	store    storage.Storer
}

func NewHyperLiquidEmitter(exchange *hyperliquid.Exchange, info *hl.Info, store storage.Storer) *HyperLiquidEmitter {
	return &HyperLiquidEmitter{
		exchange: exchange,
		info:     info,
		store:    store,
	}
}

func (e *HyperLiquidEmitter) markEmitted(md metadata.Metadata, req hyperliquid.CreateOrderRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("cannot marshal CreateOrderRequest: %w", err)
	}

	if err := e.store.Add("req|"+md.String(), raw); err != nil {
		log.Printf("could not add to store: %v", err)
	}

	return nil
}

func (e *HyperLiquidEmitter) storeResult(md metadata.Metadata, status hyperliquid.OrderStatus) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("cannot marshal OrderStatus: %w", err)
	}

	if err := e.store.Add("status|"+md.String(), raw); err != nil {
		log.Printf("could not add to store: %v", err)
	}

	return nil
}

func (e *HyperLiquidEmitter) Emit(ctx context.Context, w OrderWork) error {
	// TODO: the client doesn't handle context, we need to wrap this

	log.Printf("HyperLiquidEmitter.Emit: %v", w)

	// TODO: check if we later can do this with a central store that just fetches
	// the Orders endpoint on a loop
	res, _ := e.info.QueryOrderByCloid(*w.Req.ClientOrderID)
	// we don't care about the error, we must check if res exists
	if res != nil {
		if res.Status != "unknownOid" {
			// order already exists, skipping
			log.Printf("order already exists: %v\n%v", w, res)
			return nil
		}
	}

	result, err := e.exchange.Order(w.Req, nil)
	if err != nil {
		// we cannot submit these but we must ignore them
		if strings.Contains(err.Error(), "Order must have minimum value") {
			e.markEmitted(w.MD, w.Req)
			log.Printf("could not submit, ignoring: %s", err)
			return nil
		}
		return fmt.Errorf("could not place order: %w", err)
	}

	err = e.markEmitted(w.MD, w.Req)
	if err != nil {
		log.Printf("could not mark as emitted: %s", err)
	}

	err = e.storeResult(w.MD, result)
	if err != nil {
		log.Printf("could not store result: %s", err)
	}

	return nil
}
