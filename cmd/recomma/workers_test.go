package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/util/workqueue"

	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/engine"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
)

// mockEngine implements a simple mock of the engine for testing
type mockEngine struct {
	handleDealFunc func(ctx context.Context, key engine.WorkKey) error
}

func (m *mockEngine) HandleDeal(ctx context.Context, key engine.WorkKey) error {
	if m.handleDealFunc != nil {
		return m.handleDealFunc(ctx, key)
	}
	return nil
}

// mockDispatcher implements a simple mock of the dispatcher for testing
type mockDispatcher struct {
	dispatchFunc func(ctx context.Context, work recomma.OrderWork) error
}

func (m *mockDispatcher) Dispatch(ctx context.Context, work recomma.OrderWork) error {
	if m.dispatchFunc != nil {
		return m.dispatchFunc(ctx, work)
	}
	return nil
}

func TestProcessWorkItem_Success(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[engine.WorkKey]())
	defer q.ShutDown()

	engine := &mockEngine{
		handleDealFunc: func(ctx context.Context, key engine.WorkKey) error {
			return nil // Success
		},
	}

	wi := engine.WorkKey{DealID: 123, BotID: 456}
	q.Add(wi)

	ctx := context.Background()
	processWorkItem(ctx, q, engine, wi)

	// Item should be forgotten (not requeued)
	require.Equal(t, 0, q.NumRequeues(wi))
}

func TestProcessWorkItem_ContextCanceled(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[engine.WorkKey]())
	defer q.ShutDown()

	engine := &mockEngine{
		handleDealFunc: func(ctx context.Context, key engine.WorkKey) error {
			return context.Canceled
		},
	}

	wi := engine.WorkKey{DealID: 123, BotID: 456}
	q.Add(wi)

	ctx := context.Background()
	processWorkItem(ctx, q, engine, wi)

	// Item should be forgotten (not requeued on cancel)
	require.Equal(t, 0, q.NumRequeues(wi))
}

func TestProcessWorkItem_DealNotCached(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[engine.WorkKey]())
	defer q.ShutDown()

	engine := &mockEngine{
		handleDealFunc: func(ctx context.Context, key engine.WorkKey) error {
			return engine.ErrDealNotCached
		},
	}

	wi := engine.WorkKey{DealID: 123, BotID: 456}
	q.Add(wi)

	ctx := context.Background()
	processWorkItem(ctx, q, engine, wi)

	// Item should be requeued with rate limit
	require.Equal(t, 1, q.NumRequeues(wi))
}

func TestProcessWorkItem_TransientError(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[engine.WorkKey]())
	defer q.ShutDown()

	engine := &mockEngine{
		handleDealFunc: func(ctx context.Context, key engine.WorkKey) error {
			return errors.New("transient error")
		},
	}

	wi := engine.WorkKey{DealID: 123, BotID: 456}
	q.Add(wi)

	ctx := context.Background()

	// First 5 attempts should requeue
	for i := 0; i < 5; i++ {
		processWorkItem(ctx, q, engine, wi)
		require.Equal(t, i+1, q.NumRequeues(wi), "requeue count after attempt %d", i+1)
		q.Add(wi) // Manually re-add for next iteration
	}

	// 6th attempt should forget
	processWorkItem(ctx, q, engine, wi)
	// After forgetting, NumRequeues should still be 5 (forgetting doesn't reset the counter)
}

func TestProcessOrderItem_Success(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return nil // Success
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{
			VenueID: "test",
			Wallet:  "0x123",
			OrderId: orderid.OrderId{DealID: 123, BotID: 456, EventFingerprint: "abc"},
		},
	}
	q.Add(work)

	ctx := context.Background()
	processOrderItem(ctx, q, dispatcher, work)

	// Item should be forgotten (not requeued)
	require.Equal(t, 0, q.NumRequeues(work))
}

func TestProcessOrderItem_AlreadySatisfied(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return recomma.ErrOrderAlreadySatisfied
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{
			VenueID: "test",
			Wallet:  "0x123",
			OrderId: orderid.OrderId{DealID: 123, BotID: 456, EventFingerprint: "abc"},
		},
	}
	q.Add(work)

	ctx := context.Background()
	processOrderItem(ctx, q, dispatcher, work)

	// Item should be forgotten (no retry on already satisfied)
	require.Equal(t, 0, q.NumRequeues(work))
}

