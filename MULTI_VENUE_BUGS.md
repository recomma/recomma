# Multi-Venue Emission Bug Report

This document catalogs bugs and issues found during review of the `codex/extend-cmd/recomma-to-handle-multiple-venues` branch compared to `main`.

## Critical Issues

### Bug #1: Missing scaled_orders in Wallet Migration
**Severity**: High
**Location**: `storage/storage.go:216-273`

**Problem**: The `migrateDefaultVenueWalletLocked` function clones `hyperliquid_submissions` and `hyperliquid_status_history` tables when migrating wallets, but does NOT clone the `scaled_orders` table. This causes data loss for scaled order audit trails.

**Fix Required** (needs sqlc regeneration):

1. Add to `storage/sqlc/queries.sql`:
```sql
-- name: CloneScaledOrdersToWallet :exec
INSERT INTO scaled_orders (
    venue_id, wallet, order_id, deal_id, bot_id,
    original_size, scaled_size, multiplier, rounding_delta,
    stack_index, order_side, multiplier_updated_by,
    created_at_utc, skipped, skip_reason,
    payload_type, payload_blob
)
SELECT
    src.venue_id,
    sqlc.arg(to_wallet) AS wallet,
    src.order_id, src.deal_id, src.bot_id,
    src.original_size, src.scaled_size, src.multiplier, src.rounding_delta,
    src.stack_index, src.order_side, src.multiplier_updated_by,
    src.created_at_utc, src.skipped, src.skip_reason,
    src.payload_type, src.payload_blob
FROM scaled_orders AS src
WHERE src.venue_id = sqlc.arg(venue_id)
  AND src.wallet = sqlc.arg(from_wallet)
ON CONFLICT(venue_id, wallet, order_id) DO NOTHING;

-- name: DeleteScaledOrdersForWallet :exec
DELETE FROM scaled_orders
WHERE venue_id = sqlc.arg(venue_id)
  AND wallet = sqlc.arg(wallet);
```

2. Run: `go generate ./gen/storage`

3. Add to `storage/storage.go` in `migrateDefaultVenueWalletLocked`:
```go
// Clone scaled_orders
cloneScaledOrdersParams := sqlcgen.CloneScaledOrdersToWalletParams{
    ToWallet:   toWallet,
    VenueID:    string(defaultHyperliquidVenueID),
    FromWallet: fromWallet,
}
if err := qtx.CloneScaledOrdersToWallet(ctx, cloneScaledOrdersParams); err != nil {
    return err
}

// ... (after other deletes) ...

// Delete old scaled_orders
deleteScaledOrdersParams := sqlcgen.DeleteScaledOrdersForWalletParams{
    VenueID: string(defaultHyperliquidVenueID),
    Wallet:  fromWallet,
}
if err := qtx.DeleteScaledOrdersForWallet(ctx, deleteScaledOrdersParams); err != nil {
    return err
}
```

---

### Bug #2: Take Profit Reconciliation Doesn't Fan Out
**Severity**: Medium
**Location**: `filltracker/service.go:156-282`

**Problem**: The `reconcileTakeProfits` function only updates one `OrderIdentifier` instead of fanning out to all venues when a deal has take-profits across multiple venues.

**Current Code**:
```go
func (s *Service) reconcileTakeProfits(ctx context.Context, ident recomma.OrderIdentifier, dlr Deal) error {
    // Only uses the single 'ident' passed in
    // Should query all venues for take-profits related to dlr.DealID
}
```

**Fix Required**: Query all venues for take-profits related to the deal and update each one.

---

## Medium Priority Issues

### Bug #3: Fill Tracker Unbounded Memory Growth
**Severity**: Medium
**Location**: `filltracker/service.go`

**Problem**: The fill tracker maintains in-memory state for all deals without a cleanup mechanism. Over time, this will cause unbounded memory growth.

**Recommendation**: Implement periodic cleanup of stale/completed deals from memory.

---

## Low Priority Issues

### Bug #4: Replay Logic Edge Case
**Severity**: Low
**Location**: `engine/placement.go`

**Problem**: When replaying positions, if a venue was added after a deal started, the replay might not create submissions for that venue.

**Impact**: Unlikely in practice but worth noting for completeness.

---

## Testing Recommendations

After fixing the above bugs, add tests for:

