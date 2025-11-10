package api

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultSystemStreamBuffer  = 100
	defaultSystemEventHistory  = 50           // Keep last 50 events
	defaultSystemEventMaxAge   = 5 * time.Minute // Keep events for 5 minutes
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

	// Message history for late joiners
	eventHistory    []SystemEvent
	maxHistorySize  int
	maxHistoryAge   time.Duration
}

// NewSystemStreamController constructs a controller with the specified minimum log level
func NewSystemStreamController(minLevel SystemEventLevel) *SystemStreamController {
	return &SystemStreamController{
		subscribers:     make(map[int64]*systemSubscriber),
		bufferSize:      defaultSystemStreamBuffer,
		minLevel:        minLevel,
		logger:          slog.Default().WithGroup("system_stream"),
		eventHistory:    make([]SystemEvent, 0, defaultSystemEventHistory),
		maxHistorySize:  defaultSystemEventHistory,
		maxHistoryAge:   defaultSystemEventMaxAge,
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

	// Send event history to new subscriber immediately
	history := c.getRecentHistory()
	c.logger.Info("New subscriber registered",
		slog.Int64("subscriber_id", sub.id),
		slog.Int("history_events", len(history)))
	c.mu.Unlock()

	// Send history events in a goroutine to avoid blocking
	go func() {
		for i, evt := range history {
			c.logger.Debug("Sending history event to new subscriber",
				slog.Int64("subscriber_id", sub.id),
				slog.Int("event_index", i),
				slog.String("level", string(evt.Level)),
				slog.String("source", evt.Source),
				slog.String("message", evt.Message))
			select {
			case ch <- evt:
				c.logger.Debug("History event sent successfully",
					slog.Int64("subscriber_id", sub.id),
					slog.Int("event_index", i))
			case <-ctx.Done():
				c.logger.Warn("Subscriber context cancelled while sending history",
					slog.Int64("subscriber_id", sub.id))
				return
			default:
				// Skip if buffer full (shouldn't happen with fresh subscriber)
				c.logger.Warn("Skipping history event, buffer full",
					slog.Int64("subscriber_id", sub.id),
					slog.Int("event_index", i))
			}
		}
		c.logger.Info("Finished sending history events",
			slog.Int64("subscriber_id", sub.id),
			slog.Int("events_sent", len(history)))
	}()

	go c.awaitCancellation(sub)
	return ch, nil
}

// getRecentHistory returns events from history that are still within maxHistoryAge
// Must be called with lock held
func (c *SystemStreamController) getRecentHistory() []SystemEvent {
	now := time.Now()
	cutoff := now.Add(-c.maxHistoryAge)

	result := make([]SystemEvent, 0, len(c.eventHistory))
	for _, evt := range c.eventHistory {
		if evt.Timestamp.After(cutoff) {
			result = append(result, evt)
		}
	}
	return result
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
		c.logger.Debug("Event filtered by level",
			slog.String("level", string(evt.Level)),
			slog.String("min_level", string(c.minLevel)))
		return
	}

	atomic.AddInt64(&c.sequence, 1)

	c.mu.Lock()
	// Add to history
	c.addToHistory(evt)
	historySize := len(c.eventHistory)
	subscribers := make([]*systemSubscriber, 0, len(c.subscribers))
	for _, sub := range c.subscribers {
		subscribers = append(subscribers, sub)
	}
	c.mu.Unlock()

	c.logger.Info("Publishing system event",
		slog.String("level", string(evt.Level)),
		slog.String("source", evt.Source),
		slog.String("message", evt.Message),
		slog.Int("subscribers", len(subscribers)),
		slog.Int("history_size", historySize))

	// Fan out to subscribers without holding lock
	for _, sub := range subscribers {
		select {
		case sub.ch <- evt:
			c.logger.Debug("Event sent to subscriber",
				slog.Int64("subscriber", sub.id),
				slog.String("level", string(evt.Level)))
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

// addToHistory adds event to history buffer, pruning old events
// Must be called with lock held
func (c *SystemStreamController) addToHistory(evt SystemEvent) {
	c.eventHistory = append(c.eventHistory, evt)

	// Prune by size
	if len(c.eventHistory) > c.maxHistorySize {
		c.eventHistory = c.eventHistory[len(c.eventHistory)-c.maxHistorySize:]
	}

	// Prune by age
	cutoff := time.Now().Add(-c.maxHistoryAge)
	for i, e := range c.eventHistory {
		if e.Timestamp.After(cutoff) {
			if i > 0 {
				c.eventHistory = c.eventHistory[i:]
			}
			break
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
