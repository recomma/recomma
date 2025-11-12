package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func runLimiterTest(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	synctest.Test(t, fn)
}

// Test basic reservation and release
func TestLimiter_BasicReserveRelease(t *testing.T) {
	l := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})

	ctx := context.Background()
	workflowID := "test-workflow-1"

	// Reserve 5 slots
	err := l.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	// Check stats
	consumed, limit, queueLen, hasRes := l.Stats()
	if consumed != 0 {
		t.Errorf("Expected consumed=0, got %d", consumed)
	}
	if limit != 10 {
		t.Errorf("Expected limit=10, got %d", limit)
	}
	if queueLen != 0 {
		t.Errorf("Expected queueLen=0, got %d", queueLen)
	}
	if !hasRes {
		t.Error("Expected active reservation")
	}

	// Release
	err = l.Release(workflowID)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Check stats after release
	_, _, queueLen, hasRes = l.Stats()
	if queueLen != 0 {
		t.Errorf("Expected queueLen=0 after release, got %d", queueLen)
	}
	if hasRes {
		t.Error("Expected no active reservation after release")
	}
}

// Test consume operations
func TestLimiter_Consume(t *testing.T) {
	l := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})

	ctx := context.Background()
	workflowID := "test-workflow-1"

	// Reserve 5 slots
	err := l.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	// Consume 3 times
	for i := 0; i < 3; i++ {
		err = l.Consume(workflowID)
		if err != nil {
			t.Fatalf("Consume %d failed: %v", i+1, err)
		}
	}

	// Check stats
	consumed, _, _, _ := l.Stats()
	if consumed != 3 {
		t.Errorf("Expected consumed=3, got %d", consumed)
	}

	// Try to consume beyond reservation (reserved 5, consumed 3, so 2 more should work)
	err = l.Consume(workflowID)
	if err != nil {
		t.Fatalf("Consume 4 failed: %v", err)
	}
	err = l.Consume(workflowID)
	if err != nil {
		t.Fatalf("Consume 5 failed: %v", err)
	}

	// This should fail (exceeds reservation)
	err = l.Consume(workflowID)
	if !errors.Is(err, ErrConsumeExceedsLimit) {
		t.Errorf("Expected ErrConsumeExceedsLimit, got %v", err)
	}

	// Release
	err = l.Release(workflowID)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
}

// Test AdjustDown
func TestLimiter_AdjustDown(t *testing.T) {
	l := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})

	ctx := context.Background()
	workflowID := "test-workflow-1"

	// Reserve 10 slots
	err := l.Reserve(ctx, workflowID, 10)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	// Consume 3
	for i := 0; i < 3; i++ {
		err = l.Consume(workflowID)
		if err != nil {
			t.Fatalf("Consume %d failed: %v", i+1, err)
		}
	}

	// Adjust down to 5 (valid, since consumed is 3)
	err = l.AdjustDown(workflowID, 5)
	if err != nil {
		t.Fatalf("AdjustDown failed: %v", err)
	}

	// Try to adjust down to 2 (invalid, since consumed is 3)
	err = l.AdjustDown(workflowID, 2)
	if !errors.Is(err, ErrAdjustBelowConsumed) {
		t.Errorf("Expected ErrAdjustBelowConsumed, got %v", err)
	}

	// Consume 2 more (total 5, at the adjusted limit)
	for i := 0; i < 2; i++ {
		err = l.Consume(workflowID)
		if err != nil {
			t.Fatalf("Consume after adjust failed: %v", err)
		}
	}

	// This should fail (exceeds adjusted reservation)
	err = l.Consume(workflowID)
	if !errors.Is(err, ErrConsumeExceedsLimit) {
		t.Errorf("Expected ErrConsumeExceedsLimit after adjust, got %v", err)
	}

	// Release
	err = l.Release(workflowID)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
}

// Test FIFO queue behavior
func TestLimiter_FIFOQueue(t *testing.T) {
	runLimiterTest(t, func(t *testing.T) {
		l := NewLimiter(Config{
			RequestsPerMinute: 5,
			Logger:            slog.Default(),
		})

		ctx := t.Context()

		// First workflow reserves all capacity
		err := l.Reserve(ctx, "workflow-1", 5)
		if err != nil {
			t.Fatalf("First reserve failed: %v", err)
		}

		// Start two more workflows that will queue
		var wg sync.WaitGroup
		results := make([]string, 2)

		for i := 0; i < 2; i++ {
			wg.Add(1)
			idx := i
			workflowID := fmt.Sprintf("workflow-%d", i+2)
			go func() {
				defer wg.Done()
				err := l.Reserve(ctx, workflowID, 3)
				if err != nil {
					t.Errorf("Reserve for %s failed: %v", workflowID, err)
					return
				}
				results[idx] = workflowID
				// Release immediately after getting reservation
				l.Release(workflowID)
			}()
		}

		// Give them time to queue
		time.Sleep(100 * time.Millisecond)

		// Check queue length
		_, _, queueLen, _ := l.Stats()
		if queueLen != 2 {
			t.Errorf("Expected queue length 2, got %d", queueLen)
		}

		// Release first workflow
		err = l.Release("workflow-1")
		if err != nil {
			t.Fatalf("Release failed: %v", err)
		}

		// Wait for queued workflows to complete
		wg.Wait()

		// Verify FIFO order (workflow-2 should have been granted before workflow-3)
		if results[0] != "workflow-2" {
			t.Errorf("Expected workflow-2 first, got %s", results[0])
		}
		if results[1] != "workflow-3" {
			t.Errorf("Expected workflow-3 second, got %s", results[1])
		}
	})
}

