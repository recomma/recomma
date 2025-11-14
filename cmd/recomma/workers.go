package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"k8s.io/client-go/util/workqueue"

	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/engine"
	rlog "github.com/recomma/recomma/log"
	"github.com/recomma/recomma/recomma"
)

// rateGateKey is used to identify rate limiters per venue/wallet combination
type rateGateKey struct {
	venueType string
	wallet    string
}

// runWorker processes items from the work queue
func runWorker(ctx context.Context, wg *sync.WaitGroup, q workqueue.TypedRateLimitingInterface[engine.WorkKey], e *engine.Engine) {
	defer wg.Done()

	for {
		wi, shutdown := q.Get()
		if shutdown {
			return
		}
		// Use a longer timeout to accommodate 3Commas rate limiting.
		// The SDK's internal rate limiter may need to wait for rate limit windows,
		// which can be up to an hour or more when limits are exceeded.
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
		processWorkItem(reqCtx, q, e, wi)
		cancel()
	}
}

// processWorkItem handles a single work item from the queue
func processWorkItem(ctx context.Context, q workqueue.TypedRateLimitingInterface[engine.WorkKey], e *engine.Engine, wi engine.WorkKey) {
	logger := rlog.LoggerFromContext(ctx).With("work-key", wi)
	defer q.Done(wi)

	if err := e.HandleDeal(ctx, wi); err != nil {
		if errors.Is(err, context.Canceled) {
			q.Forget(wi)
			return
		}

		// Special-case: cache miss should be retried (rate-limited) without applying the generic cap.
		if errors.Is(err, engine.ErrDealNotCached) {
			q.AddRateLimited(wi)
			return
		}

		logger.Debug("error handling deal, forgetting", slog.String("error", err.Error()))
		if q.NumRequeues(wi) < 5 {
			q.AddRateLimited(wi)
			return
		}
		q.Forget(wi)
		return
	}
	q.Forget(wi)
}

// runOrderWorker processes items from the order queue
func runOrderWorker(
	ctx context.Context,
	wg *sync.WaitGroup,
	oq workqueue.TypedRateLimitingInterface[recomma.OrderWork],
	dispatcher *emitter.QueueEmitter,
) {
	defer wg.Done()

	for {
		w, shutdown := oq.Get()
		if shutdown {
			return
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		processOrderItem(reqCtx, oq, dispatcher, w)
		cancel()
	}
}

// processOrderItem handles a single order work item from the queue
func processOrderItem(
	ctx context.Context,
	oq workqueue.TypedRateLimitingInterface[recomma.OrderWork],
	dispatcher *emitter.QueueEmitter,
	w recomma.OrderWork,
) {
	logger := rlog.LoggerFromContext(ctx).With("order-work", w)
	defer oq.Done(w)

	if err := dispatcher.Dispatch(ctx, w); err != nil {
		if errors.Is(err, recomma.ErrOrderAlreadySatisfied) {
			logger.Debug("order already satisfied; skipping submission")
			oq.Forget(w)
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Requeue with backoff so transient timeouts or global pacing don't drop work
			oq.AddRateLimited(w)
			return
		}
		if errors.Is(err, emitter.ErrMissingOrderIdentifier) || errors.Is(err, emitter.ErrUnregisteredVenueEmitter) || errors.Is(err, emitter.ErrOrderIdentifierMismatch) {
			logger.Error("discarding order work", slog.String("reason", err.Error()))
			oq.Forget(w)
			return
		}

		logger.Debug("error submitting order, forgetting", slog.String("error", err.Error()))
		if oq.NumRequeues(w) < 5 {
			oq.AddRateLimited(w)
			return
		}
		oq.Forget(w)
		return
	}

	oq.Forget(w)
}
