# Bug: Complex Replay Logic with Multiple Overlapping Code Paths

## Description
The replay logic in the engine has multiple overlapping code paths with complex interactions between `shouldEmit`, `needsReplay`, `replayForPrimary`, and `skipExisting` flags. This makes the code difficult to reason about and increases the risk of edge cases where orders are replayed incorrectly or skipped inappropriately.

## Location
`engine/engine.go:263-400`

## Current Behavior
The flow involves:

1. Check for missing targets → set `needsReplay` (lines 290-297)
2. Adjust action with tracker (lines 299-302)
3. If no emission but replay needed → create replay with `replayForPrimary=true` (lines 304-318)
4. Create first emission plan with `skipExisting` flag (lines 320-340)
5. Create second emission plan for remaining replays (lines 342-355)

Multiple flags track state:
- `shouldEmit` - whether to emit based on event reduction
- `needsReplay` - whether missing venues need order creation
- `replayForPrimary` - whether this is a replay to primary venue
- `skipExisting` - passed to tracker adjustment to skip checks

## Problematic Scenarios

### Scenario 1: Double Replay
```
1. Order has shouldEmit=false, needsReplay=true
2. Lines 304-318: Create replay, set replayForPrimary=true
3. Lines 320-340: Append emission with skipExisting=true
4. Lines 342-355: Append another emission for needsReplay
```
Could the same venues get added twice?

### Scenario 2: Primary Venue Skip
```
1. Primary venue already has submission
2. New venue added
3. replayForPrimary=true set (line 313)
4. skipExisting=true passed to adjustActionWithTracker (line 371)
```
Does skipExisting correctly handle primary vs new venues?

### Scenario 3: Replay Cancellation
```
1. needsReplay=true for missing venues
2. First emission succeeds and sets needsReplay=false (line 338)
3. Second block (lines 342-355) checks needsReplay again
```
The conditional `if len(emissions) == 0` at line 350 suggests these blocks are mutually exclusive, but it's not obvious from the logic flow.

## Expected Behavior
The replay logic should have:
1. Clear state machine with well-defined transitions
2. Single source of truth for "what needs to be emitted"
3. No overlapping or redundant code paths
4. Explicit handling of all edge cases

## Impact
- **Correctness Risk**: Orders might be replayed to wrong venues or skipped incorrectly
- **Maintainability**: Very difficult to modify or debug
- **Testing**: Hard to achieve full coverage of all permutations

## Suggested Fix

### Option 1: Refactor to Decision Table
```go
type emissionDecision struct {
    targets      []recomma.OrderIdentifier
    action       recomma.Action
    skipExisting bool
}

func (e *Engine) decideEmissions(
    oid orderid.OrderId,
    latestEvent *recomma.BotEvent,
    action recomma.Action,
    shouldEmit bool,
    assignments []storage.VenueAssignment,
    storedIdents []recomma.OrderIdentifier,
) []emissionDecision {
    // Single function with clear decision tree
    // Returns list of decisions to execute
}
```

### Option 2: State Machine
```go
type replayState int
const (
    replayNotNeeded replayState = iota
    replayPending
    replayCompleted
)

// Explicit state transitions with guards
```

### Option 3: Add Comprehensive Tests
At minimum, add integration tests covering:
- New venue added mid-deal (replay needed)
- All venues already have submissions (no replay)
- Mix of existing and new venues
- Primary venue has submission, new venue added
- Order cancelled before replay completes

## Workaround
None currently - the logic appears to work in practice but is fragile.

## Branch
`codex/investigate-multi-wallet-support-for-hyperliquid`

## Related
- `resolveOrderTargets()` in `engine/engine.go:665`
- `missingAssignmentTargets()` in `engine/engine.go:716`
- Replay spec in `specs/multi_venue_emission.adoc`

## Additional Notes
This is a **complexity/maintainability issue** rather than a confirmed bug. The current implementation may work correctly, but it's at high risk for regression if modified. Consider adding state machine diagram or detailed comments explaining the interaction between the three code paths.
