package ws

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/recomma/recomma/filltracker"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	storage "github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
)

type Client struct {
	ws            *hyperliquid.WebsocketClient
	subscriptions sync.Map
	coinBbos      sync.Map
	bboTopics     sync.Map

	logger  *slog.Logger
	store   *storage.Storage
	tracker *filltracker.Service
}

type bboTopic struct {
	mu     sync.RWMutex
	subs   map[int64]chan hl.BestBidOffer
	nextID int64
}

func newBBOTopic() *bboTopic {
	return &bboTopic{
		subs: make(map[int64]chan hl.BestBidOffer),
	}
}

func (t *bboTopic) add(ch chan hl.BestBidOffer) int64 {
	id := atomic.AddInt64(&t.nextID, 1)
	t.mu.Lock()
	t.subs[id] = ch
	t.mu.Unlock()
	return id
}

func (t *bboTopic) remove(id int64) {
	t.mu.Lock()
	delete(t.subs, id)
	t.mu.Unlock()
}

func (t *bboTopic) broadcast(bbo hl.BestBidOffer) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, ch := range t.subs {
		select {
		case ch <- bbo:
		default:
			// Drop when subscriber backlog is full.
		}
	}
}

// New opens a websocket and subscribes to order updates for userAddr.
// Pass "" for apiURL to use SDK default (mainnet).
func New(ctx context.Context, store *storage.Storage, tracker *filltracker.Service, userAddr, apiURL string, opts ...hyperliquid.WsOpt) (*Client, error) {
	ws := hyperliquid.NewWebsocketClient(apiURL, opts...)
	if err := ws.Connect(ctx); err != nil {
		return nil, err
	}

	c := &Client{
		ws:      ws,
		logger:  slog.Default().WithGroup("hyperliquid").WithGroup("ws"),
		store:   store,
		tracker: tracker,
	}

	orderUpdatesSub, err := ws.OrderUpdates(
		hyperliquid.OrderUpdatesSubscriptionParams{User: userAddr},
		func(orders []hyperliquid.WsOrder, err error) {
			if err != nil {
				return
			}
			c.logger.Debug("OrderUpdates", slog.Any("orders", orders))
			for _, o := range orders {
				if o.Order.Cloid != nil {
					oid, err := orderid.FromHexString(*o.Order.Cloid)
					if err != nil {
						c.logger.Warn("could not get orderid from cloid", slog.String("cloid", *o.Order.Cloid), slog.String("error", err.Error()))
						continue
					}
					if c.tracker != nil {
						if err := c.tracker.UpdateStatus(context.Background(), *oid, o); err != nil {
							c.logger.Warn("could not update fill tracker", slog.String("error", err.Error()))
						}
					}
					if err := c.store.RecordHyperliquidStatus(context.Background(), *oid, o); err != nil {
						c.logger.Warn("could not store status", slog.String("error", err.Error()))
						continue
					}
				}
			}
		},
	)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}

	c.subscriptions.Store("orderUpdates", orderUpdatesSub)
	return c, nil
}

func (c *Client) EnsureBBO(coin string) {
	coin = normalizeCoin(coin)
	if coin == "" {
		return
	}
	if _, ok := c.subscriptions.Load(coin); ok {
		// exists
		return
	}
	c.logger.Info("Subscribing to BestBidOffer", slog.String("coin", coin))
	bboSub, err := c.ws.Bbo(hyperliquid.BboSubscriptionParams{Coin: coin},
		func(bbo hyperliquid.Bbo, err error) {
			if err != nil {
				if c.logger != nil {
					c.logger.Warn("BBO callback error", slog.String("coin", coin), slog.String("error", err.Error()))
				}
				return
			}

			// c.logger.Debug("BBo", slog.String("coin", coin), slog.Any("bbo", bbo))
			c.storeCoinBBO(coin, hl.WsBBOToBBO(bbo))
		})
	if err != nil {
		c.logger.Warn("could not subscribe to BBO", slog.String("coin", coin), slog.String("error", err.Error()))
	}

	c.subscriptions.Store(coin, bboSub)
}

func (c *Client) storeCoinBBO(coin string, bbo hl.BestBidOffer) {
	key := normalizeCoin(coin)
	if key == "" {
		return
	}
	bbo.Coin = key
	c.coinBbos.Store(key, bbo)
	if raw, ok := c.bboTopics.Load(key); ok {
		if topic, ok := raw.(*bboTopic); ok {
			topic.broadcast(bbo)
		}
	}
}

// WaitForBestBidOffer blocks for a BestBidOffer result until the context reaches a timeout
func (c *Client) WaitForBestBidOffer(ctx context.Context, coin string) *hl.BestBidOffer {
	coin = normalizeCoin(coin)
	if coin == "" {
		return nil
	}
	for {
		if v, ok := c.coinBbos.Load(coin); ok {
			if bbo, ok := v.(hl.BestBidOffer); ok {
				return &bbo
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// SubscribeBBO returns a channel that receives best bid/offer updates for the given coin.
func (c *Client) SubscribeBBO(ctx context.Context, coin string) (<-chan hl.BestBidOffer, error) {
	coin = normalizeCoin(coin)
	if coin == "" {
		return nil, fmt.Errorf("coin is required")
	}

	c.EnsureBBO(coin)
	if c.logger != nil {
		c.logger.Debug("SubscribeBBO", slog.String("coin", coin))
	}

	topic := c.ensureBBOTopic(coin)
	if topic == nil {
		return nil, fmt.Errorf("unable to create subscription topic")
	}

	ch := make(chan hl.BestBidOffer, 8)
	id := topic.add(ch)

	if current := c.currentBBO(coin); current != nil {
		select {
		case ch <- *current:
		default:
		}
	}

	go func() {
		<-ctx.Done()
		topic.remove(id)
		close(ch)
		if c.logger != nil {
			c.logger.Debug("SubscribeBBO closed", slog.String("coin", coin))
		}
	}()

	return ch, nil
}

// Close closes the subscription and websocket.
func (c *Client) Close() error {
	c.subscriptions.Range(func(key any, value any) bool {
		if sub, ok := value.(*hyperliquid.Subscription); ok {
			sub.Close()
		}
		return true
	})

	if c.ws != nil {
		return c.ws.Close()
	}
	return nil
}

// Exists returns true if we've seen this CLOID via OrderUpdates.
func (c *Client) Exists(ctx context.Context, oid orderid.OrderId) bool {
	_, ok, err := c.store.LoadHyperliquidStatus(ctx, oid)
	if err != nil {
		return false
	}

	return ok
}

// Get returns the full WsOrder for a CLOID, if we have it.
func (c *Client) Get(ctx context.Context, oid orderid.OrderId) (*hyperliquid.WsOrder, bool) {
	status, ok, err := c.store.LoadHyperliquidStatus(ctx, oid)
	if err != nil {
		return nil, false
	}

	return status, ok
}

func (c *Client) ensureBBOTopic(coin string) *bboTopic {
	if coin == "" {
		return nil
	}
	actual, _ := c.bboTopics.LoadOrStore(coin, newBBOTopic())
	if topic, ok := actual.(*bboTopic); ok {
		return topic
	}
	return nil
}

func (c *Client) currentBBO(coin string) *hl.BestBidOffer {
	coin = normalizeCoin(coin)
	if coin == "" {
		return nil
	}
	if v, ok := c.coinBbos.Load(coin); ok {
		if bbo, ok := v.(hl.BestBidOffer); ok {
			return &bbo
		}
	}
	return nil
}

func normalizeCoin(coin string) string {
	return strings.ToUpper(strings.TrimSpace(coin))
}
