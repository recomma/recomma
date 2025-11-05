# Multi-Venue Emission Bug Report

This document catalogs bugs and issues found during review of the `codex/extend-cmd/recomma-to-handle-multiple-venues` branch compared to `main`.

## Critical Issues

### Bug #1: OrderWork Comparability Violation
**Severity**: Critical
**Location**: `recomma/recomma.go:50-55`

```go
type OrderWork struct {
    Identifier OrderIdentifier
    OrderId    orderid.OrderId
    Action     Action  // Contains POINTERS - breaks comparability
    BotEvent   BotEvent
}
```

**Problem**: The `Action` type contains pointer fields (`BuyAction`, `SellAction`, `CancelAction`), making `OrderWork` non-comparable. This violates the contract for using `OrderWork` as a map key in workqueues.

**Fix Required**:
1. Either remove pointer indirection from Action sub-types, OR
2. Remove the `comparable` constraint from workqueue and implement custom equality checks

**Files Affected**:
- `recomma/recomma.go`
- `emitter/emitter.go` (workqueue map)

---

### Bug #2-4: Scaled Orders Multi-Venue Architecture Broken
**Severity**: Critical
**Locations**:
- `storage/order_scalers.go:482-511`
- `storage/order_scalers.go:369-422`

#### Bug #2: Hard-coded Default Venue in Queries
```go
func (s *Storage) ListScaledOrdersByOrderId(ctx context.Context, oid orderid.OrderId) ([]ScaledOrderAudit, error) {
    rows, err := s.queries.ListScaledOrdersByOrderId(ctx, sqlcgen.ListScaledOrdersByOrderIdParams{
        VenueID:       string(defaultHyperliquidVenueID),  // HARD-CODED
        OrderID:       oid.Hex(),
        OrderIDPrefix: fmt.Sprintf("%s#%%", oid.Hex()),
    })
}

func (s *Storage) ListScaledOrdersByDeal(ctx context.Context, dealID uint32) ([]ScaledOrderAudit, error) {
    rows, err := s.queries.ListScaledOrdersByDeal(ctx, sqlcgen.ListScaledOrdersByDealParams{
        DealID:  int64(dealID),
        VenueID: string(defaultHyperliquidVenueID),  // HARD-CODED
    })
}
```

**Problem**: These functions hard-code the default venue ID, breaking multi-venue support for scaled orders.

**Fix Required**: Change signatures to accept `recomma.OrderIdentifier` or `recomma.VenueID` parameter.

#### Bug #3: Wrong Venue Assignment in InsertScaledOrderAudit
```go
func (s *Storage) InsertScaledOrderAudit(ctx context.Context, params ScaledOrderAuditParams) (ScaledOrderAudit, error) {
    defaultAssignment, err := s.defaultVenueAssignmentLocked(ctx)
    // ...
    insert := sqlcgen.InsertScaledOrderParams{
        VenueID: string(defaultAssignment.VenueID),  // BUG: Always uses default
        Wallet:  defaultAssignment.Wallet,
        // ...
    }
}
```

**Problem**: Always inserts scaled orders with the default venue, regardless of which venue the order was actually submitted to.

**Fix Required**:
1. Change `ScaledOrderAuditParams` to accept `recomma.OrderIdentifier` instead of just `orderid.OrderId`
2. Use the actual venue from the submission context

#### Bug #4: Missing Caller Context
The root cause: `ScaledOrderAuditParams` only contains `orderid.OrderId`:
```go
type ScaledOrderAuditParams struct {
    OrderId orderid.OrderId  // Missing venue context
    // ...
}
```

**Problem**: The OrderId alone is insufficient to identify which venue the scaled order belongs to in a multi-venue system.

**Fix Required**: Replace `OrderId orderid.OrderId` with `Identifier recomma.OrderIdentifier` throughout the scaled order audit flow.

**Callers to Update**:
- `engine/placement.go` (CreateScaledSafetyOrders, CreateScaledTakeProfit)
- Any other code creating `ScaledOrderAuditParams`

---

### Bug #5: Missing scaled_orders in Wallet Migration
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

### Bug #6: Take Profit Reconciliation Doesn't Fan Out
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

### Bug #7: Fill Tracker Unbounded Memory Growth
**Severity**: Medium
**Location**: `filltracker/service.go`

**Problem**: The fill tracker maintains in-memory state for all deals without a cleanup mechanism. Over time, this will cause unbounded memory growth.

**Recommendation**: Implement periodic cleanup of stale/completed deals from memory.

---

## Low Priority Issues

### Bug #8: Replay Logic Edge Case
**Severity**: Low
**Location**: `engine/placement.go`

**Problem**: When replaying positions, if a venue was added after a deal started, the replay might not create submissions for that venue.

**Impact**: Unlikely in practice but worth noting for completeness.

---

## Testing Recommendations

After fixing the above bugs, add tests for:

1. **Multi-venue scaled orders**: Verify scaled orders are created with correct venue
2. **Wallet migration**: Verify scaled_orders table is migrated
3. **Take-profit fan-out**: Verify reconciliation updates all venues
4. **OrderWork comparability**: Verify workqueue map operations work correctly (Bug #1)

---

## Summary

**Critical (must fix)**: Bugs #1-6
**Medium (future work)**: Bug #7
**Low (edge case)**: Bug #8

All SQL-related fixes require running `go generate ./gen/storage` after modifying `storage/sqlc/queries.sql`.

---

## Resolved Issues

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
