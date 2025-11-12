package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
)

// Mock ThreeCommas client for testing
type mockThreeCommasClient struct {
	listBotsCalls      int
	getListOfDealsCalls int
	getDealForIDCalls  int

	listBotsErr      error
	getListOfDealsErr error
	getDealForIDErr  error
}

func (m *mockThreeCommasClient) ListBots(ctx context.Context, opts ...tc.ListBotsParamsOption) ([]tc.Bot, error) {
	m.listBotsCalls++
	if m.listBotsErr != nil {
		return nil, m.listBotsErr
	}
	return []tc.Bot{{Id: 1}}, nil
}

func (m *mockThreeCommasClient) GetListOfDeals(ctx context.Context, opts ...tc.ListDealsParamsOption) ([]tc.Deal, error) {
	m.getListOfDealsCalls++
	if m.getListOfDealsErr != nil {
		return nil, m.getListOfDealsErr
	}
	return []tc.Deal{{Id: 100, BotId: 1}}, nil
}

func (m *mockThreeCommasClient) GetDealForID(ctx context.Context, dealId tc.DealPathId) (*tc.Deal, error) {
	m.getDealForIDCalls++
	if m.getDealForIDErr != nil {
		return nil, m.getDealForIDErr
	}
	return &tc.Deal{Id: int(dealId), BotId: 1}, nil
}

func TestWithWorkflowID(t *testing.T) {
	ctx := context.Background()
	workflowID := "test-workflow-123"

	ctx = WithWorkflowID(ctx, workflowID)
	got := WorkflowIDFromContext(ctx)

	if got != workflowID {
		t.Errorf("WorkflowIDFromContext() = %v, want %v", got, workflowID)
	}
}

func TestWorkflowIDFromContext_NotSet(t *testing.T) {
	ctx := context.Background()
	got := WorkflowIDFromContext(ctx)

	if got != "" {
		t.Errorf("WorkflowIDFromContext() = %v, want empty string", got)
	}
}

func TestClient_ListBots(t *testing.T) {
	mock := &mockThreeCommasClient{}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	workflowID := "test-workflow"
	ctx := context.Background()

	// Reserve first
	err := limiter.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	defer limiter.Release(workflowID)

	// Add workflow ID to context
	ctx = WithWorkflowID(ctx, workflowID)

	// Call ListBots
	bots, err := client.ListBots(ctx, tc.WithScopeForListBots(tc.Enabled))
	if err != nil {
		t.Fatalf("ListBots failed: %v", err)
	}

	if len(bots) != 1 {
		t.Errorf("Expected 1 bot, got %d", len(bots))
	}

	if mock.listBotsCalls != 1 {
		t.Errorf("Expected 1 ListBots call, got %d", mock.listBotsCalls)
	}

	// Check that consumption happened
	consumed, _, _, _ := limiter.Stats()
	if consumed != 1 {
		t.Errorf("Expected 1 consumed slot, got %d", consumed)
	}
}

func TestClient_ListBots_NoWorkflowID(t *testing.T) {
	mock := &mockThreeCommasClient{}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	ctx := context.Background()
	// Don't add workflow ID to context

	// Call should fail
	_, err := client.ListBots(ctx)
	if err == nil {
		t.Fatal("Expected error when workflow ID not in context")
	}

	if mock.listBotsCalls != 0 {
		t.Errorf("Expected 0 ListBots calls, got %d", mock.listBotsCalls)
	}
}

func TestClient_GetListOfDeals(t *testing.T) {
	mock := &mockThreeCommasClient{}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	workflowID := "test-workflow"
	ctx := context.Background()

	// Reserve first
	err := limiter.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	defer limiter.Release(workflowID)

	// Add workflow ID to context
	ctx = WithWorkflowID(ctx, workflowID)

	// Call GetListOfDeals
	deals, err := client.GetListOfDeals(ctx, tc.WithBotIdForListDeals(1))
	if err != nil {
		t.Fatalf("GetListOfDeals failed: %v", err)
	}

	if len(deals) != 1 {
		t.Errorf("Expected 1 deal, got %d", len(deals))
	}

	if mock.getListOfDealsCalls != 1 {
		t.Errorf("Expected 1 GetListOfDeals call, got %d", mock.getListOfDealsCalls)
	}

	// Check that consumption happened
	consumed, _, _, _ := limiter.Stats()
	if consumed != 1 {
		t.Errorf("Expected 1 consumed slot, got %d", consumed)
	}
}

