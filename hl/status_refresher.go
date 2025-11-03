package hl

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/recomma/recomma/recomma"
	"github.com/sonirico/go-hyperliquid"
	"golang.org/x/sync/errgroup"
)

type orderStatusClient interface {
	QueryOrderByCloid(ctx context.Context, cloid string) (*hyperliquid.OrderQueryResult, error)
}

type statusStore interface {
	ListHyperliquidOrderIds(ctx context.Context) ([]recomma.OrderIdentifier, error)
	RecordHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier, status hyperliquid.WsOrder) error
}

type statusTracker interface {
	UpdateStatus(ctx context.Context, ident recomma.OrderIdentifier, status hyperliquid.WsOrder) error
}

// StatusClientRegistry maps venue identifiers to Hyperliquid info clients.
type StatusClientRegistry map[recomma.VenueID]orderStatusClient

// StatusRefresher pulls the latest Hyperliquid order status for stored
// submissions and mirrors the results into the local database. This is useful
// on startup after downtime to catch up on statuses we missed while offline.
type StatusRefresher struct {
	clients        map[recomma.VenueID]orderStatusClient
	store          statusStore
	tracker        statusTracker
	logger         *slog.Logger
	maxConcurrency int
	timeout        time.Duration
}

// StatusRefresherOption configures a StatusRefresher.
type StatusRefresherOption func(*StatusRefresher)

// WithStatusRefresherTracker attaches a fill tracker so in-memory state is
// kept in sync with refreshed statuses.
func WithStatusRefresherTracker(tracker statusTracker) StatusRefresherOption {
	return func(r *StatusRefresher) {
		r.tracker = tracker
	}
}

// WithStatusRefresherLogger overrides the logger used for diagnostics.
func WithStatusRefresherLogger(logger *slog.Logger) StatusRefresherOption {
	return func(r *StatusRefresher) {
		r.logger = logger
	}
}

// WithStatusRefresherConcurrency sets the maximum number of concurrent status
// queries. Values below 1 default to serial execution.
func WithStatusRefresherConcurrency(n int) StatusRefresherOption {
	return func(r *StatusRefresher) {
		r.maxConcurrency = n
	}
}

// WithStatusRefresherTimeout configures the per-request timeout when querying
// Hyperliquid for order status.
func WithStatusRefresherTimeout(d time.Duration) StatusRefresherOption {
	return func(r *StatusRefresher) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// NewStatusRefresher constructs a new refresher. The client is typically
// an *hl.Info instance.
func NewStatusRefresher(clients StatusClientRegistry, store statusStore, opts ...StatusRefresherOption) *StatusRefresher {
	clientMap := make(map[recomma.VenueID]orderStatusClient)
	for k, v := range clients {
		clientMap[k] = v
	}

	refresher := &StatusRefresher{
		clients:        clientMap,
		store:          store,
		logger:         slog.Default().WithGroup("hyperliquid").WithGroup("status-refresh"),
		maxConcurrency: 4,
		timeout:        20 * time.Second,
	}

	for _, opt := range opts {
		opt(refresher)
	}

	if refresher.maxConcurrency < 1 {
		refresher.maxConcurrency = 1
	}

	return refresher
}

// Refresh loads the known CLOIDs from storage, queries Hyperliquid for each,
// and stores the latest status. The returned error aggregates any failures that
// occurred while refreshing; success for the remaining orders is best-effort.
func (r *StatusRefresher) Refresh(ctx context.Context) error {
	if len(r.clients) == 0 {
		return errors.New("status refresher requires at least one Hyperliquid info client")
	}
	if r.store == nil {
		return errors.New("status refresher requires storage")
	}

	idents, err := r.store.ListHyperliquidOrderIds(ctx)
	if err != nil {
		return fmt.Errorf("list hyperliquid orderids: %w", err)
	}
	if len(idents) == 0 {
		return nil
	}

	start := time.Now()
	var updated atomic.Int32
	var errsMu sync.Mutex
	var errs []error

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.maxConcurrency)

	for _, ident := range idents {
		ident := ident
		g.Go(func() error {
			client, ok := r.clients[ident.VenueID]
			if !ok {
				if r.logger != nil {
					r.logger.Warn("no status client for venue", slog.String("venue", ident.Venue()))
				}
				return nil
			}
			callCtx := gctx
			if r.timeout > 0 {
				var cancel context.CancelFunc
				callCtx, cancel = context.WithTimeout(gctx, r.timeout)
				defer cancel()
			}
			ok, refreshErr := r.refreshOne(callCtx, client, ident)
			if refreshErr != nil {
				errsMu.Lock()
				errs = append(errs, refreshErr)
				errsMu.Unlock()
				return nil
			}
			if ok {
				updated.Add(1)
			}
			return nil
		})
	}

	_ = g.Wait()

	if r.logger != nil {
		r.logger.Info("refreshed hyperliquid statuses",
			slog.Int("metadata", len(idents)),
			slog.Int("updated", int(updated.Load())),
			slog.Duration("elapsed", time.Since(start)))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (r *StatusRefresher) refreshOne(ctx context.Context, client orderStatusClient, ident recomma.OrderIdentifier) (bool, error) {
	result, err := client.QueryOrderByCloid(ctx, ident.Hex())
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("failed to query order status",
				slog.String("oid", ident.Hex()),
				slog.String("error", err.Error()))
		}
		return false, fmt.Errorf("query order %s: %w", ident.Hex(), err)
	}
	if result == nil {
		return false, fmt.Errorf("query order %s returned nil result", ident.Hex())
	}

	wsOrder, err := orderResultToWsOrder(ident.OrderId, result)
	if err != nil {
		return false, fmt.Errorf("convert order %s: %w", ident.Hex(), err)
	}
	if wsOrder == nil {
		if r.logger != nil {
			r.logger.Debug("order status unavailable",
				slog.String("oid", ident.Hex()),
				slog.String("query_status", string(result.Status)))
		}
		return false, nil
	}

	if err := r.store.RecordHyperliquidStatus(ctx, ident, *wsOrder); err != nil {
		return false, fmt.Errorf("record status %s: %w", ident.Hex(), err)
	}

	if r.tracker != nil {
		if err := r.tracker.UpdateStatus(ctx, ident, *wsOrder); err != nil && r.logger != nil {
			r.logger.Warn("fill tracker update failed",
				slog.String("oid", ident.Hex()),
				slog.String("error", err.Error()))
		}
	}

	return true, nil
}
