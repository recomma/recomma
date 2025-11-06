# Bug: Default Venue Wallet Migration Race Condition

## Description
The main bootstrap calls `store.EnsureDefaultVenueWallet()` which can trigger wallet migration (`CloneHyperliquidSubmissionsToWallet`, `CloneHyperliquidStatusesToWallet`, etc.) during runtime startup. This happens while other goroutines might be initializing, potentially causing storage operations to use stale wallet identifiers.

## Location
- `cmd/recomma/main.go:290` - Migration triggered during startup
- `storage/storage.go:119-151` - Migration implementation

## Current Behavior
```go
// main.go
if err := store.EnsureDefaultVenueWallet(appCtx, defaultVenueWallet); err != nil {
    fatal("update default venue wallet", err)
}

// Later, other components may start using storage before migration completes
fillTracker := filltracker.New(store, logger)
// ... other initialization that might read from storage ...
```

The migration involves:
1. Clone submissions/statuses/scaled_orders to new wallet
2. Delete old wallet entries
3. Update venue record

If any storage reads happen during this process, they might get incomplete data.

## Expected Behavior
Migration should be:
1. Atomic (wrapped in transaction - ✅ already done)
2. Completed before any other storage operations
3. Or guarded by a flag that prevents concurrent access during migration

## Impact
- Low probability but high severity
- Could cause:
  - Missing submissions if reads occur during migration
  - Duplicate entries if writes happen before old entries are deleted
  - Inconsistent venue state

## Reproduction Steps
Difficult to reproduce due to timing, but possible scenario:
1. Start system with new wallet address (triggers migration)
2. StatusRefresher starts immediately after store initialization
3. Migration hasn't completed yet
4. Status refresher might query with old wallet identifier

## Suggested Fix

Option 1: Ensure strict initialization order
```go
// Complete migration FIRST
if err := store.EnsureDefaultVenueWallet(appCtx, defaultVenueWallet); err != nil {
    fatal("update default venue wallet", err)
}

// THEN create components that use storage
fillTracker := filltracker.New(store, logger)
// ... rest of initialization ...
```

Option 2: Add migration lock
```go
// In storage.Storage
type Storage struct {
    mu              sync.Mutex
    migrationInProgress atomic.Bool
}

func (s *Storage) EnsureDefaultVenueWallet(ctx context.Context, wallet string) error {
    s.migrationInProgress.Store(true)
    defer s.migrationInProgress.Store(false)
    // ... migration logic ...
}

// Check in query methods
func (s *Storage) LoadHyperliquidStatus(...) {
    if s.migrationInProgress.Load() {
        return error("storage migration in progress")
    }
    // ... normal logic ...
}
```

Option 3: Document that migration is atomic and safe
If the transaction guarantees isolation, document this clearly and ensure no components start before `EnsureDefaultVenueWallet()` returns.

## Branch
`codex/investigate-multi-wallet-support-for-hyperliquid`

## Related
- Migration logic in `storage/storage.go:migrateDefaultVenueWalletLocked()`
- Main bootstrap sequence in `cmd/recomma/main.go`
