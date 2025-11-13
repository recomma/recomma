package ratelimit

import (
	"context"
	"fmt"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
)

// contextKey is an unexported type for context keys to avoid collisions
type contextKey int

const (
	workflowIDKey contextKey = iota
)

// WithWorkflowID adds a workflow ID to the context
func WithWorkflowID(ctx context.Context, workflowID string) context.Context {
	return context.WithValue(ctx, workflowIDKey, workflowID)
}

// WorkflowIDFromContext retrieves the workflow ID from the context
func WorkflowIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(workflowIDKey).(string); ok {
		return id
	}
	return ""
}

// ThreeCommasAPI defines the interface for ThreeCommas client operations
type ThreeCommasAPI interface {
	ListBots(ctx context.Context, opts ...tc.ListBotsParamsOption) ([]tc.Bot, error)
	GetListOfDeals(ctx context.Context, opts ...tc.ListDealsParamsOption) ([]tc.Deal, error)
	GetDealForID(ctx context.Context, dealId tc.DealPathId) (*tc.Deal, error)
}

// Client wraps a ThreeCommas client with rate limiting
type Client struct {
	client  ThreeCommasAPI
	limiter *Limiter
}

// NewClient creates a new rate-limited ThreeCommas client
func NewClient(client ThreeCommasAPI, limiter *Limiter) *Client {
	return &Client{
		client:  client,
		limiter: limiter,
	}
}

// ListBots lists bots with rate limiting
func (c *Client) ListBots(ctx context.Context, opts ...tc.ListBotsParamsOption) ([]tc.Bot, error) {
	workflowID := WorkflowIDFromContext(ctx)
	if workflowID == "" {
		return nil, fmt.Errorf("workflow ID not found in context for ListBots")
	}

	// Consume one slot before making the API call
	if err := c.limiter.ConsumeWithOperation(workflowID, "ListBots"); err != nil {
		return nil, fmt.Errorf("rate limit consume: %w", err)
	}

	return c.client.ListBots(ctx, opts...)
}

// GetListOfDeals gets list of deals with rate limiting
func (c *Client) GetListOfDeals(ctx context.Context, opts ...tc.ListDealsParamsOption) ([]tc.Deal, error) {
	workflowID := WorkflowIDFromContext(ctx)
	if workflowID == "" {
		return nil, fmt.Errorf("workflow ID not found in context for GetListOfDeals")
	}

	// Consume one slot before making the API call
	if err := c.limiter.ConsumeWithOperation(workflowID, "GetListOfDeals"); err != nil {
		return nil, fmt.Errorf("rate limit consume: %w", err)
	}

	return c.client.GetListOfDeals(ctx, opts...)
}

// GetDealForID gets a specific deal with rate limiting
func (c *Client) GetDealForID(ctx context.Context, dealId tc.DealPathId) (*tc.Deal, error) {
	workflowID := WorkflowIDFromContext(ctx)
	if workflowID == "" {
		return nil, fmt.Errorf("workflow ID not found in context for GetDealForID")
	}

	// Consume one slot before making the API call
	if err := c.limiter.ConsumeWithOperation(workflowID, "GetDealForID"); err != nil {
		return nil, fmt.Errorf("rate limit consume: %w", err)
	}

	return c.client.GetDealForID(ctx, dealId)
}

// Limiter returns the underlying rate limiter (useful for direct Reserve/Release operations)
func (c *Client) Limiter() *Limiter {
	return c.limiter
}
