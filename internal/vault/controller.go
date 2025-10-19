package vault

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrInvalidTransition is returned when an invalid state change is attempted.
var ErrInvalidTransition = errors.New("invalid vault state transition")

// Controller orchestrates the in-memory vault state lifecycle.
type Controller struct {
	mu  sync.RWMutex
	now func() time.Time

	state            State
	user             *User
	sealedAt         *time.Time
	unsealedAt       *time.Time
	sessionExpiresAt *time.Time

	secrets     *Secrets
	ready       chan struct{}
	readyClosed bool
}

// NewController creates a new controller using the provided initial state.
func NewController(initial State, opts ...ControllerOption) *Controller {
	cfg := controllerConfig{now: time.Now}
	for _, opt := range opts {
		opt(&cfg)
	}

	c := &Controller{
		now:   cfg.now,
		state: initial,
	}
	if cfg.user != nil {
		cloned := *cfg.user
		c.user = &cloned
	}
	if cfg.sealedAt != nil {
		ts := cfg.sealedAt.UTC()
		c.sealedAt = &ts
	}
	if cfg.unsealedAt != nil {
		ts := cfg.unsealedAt.UTC()
		c.unsealedAt = &ts
	}
	if cfg.sessionExpiresAt != nil {
		ts := cfg.sessionExpiresAt.UTC()
		c.sessionExpiresAt = &ts
	}
	if cfg.secrets != nil {
		cloned := cfg.secrets.Clone()
		c.secrets = &cloned
	}

	c.ready = make(chan struct{})
	if initial == StateUnsealed {
		close(c.ready)
		c.readyClosed = true
	}
	return c
}

type controllerConfig struct {
	now              func() time.Time
	user             *User
	sealedAt         *time.Time
	unsealedAt       *time.Time
	sessionExpiresAt *time.Time
	secrets          *Secrets
}

// ControllerOption mutates the configuration passed to NewController.
type ControllerOption func(*controllerConfig)

// WithClock overrides the clock used by the controller.
func WithClock(fn func() time.Time) ControllerOption {
	return func(cfg *controllerConfig) {
		if fn != nil {
			cfg.now = fn
		}
	}
}

// WithInitialUser seeds the controller with an existing user.
func WithInitialUser(user *User) ControllerOption {
	return func(cfg *controllerConfig) {
		cfg.user = user
	}
}

// WithInitialTimestamps seeds sealed/unsealed timestamps.
func WithInitialTimestamps(sealedAt, unsealedAt, sessionExpiresAt *time.Time) ControllerOption {
	return func(cfg *controllerConfig) {
		cfg.sealedAt = sealedAt
		cfg.unsealedAt = unsealedAt
		cfg.sessionExpiresAt = sessionExpiresAt
	}
}

// WithInitialSecrets seeds the controller with decrypted secrets.
func WithInitialSecrets(secrets *Secrets) ControllerOption {
	return func(cfg *controllerConfig) {
		cfg.secrets = secrets
	}
}

// State returns the current vault state.
func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Status returns a snapshot of the controller state.
func (c *Controller) Status() ControllerStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := ControllerStatus{State: c.state}
	if c.user != nil {
		cloned := *c.user
		status.User = &cloned
	}
	if c.sealedAt != nil {
		ts := *c.sealedAt
		status.SealedAt = &ts
	}
	if c.unsealedAt != nil {
		ts := *c.unsealedAt
		status.UnsealedAt = &ts
	}
	if c.sessionExpiresAt != nil {
		ts := *c.sessionExpiresAt
		status.SessionExpiresAt = &ts
	}
	return status
}

// Secrets returns a clone of the decrypted secrets if present.
func (c *Controller) Secrets() *Secrets {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.secrets == nil {
		return nil
	}
	cloned := c.secrets.Clone()
	return &cloned
}

// SetUser records the current vault user metadata.
func (c *Controller) SetUser(user *User) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if user == nil {
		c.user = nil
		return
	}
	cloned := *user
	c.user = &cloned
}

// RequireSetup transitions the controller into the setup_required state.
func (c *Controller) RequireSetup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == StateSetupRequired {
		return nil
	}
	c.resetReadyLocked()
	c.state = StateSetupRequired
	c.secrets = nil
	c.sessionExpiresAt = nil
	now := c.now().UTC()
	c.sealedAt = &now
	c.unsealedAt = nil
	return nil
}

// Seal clears the decrypted secrets and transitions the controller to the sealed state.
func (c *Controller) Seal() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case StateSealed:
		return nil
	case StateSetupRequired, StateUnsealed:
		// allow transition
	default:
		return ErrInvalidTransition
	}

	c.resetReadyLocked()
	c.state = StateSealed
	c.secrets = nil
	c.sessionExpiresAt = nil
	now := c.now().UTC()
	c.sealedAt = &now
	return nil
}

// Unseal stores the decrypted secrets and marks the controller as ready.
func (c *Controller) Unseal(secrets Secrets, sessionExpiresAt *time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case StateSealed, StateSetupRequired:
		// valid transitions
	case StateUnsealed:
		// allow refreshing secrets while already unsealed
	default:
		return ErrInvalidTransition
	}

	cloned := secrets.Clone()
	c.secrets = &cloned
	if sessionExpiresAt != nil {
		ts := sessionExpiresAt.UTC()
		c.sessionExpiresAt = &ts
	} else {
		c.sessionExpiresAt = nil
	}
	now := c.now().UTC()
	c.unsealedAt = &now
	c.state = StateUnsealed
	c.signalReadyLocked()
	return nil
}

// WaitUntilUnsealed blocks until the controller reaches the unsealed state or the context is cancelled.
func (c *Controller) WaitUntilUnsealed(ctx context.Context) error {
	c.mu.RLock()
	if c.state == StateUnsealed {
		c.mu.RUnlock()
		return nil
	}
	ready := c.ready
	c.mu.RUnlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ready:
		return nil
	}
}

func (c *Controller) signalReadyLocked() {
	if !c.readyClosed {
		close(c.ready)
		c.readyClosed = true
	}
}

func (c *Controller) resetReadyLocked() {
	if c.readyClosed {
		c.ready = make(chan struct{})
		c.readyClosed = false
	} else if c.ready == nil {
		c.ready = make(chan struct{})
	}
}
