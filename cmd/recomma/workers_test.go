package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/recomma/recomma/emitter"
	rlog "github.com/recomma/recomma/log"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
)

type fakeRateLimitingQueue[T comparable] struct {
	addRateLimited []T
	forgotten      []T
	done           []T
	requeues       map[T]int
}

func newFakeQueue[T comparable]() *fakeRateLimitingQueue[T] {
	return &fakeRateLimitingQueue[T]{
		requeues: make(map[T]int),
	}
}

func (q *fakeRateLimitingQueue[T]) Add(item T) {}

func (q *fakeRateLimitingQueue[T]) Len() int { return 0 }

func (q *fakeRateLimitingQueue[T]) Get() (item T, shutdown bool) {
	var zero T
	return zero, true
}

func (q *fakeRateLimitingQueue[T]) Done(item T) {
	q.done = append(q.done, item)
}

func (q *fakeRateLimitingQueue[T]) ShutDown() {}

func (q *fakeRateLimitingQueue[T]) ShutDownWithDrain() {}

func (q *fakeRateLimitingQueue[T]) ShuttingDown() bool { return false }

func (q *fakeRateLimitingQueue[T]) AddAfter(item T, duration time.Duration) {}

func (q *fakeRateLimitingQueue[T]) AddRateLimited(item T) {
	q.addRateLimited = append(q.addRateLimited, item)
	q.requeues[item]++
}

func (q *fakeRateLimitingQueue[T]) Forget(item T) {
	q.forgotten = append(q.forgotten, item)
	delete(q.requeues, item)
}

func (q *fakeRateLimitingQueue[T]) NumRequeues(item T) int {
	return q.requeues[item]
}

func (q *fakeRateLimitingQueue[T]) setRequeues(item T, count int) {
	q.requeues[item] = count
}

type noopOrderQueue struct{}

func (n *noopOrderQueue) Add(recomma.OrderWork) {}

type stubVenueEmitter struct {
	err error
}

func (s *stubVenueEmitter) Emit(ctx context.Context, w recomma.OrderWork) error {
	return s.err
}

func newDispatcher(stub *stubVenueEmitter) *emitter.QueueEmitter {
	qe := emitter.NewQueueEmitter(&noopOrderQueue{})
	qe.Register(recomma.VenueID("hyperliquid:test"), stub)
	return qe
}

func newOrderWork() recomma.OrderWork {
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "0xabc", oid)
	return recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action: recomma.Action{
			Type: recomma.ActionCancel,
		},
	}
}

func newLoggerContext() context.Context {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	return rlog.ContextWithLogger(context.Background(), logger)
}

func TestProcessOrderItem_OrderAlreadySatisfied(t *testing.T) {
	q := newFakeQueue[recomma.OrderWork]()
	stub := &stubVenueEmitter{err: recomma.ErrOrderAlreadySatisfied}
	dispatcher := newDispatcher(stub)
	work := newOrderWork()

	processOrderItem(newLoggerContext(), q, dispatcher, work)

	require.Len(t, q.addRateLimited, 0)
	require.Len(t, q.forgotten, 1)
	require.Equal(t, work, q.forgotten[0])
	require.Len(t, q.done, 1)
}

func TestProcessOrderItem_ContextTimeoutRequeues(t *testing.T) {
	q := newFakeQueue[recomma.OrderWork]()
	stub := &stubVenueEmitter{err: context.DeadlineExceeded}
	dispatcher := newDispatcher(stub)
	work := newOrderWork()

	processOrderItem(newLoggerContext(), q, dispatcher, work)

	require.Len(t, q.addRateLimited, 1)
	require.Equal(t, work, q.addRateLimited[0])
	require.Len(t, q.forgotten, 0)
}

func TestProcessOrderItem_DiscardedErrors(t *testing.T) {
	q := newFakeQueue[recomma.OrderWork]()
	dispatcher := newDispatcher(&stubVenueEmitter{err: emitter.ErrMissingOrderIdentifier})
	work := newOrderWork()

	processOrderItem(newLoggerContext(), q, dispatcher, work)

	require.Len(t, q.forgotten, 1)
	require.Equal(t, work, q.forgotten[0])
	require.Len(t, q.addRateLimited, 0)
}

func TestProcessOrderItem_RetriesUntilLimit(t *testing.T) {
	q := newFakeQueue[recomma.OrderWork]()
	dispatcher := newDispatcher(&stubVenueEmitter{err: errors.New("boom")})
	work := newOrderWork()

	processOrderItem(newLoggerContext(), q, dispatcher, work)

	require.Len(t, q.addRateLimited, 1)
	require.Equal(t, work, q.addRateLimited[0])
	require.Len(t, q.forgotten, 0)
}

func TestProcessOrderItem_DropsAfterThreshold(t *testing.T) {
	q := newFakeQueue[recomma.OrderWork]()
	dispatcher := newDispatcher(&stubVenueEmitter{err: errors.New("boom")})
	work := newOrderWork()
	q.setRequeues(work, 5)

	processOrderItem(newLoggerContext(), q, dispatcher, work)

	require.Len(t, q.addRateLimited, 0)
	require.Len(t, q.forgotten, 1)
	require.Equal(t, work, q.forgotten[0])
}

func TestProcessOrderItem_SuccessForgets(t *testing.T) {
	q := newFakeQueue[recomma.OrderWork]()
	dispatcher := newDispatcher(&stubVenueEmitter{err: nil})
	work := newOrderWork()

	processOrderItem(newLoggerContext(), q, dispatcher, work)

	require.Len(t, q.forgotten, 1)
	require.Equal(t, work, q.forgotten[0])
	require.Len(t, q.addRateLimited, 0)
}
