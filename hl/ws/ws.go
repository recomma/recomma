package ws

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/sonirico/go-hyperliquid"
	"github.com/terwey/recomma/filltracker"
	"github.com/terwey/recomma/hl"
	"github.com/terwey/recomma/metadata"
	storage "github.com/terwey/recomma/storage"
)

type Client struct {
	ws            *hyperliquid.WebsocketClient
	subscriptions sync.Map
	coinBbos      sync.Map

	logger  *slog.Logger
	store   *storage.Storage
	tracker *filltracker.Service
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
					md, err := metadata.FromHexString(*o.Order.Cloid)
					if err != nil {
						c.logger.Warn("could not get metadata from cloid", slog.String("cloid", *o.Order.Cloid), slog.String("error", err.Error()))
						continue
					}
					if c.tracker != nil {
						if err := c.tracker.UpdateStatus(*md, o); err != nil {
							c.logger.Warn("could not update fill tracker", slog.String("error", err.Error()))
						}
					}
					if err := c.store.RecordHyperliquidStatus(*md, o); err != nil {
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
	if _, ok := c.subscriptions.Load(coin); ok {
		// exists
		return
	}
	c.logger.Info("Subscribing to BestBidOffer", slog.String("coin", coin))
	bboSub, err := c.ws.Bbo(hyperliquid.BboSubscriptionParams{Coin: coin},
		func(bbo hyperliquid.Bbo, err error) {
			if err != nil {
				return
			}

			// c.logger.Debug("BBo", slog.String("coin", coin), slog.Any("bbo", bbo))
			c.storeCoinBBO(coin, hl.WsBBOToBBO(bbo))
		})
	if err != nil {
		c.logger.Warn("could not subscribe to BBO", slog.String("coin", coin))
	}

	c.subscriptions.Store(coin, bboSub)
}

func (c *Client) storeCoinBBO(coin string, bbo hl.BestBidOffer) {
	c.coinBbos.Store(coin, bbo)
}

// WaitForBestBidOffer blocks for a BestBidOffer result until the context reaches a timeout
func (c *Client) WaitForBestBidOffer(ctx context.Context, coin string) *hl.BestBidOffer {
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
func (c *Client) Exists(md metadata.Metadata) bool {
	_, ok, err := c.store.LoadHyperliquidStatus(md)
	if err != nil {
		return false
	}

	return ok
}

// Get returns the full WsOrder for a CLOID, if we have it.
func (c *Client) Get(md metadata.Metadata) (*hyperliquid.WsOrder, bool) {
	status, ok, err := c.store.LoadHyperliquidStatus(md)
	if err != nil {
		return nil, false
	}

	return status, ok
}
