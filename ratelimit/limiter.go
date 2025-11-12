package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var (
	ErrInvalidWorkflowID    = errors.New("invalid workflow ID")
	ErrReservationNotHeld   = errors.New("reservation not held by this workflow")
	ErrAlreadyReserved      = errors.New("workflow already has active reservation")
	ErrInsufficientCapacity = errors.New("insufficient capacity for reservation")
	ErrConsumeExceedsLimit  = errors.New("consume would exceed reserved slots")
	ErrAdjustBelowConsumed  = errors.New("cannot adjust below already consumed slots")
	ErrExtendNegative       = errors.New("cannot extend by negative amount")
)

// Config holds rate limiter configuration
type Config struct {
	RequestsPerMinute int
	WindowDuration    time.Duration // Defaults to 60 seconds if zero
	PrioritySlots     int           // Reserved for priority operations (future use)
	Logger            *slog.Logger  // Optional logger
}

// Limiter implements a fixed-window rate limiter with workflow reservation support
type Limiter struct {
	mu sync.Mutex

	// Configuration
	limit         int
	window        time.Duration
	prioritySlots int
	logger        *slog.Logger

	// Window state
	windowStart time.Time
	consumed    int

	// Reservation state - supports multiple concurrent reservations
	activeReservations map[string]*reservation
	waitQueue          []waitingWorkflow
}

type reservation struct {
	workflowID    string
	slotsReserved int
	slotsConsumed int
	createdAt     time.Time
	completed     bool
}

type waitingWorkflow struct {
	workflowID     string
	requestedSlots int
	createdAt      time.Time
	ready          chan struct{}
}

