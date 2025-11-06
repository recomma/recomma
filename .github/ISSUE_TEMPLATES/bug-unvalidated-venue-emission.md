# Bug: Unvalidated Venue Emission Can Fail Silently

## Description
The engine emits order work to venues based on bot-venue assignments without validating that those venues are actually registered in the emitter. If a venue assignment exists in the database but the emitter wasn't initialized (e.g., due to configuration error), emission will fail silently with only a log warning.

## Location
`engine/engine.go:350-360`

## Current Behavior
```go
for _, ident := range emission.targets {
    // ... prepare work ...
    if err := e.emitter.Emit(ctx, work); err != nil {
        e.logger.Warn("could not submit order", ...)  // Just logs warning
    }
}
```

The `resolveOrderTargets()` function returns identifiers based on database assignments, but there's no guarantee those venues are registered in the `QueueEmitter`.

## Expected Behavior
The system should either:
1. Validate venue registrations before emission and fail loudly if a venue is missing
2. Pre-validate all venue assignments during engine initialization
3. Return errors from `Emit()` calls and propagate them up the stack

## Impact
- Orders may silently fail to be submitted to configured venues
- Operators won't know orders are being dropped until they check logs
- Could lead to unexpected position imbalances across venues

## Reproduction Steps
1. Add a bot-venue assignment to the database for `venue-X`
2. Start the runtime without configuring `venue-X` in secrets
3. Process a deal - engine will attempt to emit to `venue-X`
4. Emission fails but only logs a warning

## Suggested Fix
Option 1: Pre-validate during engine initialization
```go
func (e *Engine) ValidateVenueAssignments(ctx context.Context) error {
    // Load all unique venue IDs from bot_venue_assignments
    // Check each one is registered in e.emitter
    // Return error if any are missing
}
```

Option 2: Make emission errors fatal
```go
if err := e.emitter.Emit(ctx, work); err != nil {
    return fmt.Errorf("could not submit order to venue %s: %w", ident.Venue(), err)
}
```

## Branch
`codex/investigate-multi-wallet-support-for-hyperliquid`

## Related
- Spec: `specs/multi_venue_emission.adoc`
- Related to venue registry implementation in `emitter/emitter.go`
