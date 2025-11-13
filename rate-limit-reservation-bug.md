# Rate Limit Reservation Blocks ProduceActiveDeals When Workers Are Active

## Description

`ProduceActiveDeals` pessimistically reserves the entire rate limit quota before knowing how many bots need to be polled. This causes the periodic bot polling to stall when any other workflow has active reservations, and permanently skips bots when the bot count exceeds the rate limit.

## Location

`engine/engine.go:117-124`

```go
_, limit, _, _ := e.limiter.Stats()

// Reserve entire quota pessimistically (we don't know how many bots yet)
if err := e.limiter.Reserve(ctx, workflowID, limit); err != nil {
    return fmt.Errorf("rate limit reserve: %w", err)
}
```

## Root Cause

The `Reserve` API requires that **all requested slots are currently available** in the current window (see `ratelimit/limiter.go:106`):

```go
if l.consumed+totalReserved+count <= l.limit {
    // Grant immediately
}
```

When ProduceActiveDeals tries to reserve `limit` slots while other workflows have active reservations, the condition can only succeed if `consumed + existing_reservations == 0`, causing it to block and wait in queue.

## Impact

### 1. Bot Polling Stalls During Active Deals

- ProduceActiveDeals runs periodically (e.g., every 15 seconds)
- While HandleDeal workers are processing deals with active reservations, ProduceActiveDeals **blocks** waiting for the queue
- New deals are not discovered until all workers complete
- Periodic bot polling is effectively stalled

### 2. Bots Beyond Quota Are Permanently Skipped

When bot count exceeds the rate limit:

1. Reserve: 300 slots (full limit)
2. ListBots: consumes 1 slot → 299 remaining
3. AdjustDown to `1 + len(bots)` (e.g., 1 + 400 = 401)
4. `AdjustDown` only reduces reservations, never enlarges (see `limiter.go:233-236`)
5. Reservation stays at 300, but 401 calls are needed
6. Subsequent `GetListOfDeals` calls hit `ErrConsumeExceedsLimit` (see `limiter.go:191-193`)
7. Result: Bots beyond index 299 are skipped every polling cycle

## Reproduction Scenario

**Setup:**
- Rate limit: 300 requests/minute
- Active bots: 400
- 5 concurrent HandleDeal workers each holding 2-slot reservations (10 slots total reserved)

**Observed Behavior:**
1. ProduceActiveDeals tries to reserve 300 slots
2. Available capacity: 300 - 10 = 290 slots
3. Reserve blocks waiting for workers to complete
4. After workers finish, Reserve succeeds
5. ListBots consumes 1 slot
6. AdjustDown(401) does nothing (can't enlarge from 300)
7. First 299 GetListOfDeals succeed
8. Bots 300-400 fail with `ErrConsumeExceedsLimit`

## Potential Solutions

### Option 1: Reserve Only Available Capacity
```go
consumed, limit, _, _ := e.limiter.Stats()
available := limit - consumed - totalReserved // Need to expose totalReserved

if available > 0 {
    if err := e.limiter.Reserve(ctx, workflowID, available); err != nil {
        return err
    }
}
```

### Option 2: Incremental Window Iteration
```go
initialReservation := min(limit/2, 50)
if err := e.limiter.Reserve(ctx, workflowID, initialReservation); err != nil {
    return err
}

// After ListBots, extend if needed
neededSlots := 1 + len(bots)
if neededSlots > initialReservation {
    if err := e.limiter.Extend(ctx, workflowID, neededSlots-initialReservation); err != nil {
        // Handle spanning multiple windows
    }
}
```

### Option 3: Remove Reservation for ProduceActiveDeals
```go
// Don't use Reserve/AdjustDown for ProduceActiveDeals
// Let individual API calls naturally consume slots across windows
// This allows the workflow to span multiple windows without blocking
bots, err := e.client.ListBots(ctx, tc.WithScopeForListBots(tc.Enabled))
// Each GetListOfDeals will consume as slots become available
```

## Recommendation

**Option 3** (remove reservation pattern for ProduceActiveDeals) is cleanest because:
- The workflow naturally spans multiple windows when bot count > limit
- Individual HandleDeal workers already use reservations effectively
- Removes blocking behavior entirely
- Simpler code

## Related Code

- Reservation logic: `ratelimit/limiter.go:85-170`
- Reserve capacity check: `ratelimit/limiter.go:106`
- AdjustDown limitation: `ratelimit/limiter.go:233-236`
- Consume limit check: `ratelimit/limiter.go:191-193`