1. **Wallet migration**: Verify scaled_orders table is migrated (Bug #1)
2. **Take-profit fan-out**: Verify reconciliation updates all venues (Bug #2)

---

## Summary

**Critical (must fix)**: Bugs #1-2
**Medium (future work)**: Bug #3
**Low (edge case)**: Bug #4

All SQL-related fixes require running `go generate ./gen/storage` after modifying `storage/sqlc/queries.sql`.

---

## Resolved Issues

### OrderWork Comparability Violation - RESOLVED ✓
**Status**: Fixed
**Resolution Date**: 2025-11-05
**PR**: #75
**Commits**:
- `a4b8c76` - fix: remove pointer fields from Action struct to make OrderWork comparable
- `d90e115` - fix: correct remaining pointer-to-value conversions
- `30a763f` - fix: simplify cloneAction since Action fields are now values
- `7092f95` - refactor: remove not needed cloneAction

**Original Problem**: The `Action` type contained pointer fields (`*BuyAction`, `*SellAction`, `*CancelAction`), making `OrderWork` non-comparable and violating the contract for using it as a map key in workqueues.

**Fix**: Changed Action struct fields from pointers to values:
```go
type Action struct {
    Type   ActionType
    Create hyperliquid.CreateOrderRequest    // Changed from pointer
    Modify hyperliquid.ModifyOrderRequest    // Changed from pointer
    Cancel hyperliquid.CancelOrderRequestByCloid  // Changed from pointer
    Reason string
}
```

**Files Changed**:
- `recomma/recomma.go`
- `emitter/emitter.go`
- `engine/engine.go`
- `adapter/adapter.go`
- `internal/api/handler.go`

---

### Scaled Orders Multi-Venue Architecture - RESOLVED ✓
**Status**: Fixed
**Resolution Date**: 2025-11-05
**PR**: #76
**Commits**:
- `a2f5fd0` - fix: resolve multi-venue scaled order bugs
- `5fd46f2` - fix: update tests to use OrderIdentifier
- `ed82f06` - chore: gofmt and go generate
- `5808b73` - fix: match scaled orders by venue in filltracker

**Original Problems**:

1. **Hard-coded Default Venue in Queries**: `ListScaledOrdersByOrderId` and `ListScaledOrdersByDeal` hard-coded `defaultHyperliquidVenueID`.

2. **Wrong Venue Assignment**: `InsertScaledOrderAudit` always used default venue instead of the actual submission venue.

3. **Missing Caller Context**: `ScaledOrderAuditParams` only had `orderid.OrderId` without venue information.

**Fix**:

1. Updated `ScaledOrderAuditParams` to use `Identifier recomma.OrderIdentifier`:
```go
type ScaledOrderAuditParams struct {
    Identifier          recomma.OrderIdentifier  // Now includes VenueID, Wallet, OrderId
    DealID              uint32
    // ...
}
```

2. Modified `InsertScaledOrderAudit` to use venue from identifier:
```go
venueID := params.Identifier.VenueID
wallet := params.Identifier.Wallet
```

3. Removed `VenueID` filter from SQL queries:
```sql
-- ListScaledOrdersByOrderId: Now queries across all venues
WHERE order_id = sqlc.arg(order_id)
   OR order_id LIKE sqlc.arg(order_id_prefix)

-- ListScaledOrdersByDeal: Now queries across all venues
WHERE deal_id = sqlc.arg(deal_id)
```

**Files Changed**:
- `storage/order_scalers.go`
- `storage/order_scalers_test.go`
- `storage/sqlc/queries.sql`
- `storage/sqlcgen/queries.sql.go`
- `engine/orderscaler/service.go`
- `engine/orderscaler/service_test.go`
- `filltracker/service.go`
- `filltracker/service_test.go`

---

### ~~Bug: Redundant OrderId Field in OrderWork~~ - NOT A BUG
**Status**: Resolved - Clarified as working as intended
**Resolution Date**: 2025-11-05
**Commit**: `aeaf0c2` - chore: clarify orderid purpose

**Original Report**: `OrderId orderid.OrderId` in `OrderWork` was thought to duplicate `Identifier.OrderId`.

**Clarification**: The `OrderId` field tracks **incoming 3commas orders** (BotID + DealID + MessageID fingerprint), while `Identifier.OrderId` tracks the **venue-specific order identifier**. These serve different purposes:
- `orderid.OrderId`: 3commas order fingerprint for replay/deduplication
- `recomma.OrderIdentifier`: Multi-venue order tracking (venue + wallet + order)

See `orderid/orderid.go:12` for updated documentation.

---

### SQL Duplicate Clauses - RESOLVED ✓
**Status**: Fixed
**Resolution Date**: 2025-11-05
**Commit**: `cd763cf` - sql: remove duplicates

**Original Bugs**:
1. Duplicate `AND order_id = sqlc.arg(order_id)` in `FetchLatestHyperliquidStatus`
2. Duplicate `AND order_id = sqlc.arg(order_id)` in `ListHyperliquidStatuses`
3. Duplicate `order_id` column in `ListLatestHyperliquidSafetyStatuses` CTE

**Fix**: Removed all duplicate clauses and regenerated sqlc code.

**Files Changed**:
- `storage/sqlc/queries.sql`
- `storage/sqlcgen/queries.sql.go`