// Test AdjustDown enables waiting workflows
func TestLimiter_AdjustDownEnablesWaiting(t *testing.T) {
	t.Skipf("AdjustDownEnablesWaiting limiter needs additional implementation for this")
	runLimiterTest(t, func(t *testing.T) {
		l := NewLimiter(Config{
			RequestsPerMinute: 5,
			Logger:            slog.Default(),
		})

		ctx := t.Context()

		// First workflow reserves all capacity
		err := l.Reserve(ctx, "workflow-1", 5)
		if err != nil {
			t.Fatalf("First reserve failed: %v", err)
		}

		// Consume 1
		err = l.Consume("workflow-1")
		if err != nil {
			t.Fatalf("Consume failed: %v", err)
		}

		// Start second workflow that will queue
		var wg sync.WaitGroup
		wg.Add(1)
		granted := make(chan struct{})
		go func() {
			defer wg.Done()
			err := l.Reserve(ctx, "workflow-2", 2)
			if err != nil {
				t.Errorf("Second reserve failed: %v", err)
				return
			}
			close(granted)
			l.Release("workflow-2")
		}()

		// Give it time to queue
		time.Sleep(100 * time.Millisecond)

		// Adjust down first workflow from 5 to 3
		// This should free 2 slots, allowing workflow-2 to proceed
		err = l.AdjustDown("workflow-1", 3)
		if err != nil {
			t.Fatalf("AdjustDown failed: %v", err)
		}

		// Wait for workflow-2 to be granted (should happen quickly after AdjustDown)
		select {
		case <-granted:
			// Success!
		case <-time.After(1 * time.Second):
			t.Fatal("workflow-2 was not granted after AdjustDown")
		}

		// Release first workflow
		err = l.Release("workflow-1")
		if err != nil {
			t.Fatalf("Release failed: %v", err)
		}

		wg.Wait()
	})
}

// Test SignalComplete
func TestLimiter_SignalComplete(t *testing.T) {
	l := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})

	ctx := context.Background()
	workflowID := "test-workflow-1"

	// Reserve and consume
	err := l.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		err = l.Consume(workflowID)
		if err != nil {
			t.Fatalf("Consume failed: %v", err)
		}
	}

	// Signal complete
	err = l.SignalComplete(workflowID)
	if err != nil {
		t.Fatalf("SignalComplete failed: %v", err)
	}

	// Should still be able to release
	err = l.Release(workflowID)
	if err != nil {
		t.Fatalf("Release after SignalComplete failed: %v", err)
	}
}

// Test Extend
func TestLimiter_Extend(t *testing.T) {
	l := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})

	ctx := context.Background()
	workflowID := "test-workflow-1"

	// Reserve 5 slots
	err := l.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	// Consume 3
	for i := 0; i < 3; i++ {
		err = l.Consume(workflowID)
		if err != nil {
			t.Fatalf("Consume failed: %v", err)
		}
	}

	// Extend by 2 (should fit in current window: consumed=3, reserved will be 7, limit=10)
	err = l.Extend(ctx, workflowID, 2)
	if err != nil {
		t.Fatalf("Extend failed: %v", err)
	}

	// Should be able to consume 4 more (originally reserved 5, consumed 3, extended by 2 = total 7 reserved, can consume 4 more)
	for i := 0; i < 4; i++ {
		err = l.Consume(workflowID)
		if err != nil {
			t.Fatalf("Consume after extend failed: %v", err)
		}
	}

	// This should fail (exceeds extended reservation)
	err = l.Consume(workflowID)
	if !errors.Is(err, ErrConsumeExceedsLimit) {
		t.Errorf("Expected ErrConsumeExceedsLimit after extend, got %v", err)
	}

	// Release
	err = l.Release(workflowID)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
}