func TestClient_GetDealForID(t *testing.T) {
	mock := &mockThreeCommasClient{}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	workflowID := "test-workflow"
	ctx := context.Background()

	// Reserve first
	err := limiter.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	defer limiter.Release(workflowID)

	// Add workflow ID to context
	ctx = WithWorkflowID(ctx, workflowID)

	// Call GetDealForID
	deal, err := client.GetDealForID(ctx, 100)
	if err != nil {
		t.Fatalf("GetDealForID failed: %v", err)
	}

	if deal.Id != 100 {
		t.Errorf("Expected deal ID 100, got %d", deal.Id)
	}

	if mock.getDealForIDCalls != 1 {
		t.Errorf("Expected 1 GetDealForID call, got %d", mock.getDealForIDCalls)
	}

	// Check that consumption happened
	consumed, _, _, _ := limiter.Stats()
	if consumed != 1 {
		t.Errorf("Expected 1 consumed slot, got %d", consumed)
	}
}

func TestClient_ConsumeExceedsReservation(t *testing.T) {
	mock := &mockThreeCommasClient{}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	workflowID := "test-workflow"
	ctx := context.Background()

	// Reserve only 2 slots
	err := limiter.Reserve(ctx, workflowID, 2)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	defer limiter.Release(workflowID)

	// Add workflow ID to context
	ctx = WithWorkflowID(ctx, workflowID)

	// First two calls should succeed
	_, err = client.GetDealForID(ctx, 1)
	if err != nil {
		t.Fatalf("First GetDealForID failed: %v", err)
	}

	_, err = client.GetDealForID(ctx, 2)
	if err != nil {
		t.Fatalf("Second GetDealForID failed: %v", err)
	}

	// Third call should fail (exceeds reservation)
	_, err = client.GetDealForID(ctx, 3)
	if err == nil {
		t.Fatal("Expected error when exceeding reservation")
	}
	if !errors.Is(err, ErrConsumeExceedsLimit) {
		// The error is wrapped, so check the message
		if mock.getDealForIDCalls == 3 {
			t.Error("API call was made even though reservation was exceeded")
		}
	}
}

func TestClient_MultipleCalls(t *testing.T) {
	mock := &mockThreeCommasClient{}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	workflowID := "test-workflow"
	ctx := context.Background()

	// Reserve enough for all calls
	err := limiter.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	defer limiter.Release(workflowID)

	// Add workflow ID to context
	ctx = WithWorkflowID(ctx, workflowID)

	// Make multiple calls
	_, err = client.ListBots(ctx)
	if err != nil {
		t.Fatalf("ListBots failed: %v", err)
	}

	_, err = client.GetListOfDeals(ctx, tc.WithBotIdForListDeals(1))
	if err != nil {
		t.Fatalf("GetListOfDeals failed: %v", err)
	}

	_, err = client.GetDealForID(ctx, 100)
	if err != nil {
		t.Fatalf("GetDealForID failed: %v", err)
	}

	// Check total consumption
	consumed, _, _, _ := limiter.Stats()
	if consumed != 3 {
		t.Errorf("Expected 3 consumed slots, got %d", consumed)
	}

	// Verify all mock calls were made
	if mock.listBotsCalls != 1 {
		t.Errorf("Expected 1 ListBots call, got %d", mock.listBotsCalls)
	}
	if mock.getListOfDealsCalls != 1 {
		t.Errorf("Expected 1 GetListOfDeals call, got %d", mock.getListOfDealsCalls)
	}
	if mock.getDealForIDCalls != 1 {
		t.Errorf("Expected 1 GetDealForID call, got %d", mock.getDealForIDCalls)
	}
}

func TestClient_Limiter(t *testing.T) {
	mock := &mockThreeCommasClient{}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	// Test that we can access the underlying limiter
	got := client.Limiter()
	if got != limiter {
		t.Error("Limiter() did not return the expected limiter instance")
	}
}

func TestClient_APIError(t *testing.T) {
	mock := &mockThreeCommasClient{
		getDealForIDErr: errors.New("API error"),
	}
	limiter := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})
	client := NewClient(mock, limiter)

	workflowID := "test-workflow"
	ctx := context.Background()

	// Reserve first
	err := limiter.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	defer limiter.Release(workflowID)

	// Add workflow ID to context
	ctx = WithWorkflowID(ctx, workflowID)

	// Call GetDealForID (should consume slot even though API fails)
	_, err = client.GetDealForID(ctx, 100)
	if err == nil {
		t.Fatal("Expected API error")
	}

	// Slot should still be consumed
	consumed, _, _, _ := limiter.Stats()
	if consumed != 1 {
		t.Errorf("Expected 1 consumed slot even after API error, got %d", consumed)
	}
}
