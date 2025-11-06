package emitter

import (
	"context"
	"sync"
	"time"
)

// RateGate coordinates pacing for emitters that share upstream rate limits.
// Implementations must be safe for concurrent use by multiple goroutines.
type RateGate interface {
	Wait(ctx context.Context) error
	Cooldown(d time.Duration)
}

const defaultRateGateSpacing = 300 * time.Millisecond

// NewRateGate returns a RateGate that enforces a minimum spacing between
// operations. A non-positive spacing falls back to a sensible default.
func NewRateGate(minSpacing time.Duration) RateGate {
	if minSpacing <= 0 {
		minSpacing = defaultRateGateSpacing
	}
	return &rateGate{
		minSpacing: minSpacing,
		next:       time.Now(),
	}
}

type rateGate struct {
	mu         sync.Mutex
	minSpacing time.Duration
	next       time.Time
}

func (g *rateGate) Wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		wait := time.Until(g.next)
		if wait <= 0 {
			g.next = time.Now().Add(g.minSpacing)
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		}
	}
}

func (g *rateGate) Cooldown(d time.Duration) {
	if d <= 0 {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	next := time.Now().Add(d)
	if next.After(g.next) {
		g.next = next
	}
}