// Test Extend requiring window reset
func TestLimiter_ExtendRequiresWindowReset(t *testing.T) {
	runLimiterTest(t, func(t *testing.T) {
		l := NewLimiter(Config{
			RequestsPerMinute: 10,
			WindowDuration:    500 * time.Millisecond, // Short window for testing
			Logger:            slog.Default(),
		})

		ctx := t.Context()
		workflowID := "test-workflow-1"

		// Reserve 8 slots
		err := l.Reserve(ctx, workflowID, 8)
		if err != nil {
			t.Fatalf("Reserve failed: %v", err)
		}

		// Consume 8
		for i := 0; i < 8; i++ {
			err = l.Consume(workflowID)
			if err != nil {
				t.Fatalf("Consume failed: %v", err)
			}
		}

		// Try to extend by 5 (would need 13 total, but limit is 10, so need to wait for window reset)
		start := time.Now()
		err = l.Extend(ctx, workflowID, 5)
		if err != nil {
			t.Fatalf("Extend failed: %v", err)
		}
		elapsed := time.Since(start)

		// Should have waited for window reset (~500ms)
		if elapsed < 400*time.Millisecond {
			t.Errorf("Extend should have waited for window reset, but elapsed time was %v", elapsed)
		}

		// After window reset, consumed should be 0, and we should have extended reservation
		// Consume 5 more
		for i := 0; i < 5; i++ {
			err = l.Consume(workflowID)
			if err != nil {
				t.Fatalf("Consume after extend failed: %v", err)
			}
		}

		// Release
		err = l.Release(workflowID)
		if err != nil {
			t.Fatalf("Release failed: %v", err)
		}
	})
}

// Test window reset behavior
func TestLimiter_WindowReset(t *testing.T) {
	runLimiterTest(t, func(t *testing.T) {
		l := NewLimiter(Config{
			RequestsPerMinute: 5,
			WindowDuration:    500 * time.Millisecond,
			Logger:            slog.Default(),
		})

		ctx := t.Context()

		// Reserve and consume all capacity
		err := l.Reserve(ctx, "workflow-1", 5)
		if err != nil {
			t.Fatalf("Reserve failed: %v", err)
		}

		for i := 0; i < 5; i++ {
			err = l.Consume("workflow-1")
			if err != nil {
				t.Fatalf("Consume failed: %v", err)
			}
		}

		// Release
		err = l.Release("workflow-1")
		if err != nil {
			t.Fatalf("Release failed: %v", err)
		}

		// Try to reserve again immediately (should wait until window resets)
		reserveErr := make(chan error, 1)
		go func() {
			reserveErr <- l.Reserve(ctx, "workflow-2", 5)
		}()

		// Ensure the second workflow is queued before advancing time
		synctest.Wait()

		// Wait for window reset
		time.Sleep(600 * time.Millisecond)

		// Start third workflow which will trigger reset detection
		reserveThird := make(chan error, 1)
		go func() {
			reserveThird <- l.Reserve(ctx, "workflow-3", 5)
		}()

		select {
		case err = <-reserveErr:
			if err != nil {
				t.Fatalf("Second reserve failed: %v", err)
			}
			l.Release("workflow-2")
		case <-time.After(1 * time.Second):
			t.Fatal("workflow-2 reservation was not granted after window reset")
		}

		select {
		case err = <-reserveThird:
			if err != nil {
				t.Fatalf("Reserve after window reset failed: %v", err)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("workflow-3 reservation was not granted after window reset")
		}

		// Cleanup
		l.Release("workflow-3")
	})
}

// Test context cancellation during Reserve
func TestLimiter_ReserveContextCancellation(t *testing.T) {
	runLimiterTest(t, func(t *testing.T) {
		l := NewLimiter(Config{
			RequestsPerMinute: 5,
			Logger:            slog.Default(),
		})

		ctx := t.Context()

		// First workflow reserves all capacity
		err := l.Reserve(ctx, "workflow-1", 5)
		if err != nil {
			t.Fatalf("First reserve failed: %v", err)
		}

		// Second workflow with cancellable context
		ctx2, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() {
			errCh <- l.Reserve(ctx2, "workflow-2", 3)
		}()

		// Give it time to queue
		time.Sleep(100 * time.Millisecond)

		// Cancel the context
		cancel()

		// Should get context.Canceled error
		err = <-errCh
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Expected context.Canceled, got %v", err)
		}

		// Queue should be empty now
		_, _, queueLen, _ := l.Stats()
		if queueLen != 0 {
			t.Errorf("Expected queue length 0 after cancellation, got %d", queueLen)
		}

		// Cleanup
		l.Release("workflow-1")
	})
}