// NewLimiter creates a new rate limiter with the given configuration
func NewLimiter(cfg Config) *Limiter {
	if cfg.WindowDuration <= 0 {
		cfg.WindowDuration = 60 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &Limiter{
		limit:              cfg.RequestsPerMinute,
		window:             cfg.WindowDuration,
		prioritySlots:      cfg.PrioritySlots,
		logger:             cfg.Logger.WithGroup("ratelimit"),
		windowStart:        time.Now(),
		consumed:           0,
		activeReservations: make(map[string]*reservation),
	}
}

// Reserve requests N slots for a workflow. Blocks until granted or context cancelled.
func (l *Limiter) Reserve(ctx context.Context, workflowID string, count int) error {
	if workflowID == "" {
		return ErrInvalidWorkflowID
	}
	if count <= 0 {
		return fmt.Errorf("reservation count must be positive: %d", count)
	}

	l.mu.Lock()

	// Check if this workflow already has an active reservation
	if _, exists := l.activeReservations[workflowID]; exists {
		l.mu.Unlock()
		return ErrAlreadyReserved
	}

	// Calculate total reserved slots across all active reservations
	totalReserved := l.calculateTotalReserved()

	// Check if we have capacity, grant immediately
	l.resetWindowIfNeeded()
	if l.consumed+totalReserved+count <= l.limit {
		l.activeReservations[workflowID] = &reservation{
			workflowID:    workflowID,
			slotsReserved: count,
			createdAt:     time.Now(),
		}
		l.logger.Info("rate limit reserve granted",
			slog.String("workflow_id", workflowID),
			slog.Int("slots_reserved", count),
			slog.Int("window_consumed", l.consumed),
			slog.Int("total_reserved", totalReserved+count),
			slog.Int("window_limit", l.limit),
			slog.Duration("wait_duration", 0),
		)
		l.mu.Unlock()
		return nil
	}

	// Need to wait - add to queue
	queuePos := len(l.waitQueue)
	ready := make(chan struct{})
	l.waitQueue = append(l.waitQueue, waitingWorkflow{
		workflowID:     workflowID,
		requestedSlots: count,
		createdAt:      time.Now(),
		ready:          ready,
	})

	activeWorkflows := make([]string, 0, len(l.activeReservations))
	for wf := range l.activeReservations {
		activeWorkflows = append(activeWorkflows, wf)
	}

	l.logger.Info("rate limit reserve attempt",
		slog.String("workflow_id", workflowID),
		slog.Int("requested_slots", count),
		slog.Int("window_consumed", l.consumed),
		slog.Int("total_reserved", totalReserved),
		slog.Int("window_limit", l.limit),
		slog.Any("active_workflows", activeWorkflows),
		slog.Int("queue_position", queuePos),
	)
	l.mu.Unlock()

	// Wait for our turn or context cancellation
	startWait := time.Now()
	select {
	case <-ready:
		waitDuration := time.Since(startWait)
		if waitDuration > 30*time.Second {
			l.logger.Warn("rate limit queue wait exceeded threshold",
				slog.String("workflow_id", workflowID),
				slog.Duration("wait_duration", waitDuration),
				slog.Int("queue_position", queuePos),
			)
		}
		return nil
	case <-ctx.Done():
		// Remove from queue
		l.mu.Lock()
		l.removeFromQueue(workflowID)
		l.mu.Unlock()
		return ctx.Err()
	}
}

// Consume marks one slot as consumed (called immediately before each API call)
func (l *Limiter) Consume(workflowID string) error {
	return l.ConsumeWithOperation(workflowID, "")
}

// ConsumeWithOperation marks one slot as consumed with operation name for logging
func (l *Limiter) ConsumeWithOperation(workflowID string, operation string) error {
	if workflowID == "" {
		return ErrInvalidWorkflowID
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	res, exists := l.activeReservations[workflowID]
	if !exists {
		return ErrReservationNotHeld
	}

	if res.slotsConsumed >= res.slotsReserved {
		return ErrConsumeExceedsLimit
	}

	l.resetWindowIfNeeded()

	l.consumed++
	res.slotsConsumed++

	attrs := []any{
		slog.String("workflow_id", workflowID),
		slog.Int("slots_consumed", res.slotsConsumed),
		slog.Int("window_consumed", l.consumed),
		slog.Int("window_limit", l.limit),
	}
	if operation != "" {
		attrs = append(attrs, slog.String("operation", operation))
	}
	l.logger.Debug("rate limit consume", attrs...)

	return nil
}

// AdjustDown reduces reservation size when actual needs are known
func (l *Limiter) AdjustDown(workflowID string, newTotal int) error {
	if workflowID == "" {
		return ErrInvalidWorkflowID
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	res, exists := l.activeReservations[workflowID]
	if !exists {
		return ErrReservationNotHeld
	}

	if newTotal < res.slotsConsumed {
		return ErrAdjustBelowConsumed
	}

	previousReservation := res.slotsReserved
	if newTotal >= previousReservation {
		// No adjustment needed
		return nil
	}

	freedCapacity := previousReservation - newTotal
	res.slotsReserved = newTotal

	l.logger.Info("rate limit adjust down",
		slog.String("workflow_id", workflowID),
		slog.Int("previous_reservation", previousReservation),
		slog.Int("new_reservation", newTotal),
		slog.Int("slots_consumed", res.slotsConsumed),
		slog.Int("freed_capacity", freedCapacity),
	)

	// Try to grant waiting workflows with the freed capacity
	l.tryGrantWaiting()

	return nil
}

// Extend requests additional slots beyond current reservation
func (l *Limiter) Extend(ctx context.Context, workflowID string, additional int) error {
	if workflowID == "" {
		return ErrInvalidWorkflowID
	}
	if additional < 0 {
		return ErrExtendNegative
	}
	if additional == 0 {
		return nil
	}

	l.mu.Lock()

	res, exists := l.activeReservations[workflowID]
	if !exists {
		l.mu.Unlock()
		return ErrReservationNotHeld
	}

	currentReservation := res.slotsReserved
	newReservation := currentReservation + additional

	// Check if we have capacity in current window
	l.resetWindowIfNeeded()
	totalReserved := l.calculateTotalReserved()
	if l.consumed+totalReserved-res.slotsReserved+newReservation <= l.limit {
		res.slotsReserved = newReservation
		l.logger.Info("rate limit extend granted",
			slog.String("workflow_id", workflowID),
			slog.Int("current_reservation", currentReservation),
			slog.Int("additional_slots", additional),
			slog.Int("new_reservation", newReservation),
			slog.Int("window_consumed", l.consumed),
			slog.Int("window_limit", l.limit),
		)
		l.mu.Unlock()
		return nil
	}

	// Need to wait for window reset
	l.logger.Info("rate limit extend attempt",
		slog.String("workflow_id", workflowID),
		slog.Int("current_reservation", currentReservation),
		slog.Int("additional_slots", additional),
		slog.Int("window_consumed", l.consumed),
		slog.Int("window_limit", l.limit),
		slog.Bool("will_wait_for_reset", true),
	)

	nextWindow := l.windowStart.Add(l.window)
	l.mu.Unlock()

	// Wait for window reset
	waitDuration := time.Until(nextWindow)
	if waitDuration > 0 {
		timer := time.NewTimer(waitDuration)
		select {
		case <-timer.C:
			// Window has reset, try again
			l.mu.Lock()
			defer l.mu.Unlock()

			l.resetWindowIfNeeded()
			totalReserved := l.calculateTotalReserved()
			if l.consumed+totalReserved-res.slotsReserved+newReservation <= l.limit {
				res.slotsReserved = newReservation
				return nil
			}
			return ErrInsufficientCapacity
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		}
	}

	// Window already reset, try once more
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resetWindowIfNeeded()
	totalReserved = l.calculateTotalReserved()
	if l.consumed+totalReserved-res.slotsReserved+newReservation <= l.limit {
		res.slotsReserved = newReservation
		return nil
	}
	return ErrInsufficientCapacity
}

// SignalComplete indicates no more API calls will be made
func (l *Limiter) SignalComplete(workflowID string) error {
	if workflowID == "" {
		return ErrInvalidWorkflowID
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	res, exists := l.activeReservations[workflowID]
	if !exists {
		return ErrReservationNotHeld
	}

	res.completed = true

	slotsWasted := res.slotsReserved - res.slotsConsumed
	l.logger.Info("rate limit workflow complete",
		slog.String("workflow_id", workflowID),
		slog.Int("slots_reserved", res.slotsReserved),
		slog.Int("slots_consumed", res.slotsConsumed),
		slog.Int("slots_wasted", slotsWasted),
	)

	// Try to grant waiting workflows
	l.tryGrantWaiting()

	return nil
}

// Release frees the reservation entirely
func (l *Limiter) Release(workflowID string) error {
	if workflowID == "" {
		return ErrInvalidWorkflowID
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	res, exists := l.activeReservations[workflowID]
	if !exists {
		return ErrReservationNotHeld
	}

	duration := time.Since(res.createdAt)
	slotsReserved := res.slotsReserved
	slotsConsumed := res.slotsConsumed

	nextInQueue := "<none>"
	if len(l.waitQueue) > 0 {
		nextInQueue = l.waitQueue[0].workflowID
	}

	l.logger.Info("rate limit release",
		slog.String("workflow_id", workflowID),
		slog.Duration("duration", duration),
		slog.Int("slots_reserved", slotsReserved),
		slog.Int("slots_consumed", slotsConsumed),
		slog.String("next_in_queue", nextInQueue),
	)

	delete(l.activeReservations, workflowID)

	// Try to grant next waiting workflow
	l.tryGrantWaiting()

	return nil
}

// Stats returns current rate limiter statistics (for testing/monitoring)
func (l *Limiter) Stats() (consumed, limit, queueLen int, hasReservation bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.resetWindowIfNeeded()
	return l.consumed, l.limit, len(l.waitQueue), len(l.activeReservations) > 0
}

// resetWindowIfNeeded resets the window if we've passed the boundary (must be called with lock held)
func (l *Limiter) resetWindowIfNeeded() {
	now := time.Now()
	if now.Sub(l.windowStart) >= l.window {
		previousConsumed := l.consumed
		utilizationPct := 0
		if l.limit > 0 {
			utilizationPct = (previousConsumed * 100) / l.limit
		}

		activeWorkflows := make([]string, 0, len(l.activeReservations))
		for wf := range l.activeReservations {
			activeWorkflows = append(activeWorkflows, wf)
		}

		l.logger.Info("rate limit window reset",
			slog.Int("previous_window_consumed", previousConsumed),
			slog.Int("previous_window_limit", l.limit),
			slog.Int("utilization_pct", utilizationPct),
			slog.Any("active_workflows", activeWorkflows),
			slog.Int("queue_length", len(l.waitQueue)),
		)

		l.windowStart = now
		l.consumed = 0

		// Active reservations are NOT cancelled, workflows continue
		// Try to grant waiting workflows now that window has reset
		l.tryGrantWaiting()
	}
}

// tryGrantWaiting attempts to grant reservations to waiting workflows (must be called with lock held)
func (l *Limiter) tryGrantWaiting() {
	// Process queue in FIFO order, granting as many as capacity allows
	for len(l.waitQueue) > 0 {
		next := l.waitQueue[0]

		l.resetWindowIfNeeded()

		// Calculate total reserved slots across all active reservations
		totalReserved := l.calculateTotalReserved()

		// Check if we have capacity for this workflow
		if l.consumed+totalReserved+next.requestedSlots <= l.limit {
			// Grant the reservation
			l.activeReservations[next.workflowID] = &reservation{
				workflowID:    next.workflowID,
				slotsReserved: next.requestedSlots,
				createdAt:     time.Now(),
			}

			waitDuration := time.Since(next.createdAt)
			l.logger.Info("rate limit reserve granted from queue",
				slog.String("workflow_id", next.workflowID),
				slog.Int("slots_reserved", next.requestedSlots),
				slog.Int("window_consumed", l.consumed),
				slog.Int("total_reserved", totalReserved+next.requestedSlots),
				slog.Int("window_limit", l.limit),
				slog.Duration("wait_duration", waitDuration),
				slog.Int("concurrent_workflows", len(l.activeReservations)),
			)

			// Remove from queue and signal
			l.waitQueue = l.waitQueue[1:]
			close(next.ready)
		} else {
			// Not enough capacity yet, stop trying
			break
		}
	}
}

// calculateTotalReserved sums up all reserved slots across active reservations (must be called with lock held)
func (l *Limiter) calculateTotalReserved() int {
	total := 0
	for _, res := range l.activeReservations {
		total += res.slotsReserved
	}
	return total
}

// removeFromQueue removes a workflow from the wait queue (must be called with lock held)
func (l *Limiter) removeFromQueue(workflowID string) {
	for i, w := range l.waitQueue {
		if w.workflowID == workflowID {
			l.waitQueue = append(l.waitQueue[:i], l.waitQueue[i+1:]...)
			return
		}
	}
}
