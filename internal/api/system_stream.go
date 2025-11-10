package api

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultSystemStreamBuffer = 100
)

// SystemEventLevel represents the severity of a system event
type SystemEventLevel string

const (
	SystemEventDebug SystemEventLevel = "debug"
	SystemEventInfo  SystemEventLevel = "info"
	SystemEventWarn  SystemEventLevel = "warn"
	SystemEventError SystemEventLevel = "error"
)

// SystemEvent represents a system-level event (error, warning, info, log)
type SystemEvent struct {
	Level     SystemEventLevel       `json:"level"`
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

type systemSubscriber struct {
	id  int64
	ch  chan SystemEvent
	ctx context.Context
}

// SystemStreamController coordinates system event subscribers and event fan-out
type SystemStreamController struct {
	mu          sync.RWMutex
	subscribers map[int64]*systemSubscriber
	nextSubID   int64
	sequence    int64
	logger      *slog.Logger
	bufferSize  int
	minLevel    SystemEventLevel // Only publish events >= this level
}

// NewSystemStreamController constructs a controller with the specified minimum log level
func NewSystemStreamController(minLevel SystemEventLevel) *SystemStreamController {
	return &SystemStreamController{
		subscribers: make(map[int64]*systemSubscriber),
		bufferSize:  defaultSystemStreamBuffer,
		minLevel:    minLevel,
		logger:      slog.Default().WithGroup("system_stream"),
	}
}

// Subscribe registers a subscriber for live system events
func (c *SystemStreamController) Subscribe(ctx context.Context) (<-chan SystemEvent, error) {
	ch := make(chan SystemEvent, c.bufferSize)
	sub := &systemSubscriber{
		id:  atomic.AddInt64(&c.nextSubID, 1),
		ch:  ch,
		ctx: ctx,
	}

	c.mu.Lock()
	c.subscribers[sub.id] = sub
	c.mu.Unlock()

	go c.awaitCancellation(sub)
	return ch, nil
}

func (c *SystemStreamController) awaitCancellation(sub *systemSubscriber) {
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
func (c *SystemStreamController) Publish(evt SystemEvent) {
	// Filter by level
	if !c.shouldPublish(evt.Level) {
		return
	}

	atomic.AddInt64(&c.sequence, 1)

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, sub := range c.subscribers {
		select {
		case sub.ch <- evt:
		default:
			if c.logger != nil {
				c.logger.Warn("dropping system event; subscriber buffer full",
					slog.Int64("subscriber", sub.id),
					slog.String("level", string(evt.Level)),
					slog.String("source", evt.Source))
			}
		}
	}
}

func (c *SystemStreamController) shouldPublish(level SystemEventLevel) bool {
	levelRank := map[SystemEventLevel]int{
		SystemEventDebug: 0,
		SystemEventInfo:  1,
		SystemEventWarn:  2,
		SystemEventError: 3,
	}
	return levelRank[level] >= levelRank[c.minLevel]
}

// Flush drains and closes all subscriber channels. Primarily used in tests.
func (c *SystemStreamController) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, sub := range c.subscribers {
		close(sub.ch)
		delete(c.subscribers, id)
	}
}
