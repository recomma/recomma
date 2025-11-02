package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	defaultStreamBuffer = 64
)

// StreamPublisher consumes order stream events produced by storage mutations.
type StreamPublisher interface {
	Publish(StreamEvent)
}

// StreamController coordinates order stream subscribers and event fan-out.
// It implements both StreamSource (for HTTP handlers) and StreamPublisher (for
// producers writing to the backing store).
type StreamController struct {
	mu          sync.RWMutex
	subscribers map[int64]*streamSubscriber
	nextSubID   int64
	sequence    int64
	logger      *slog.Logger
	bufferSize  int
}

type StreamControllerOption func(*StreamController)

// WithStreamLogger overrides the logger used by the controller.
func WithStreamLogger(logger *slog.Logger) StreamControllerOption {
	return func(c *StreamController) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithStreamBufferSize sets the per-subscriber channel buffer size. Values <= 0
// fall back to the default.
func WithStreamBufferSize(size int) StreamControllerOption {
	return func(c *StreamController) {
		if size > 0 {
			c.bufferSize = size
		}
	}
}

// NewStreamController constructs a controller with sane defaults.
func NewStreamController(opts ...StreamControllerOption) *StreamController {
	c := &StreamController{
		subscribers: make(map[int64]*streamSubscriber),
		bufferSize:  defaultStreamBuffer,
		logger:      slog.Default().WithGroup("stream"),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type streamSubscriber struct {
	id     int64
	filter StreamFilter
	ch     chan StreamEvent
	ctx    context.Context
}

// Subscribe registers a subscriber for live events matching the provided filter.
func (c *StreamController) Subscribe(ctx context.Context, filter StreamFilter) (<-chan StreamEvent, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}

	ch := make(chan StreamEvent, c.bufferSize)
	sub := &streamSubscriber{
		id:     atomic.AddInt64(&c.nextSubID, 1),
		filter: filter,
		ch:     ch,
		ctx:    ctx,
	}

	c.mu.Lock()
	c.subscribers[sub.id] = sub
	c.mu.Unlock()

	go c.awaitCancellation(sub)

	return ch, nil
}

func (c *StreamController) awaitCancellation(sub *streamSubscriber) {
	<-sub.ctx.Done()

	c.mu.Lock()
	if _, ok := c.subscribers[sub.id]; ok {
		delete(c.subscribers, sub.id)
		close(sub.ch)
	}
	c.mu.Unlock()
}

// Publish fan-outs the event to all matching subscribers. Events are delivered
// best-effort – when the subscriber buffer is full the event is dropped for
// that subscriber to avoid blocking producers.
func (c *StreamController) Publish(evt StreamEvent) {
	sequence := atomic.AddInt64(&c.sequence, 1)
	evtCopy := evt
	evtCopy.Sequence = &sequence

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, sub := range c.subscribers {
		if !matchesFilter(sub.filter, evtCopy) {
			continue
		}

		select {
		case sub.ch <- evtCopy:
		default:
			if c.logger != nil {
				c.logger.Warn("dropping stream event; subscriber buffer full",
					slog.Int64("subscriber", sub.id),
					slog.String("orderid", evtCopy.OrderID.Hex()),
					slog.String("type", string(evtCopy.Type)),
				)
			}
		}
	}
}

func matchesFilter(filter StreamFilter, evt StreamEvent) bool {
	if filter.OrderIdPrefix != nil {
		prefix := strings.ToLower(strings.TrimSpace(*filter.OrderIdPrefix))
		if prefix != "" && !strings.HasPrefix(strings.ToLower(evt.OrderID.Hex()), prefix) {
			return false
		}
	}
	if filter.BotID != nil && evt.OrderID.BotID != uint32(*filter.BotID) {
		return false
	}
	if filter.DealID != nil && evt.OrderID.DealID != uint32(*filter.DealID) {
		return false
	}
	if filter.BotEventID != nil && evt.OrderID.BotEventID != uint32(*filter.BotEventID) {
		return false
	}
	if filter.ObservedFrom != nil && evt.ObservedAt.Before(filter.ObservedFrom.UTC()) {
		return false
	}
	return true
}

// Flush drains and closes all subscriber channels. Primarily used in tests.
func (c *StreamController) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, sub := range c.subscribers {
		close(sub.ch)
		delete(c.subscribers, id)
	}
}