// Test error conditions
func TestLimiter_ErrorConditions(t *testing.T) {
	l := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})

	ctx := context.Background()

	// Try to reserve with empty workflow ID
	err := l.Reserve(ctx, "", 5)
	if !errors.Is(err, ErrInvalidWorkflowID) {
		t.Errorf("Expected ErrInvalidWorkflowID for empty workflow ID, got %v", err)
	}

	// Try to reserve with zero count
	err = l.Reserve(ctx, "workflow-1", 0)
	if err == nil {
		t.Error("Expected error for zero reservation count")
	}

	// Try to reserve with negative count
	err = l.Reserve(ctx, "workflow-1", -5)
	if err == nil {
		t.Error("Expected error for negative reservation count")
	}

	// Reserve successfully
	err = l.Reserve(ctx, "workflow-1", 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	// Try to reserve again with same workflow ID
	err = l.Reserve(ctx, "workflow-1", 3)
	if !errors.Is(err, ErrAlreadyReserved) {
		t.Errorf("Expected ErrAlreadyReserved, got %v", err)
	}

	// Try to consume with different workflow ID
	err = l.Consume("workflow-2")
	if !errors.Is(err, ErrReservationNotHeld) {
		t.Errorf("Expected ErrReservationNotHeld, got %v", err)
	}

	// Try to adjust down with different workflow ID
	err = l.AdjustDown("workflow-2", 3)
	if !errors.Is(err, ErrReservationNotHeld) {
		t.Errorf("Expected ErrReservationNotHeld, got %v", err)
	}

	// Try to extend with different workflow ID
	err = l.Extend(ctx, "workflow-2", 2)
	if !errors.Is(err, ErrReservationNotHeld) {
		t.Errorf("Expected ErrReservationNotHeld, got %v", err)
	}

	// Try to signal complete with different workflow ID
	err = l.SignalComplete("workflow-2")
	if !errors.Is(err, ErrReservationNotHeld) {
		t.Errorf("Expected ErrReservationNotHeld, got %v", err)
	}

	// Try to release with different workflow ID
	err = l.Release("workflow-2")
	if !errors.Is(err, ErrReservationNotHeld) {
		t.Errorf("Expected ErrReservationNotHeld, got %v", err)
	}

	// Cleanup
	l.Release("workflow-1")
}

// Test concurrent operations
func TestLimiter_Concurrent(t *testing.T) {
	runLimiterTest(t, func(t *testing.T) {
		l := NewLimiter(Config{
			RequestsPerMinute: 20,
			Logger:            slog.Default(),
		})

		ctx := t.Context()
		const numWorkflows = 10

		var wg sync.WaitGroup
		for i := 0; i < numWorkflows; i++ {
			wg.Add(1)
			workflowID := fmt.Sprintf("workflow-%d", i)
			go func(id string) {
				defer wg.Done()

				// Reserve
				err := l.Reserve(ctx, id, 2)
				if err != nil {
					t.Errorf("Reserve for %s failed: %v", id, err)
					return
				}

				// Consume
				err = l.Consume(id)
				if err != nil {
					t.Errorf("Consume for %s failed: %v", id, err)
				}

				// Small delay
				time.Sleep(10 * time.Millisecond)

				// Consume again
				err = l.Consume(id)
				if err != nil {
					t.Errorf("Second consume for %s failed: %v", id, err)
				}

				// Signal complete and release
				l.SignalComplete(id)
				l.Release(id)
			}(workflowID)
		}

		wg.Wait()

		// All should be done, no active reservation
		_, _, queueLen, hasRes := l.Stats()
		if queueLen != 0 {
			t.Errorf("Expected queue length 0 after all workflows complete, got %d", queueLen)
		}
		if hasRes {
			t.Error("Expected no active reservation after all workflows complete")
		}
	})
}

// Test ConsumeWithOperation
func TestLimiter_ConsumeWithOperation(t *testing.T) {
	l := NewLimiter(Config{
		RequestsPerMinute: 10,
		Logger:            slog.Default(),
	})

	ctx := context.Background()
	workflowID := "test-workflow-1"

	// Reserve
	err := l.Reserve(ctx, workflowID, 5)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	// Consume with operation name
	err = l.ConsumeWithOperation(workflowID, "ListBots")
	if err != nil {
		t.Fatalf("ConsumeWithOperation failed: %v", err)
	}

	consumed, _, _, _ := l.Stats()
	if consumed != 1 {
		t.Errorf("Expected consumed=1, got %d", consumed)
	}

	// Cleanup
	l.Release(workflowID)
}

// Benchmark Reserve/Release cycle
func BenchmarkLimiter_ReserveRelease(b *testing.B) {
	l := NewLimiter(Config{
		RequestsPerMinute: 1000,
		Logger:            slog.Default(),
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		workflowID := fmt.Sprintf("workflow-%d", i)
		l.Reserve(ctx, workflowID, 1)
		l.Release(workflowID)
	}
}