func TestProcessOrderItem_ContextCanceled(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return context.Canceled
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{
			VenueID: "test",
			Wallet:  "0x123",
			OrderId: orderid.OrderId{DealID: 123, BotID: 456, EventFingerprint: "abc"},
		},
	}
	q.Add(work)

	ctx := context.Background()
	processOrderItem(ctx, q, dispatcher, work)

	// Item should be requeued with rate limit (transient error)
	require.Equal(t, 1, q.NumRequeues(work))
}

func TestProcessOrderItem_DeadlineExceeded(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return context.DeadlineExceeded
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{
			VenueID: "test",
			Wallet:  "0x123",
			OrderId: orderid.OrderId{DealID: 123, BotID: 456, EventFingerprint: "abc"},
		},
	}
	q.Add(work)

	ctx := context.Background()
	processOrderItem(ctx, q, dispatcher, work)

	// Item should be requeued with rate limit (transient timeout)
	require.Equal(t, 1, q.NumRequeues(work))
}

func TestProcessOrderItem_MissingIdentifier(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return emitter.ErrMissingOrderIdentifier
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{},
	}
	q.Add(work)

	ctx := context.Background()
	processOrderItem(ctx, q, dispatcher, work)

	// Item should be forgotten (permanent error)
	require.Equal(t, 0, q.NumRequeues(work))
}

func TestProcessOrderItem_UnregisteredVenue(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return emitter.ErrUnregisteredVenueEmitter
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{
			VenueID: "unknown",
			Wallet:  "0x123",
			OrderId: orderid.OrderId{DealID: 123, BotID: 456, EventFingerprint: "abc"},
		},
	}
	q.Add(work)

	ctx := context.Background()
	processOrderItem(ctx, q, dispatcher, work)

	// Item should be forgotten (permanent config error)
	require.Equal(t, 0, q.NumRequeues(work))
}

func TestProcessOrderItem_IdentifierMismatch(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return emitter.ErrOrderIdentifierMismatch
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{
			VenueID: "test",
			Wallet:  "0x123",
			OrderId: orderid.OrderId{DealID: 123, BotID: 456, EventFingerprint: "abc"},
		},
	}
	q.Add(work)

	ctx := context.Background()
	processOrderItem(ctx, q, dispatcher, work)

	// Item should be forgotten (permanent error)
	require.Equal(t, 0, q.NumRequeues(work))
}

func TestProcessOrderItem_TransientError(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())
	defer q.ShutDown()

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return errors.New("transient network error")
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.OrderIdentifier{
			VenueID: "test",
			Wallet:  "0x123",
			OrderId: orderid.OrderId{DealID: 123, BotID: 456, EventFingerprint: "abc"},
		},
	}
	q.Add(work)

	ctx := context.Background()

	// First 5 attempts should requeue
	for i := 0; i < 5; i++ {
		processOrderItem(ctx, q, dispatcher, work)
		require.Equal(t, i+1, q.NumRequeues(work), "requeue count after attempt %d", i+1)
		q.Add(work) // Manually re-add for next iteration
	}

	// 6th attempt should forget
	processOrderItem(ctx, q, dispatcher, work)
}

func TestRateGateKey(t *testing.T) {
	// Just verify the type exists and can be created
	key := rateGateKey{
		venueType: "hyperliquid",
		wallet:    "0x1234",
	}

	require.Equal(t, "hyperliquid", key.venueType)
	require.Equal(t, "0x1234", key.wallet)
}

func TestRunWorker_Shutdown(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[engine.WorkKey]())

	engine := &mockEngine{
		handleDealFunc: func(ctx context.Context, key engine.WorkKey) error {
			return nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)

	// Start worker
	go runWorker(ctx, &wg, q, engine)

	// Shutdown queue
	q.ShutDown()

	// Wait for worker to finish with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Worker shut down successfully
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not shut down in time")
	}
}

func TestRunOrderWorker_Shutdown(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[recomma.OrderWork]())

	dispatcher := &mockDispatcher{
		dispatchFunc: func(ctx context.Context, work recomma.OrderWork) error {
			return nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)

	// Start worker
	go runOrderWorker(ctx, &wg, q, dispatcher)

	// Shutdown queue
	q.ShutDown()

	// Wait for worker to finish with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Worker shut down successfully
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not shut down in time")
	}
}
