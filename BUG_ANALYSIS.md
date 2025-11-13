# Rate Limiter Deadlock Bug Analysis

**Branch:** `bug/limiter-lock`
**Affected File:** `ratelimit/limiter.go`
**Critical Lines:** 150-168 (Reserve method)

## Executive Summary

The rate limiter has a deadlock bug where queued reservations never wake when the time window resets if no other limiter operations occur. This causes workflows to hang indefinitely until context cancellation.

## Root Cause

### The Problem

When a workflow calls `Reserve()` and capacity is exhausted, it:
1. Gets added to `waitQueue` with a `ready` channel
2. Releases the mutex
3. Blocks waiting for the `ready` channel to close

The `ready` channel is only closed by `tryGrantWaiting()` which is called from:
- `Release()` - when a reservation is released
- `SignalComplete()` - when a workflow completes
- `AdjustDown()` - when a reservation is reduced
- `resetWindowIfNeeded()` - when window resets **during another operation**

### The Deadlock Scenario

1. **Rate limit exhausted**: All 1200 requests/minute consumed
2. **All reservations released**: No active workflows remain
3. **Single workflow queues**: Calls `Reserve()`, enters wait queue
4. **No subsequent operations**: No other calls to limiter methods
5. **Window expires**: 60 seconds pass
6. **Nothing triggers reset detection**: `resetWindowIfNeeded()` is never called
7. **Workflow hangs forever**: Blocked on `ready` channel until context cancelled

### Why This Happens

`resetWindowIfNeeded()` is only called **during other limiter operations**. It's not called automatically when time passes. If the queue has waiting workflows but no active reservations, and no new operations occur, the window can reset but no one detects it.

## Evidence

### Test That Proves the Bug

The existing `TestLimiter_WindowReset` test (lines 451-520) actually **demonstrates** the bug rather than testing correct behavior:

```go
// workflow-1 exhausts capacity and releases (no active reservations)
// workflow-2 tries to reserve and queues
time.Sleep(600 * time.Millisecond) // Window resets
// workflow-3 starts - THIS IS THE WORKAROUND
// workflow-3's Reserve() call triggers resetWindowIfNeeded()
// This wakes up workflow-2
```

**Without workflow-3**, workflow-2 would hang forever!

### Corrected Tests

Two new tests have been written that define the **expected** behavior:

1. **TestLimiter_WindowReset** (corrected version, lines 451-516)
   - Exhausts capacity, releases all reservations
   - Single workflow queues
   - Window expires
   - Workflow should automatically wake WITHOUT needing another operation
   - **Will FAIL** with current implementation

2. **TestLimiter_QueuedReservationDeadlock** (new test, lines 520-583)
   - Explicitly reproduces the bug scenario from the issue report
   - Burst exhausts quota, all reservations released
   - Single caller queues
   - No active reservations, no other operations
   - Window expires
   - **Will FAIL** - workflow never wakes (deadlock)

## Real-World Impact

From `engine/engine.go:122`:

```go
// Reserve entire quota pessimistically
if err := e.limiter.Reserve(ctx, workflowID, limit); err != nil {
    return fmt.Errorf("rate limit reserve: %w", err)
}
defer e.limiter.Release(workflowID)
```

When 3Commas API polling hits a rate limit burst:
1. First workflow exhausts the quota
2. Workflow completes and releases
3. Next polling attempt queues
4. **No other operations occur** (only one polling workflow at a time)
5. Workflow hangs for 60 seconds until context timeout
6. All subsequent polling is blocked

## Solution Approaches

### 1. Background Ticker (Recommended)

Add a background goroutine that periodically checks for window resets:

```go
func (l *Limiter) startWindowTicker() {
    ticker := time.NewTicker(l.window / 10) // Check 10x per window
    go func() {
        for range ticker.C {
            l.mu.Lock()
            if len(l.waitQueue) > 0 {
                l.resetWindowIfNeeded()
            }
            l.mu.Unlock()
        }
    }()
}
```

**Pros:**
- Simple implementation
- Guaranteed to detect resets
- Works for all edge cases

**Cons:**
- Always running (but can optimize to only run when queue non-empty)
- Small overhead

### 2. Scheduled Wake-Up

When adding to queue, calculate next window start and schedule a timer:

```go
// In Reserve(), after adding to queue:
nextWindow := l.windowStart.Add(l.window)
waitDuration := time.Until(nextWindow)
if waitDuration > 0 {
    time.AfterFunc(waitDuration, func() {
        l.mu.Lock()
        l.resetWindowIfNeeded()
        l.mu.Unlock()
    })
}
```

**Pros:**
- No continuous ticker overhead
- Wake exactly when needed

**Cons:**
- More complex logic
- Need to handle race conditions
- Multiple timers if multiple workflows queue

### 3. Hybrid Approach

Combine both:
- Use scheduled wake-up for precision
- Use periodic ticker as safety net (only when queue non-empty)

## Test Execution

The corrected tests cannot be run currently due to Go version requirements (project needs 1.25.0, local is 1.24.7) and network connectivity issues. However, they are syntactically correct and will:

1. **FAIL** with the current implementation (proving the bug)
2. **PASS** once a background ticker or scheduled wake-up is implemented

## Next Steps

1. ✅ Identified root cause
2. ✅ Wrote corrected tests defining expected behavior
3. ⏳ Implement solution (background ticker or scheduled wake-up)
4. ⏳ Verify corrected tests pass
5. ⏳ Ensure all existing tests still pass
6. ⏳ Commit and push fix to branch

## Files Modified

- `ratelimit/limiter_test.go`:
  - Corrected `TestLimiter_WindowReset` to test actual expected behavior
  - Added `TestLimiter_QueuedReservationDeadlock` to explicitly test the bug scenario
