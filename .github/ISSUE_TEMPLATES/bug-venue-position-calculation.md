# Bug: Venue Position Calculation Returns Arbitrary OrderId

## Description
The `calculateVenuePositions()` function aggregates positions by `(venue, wallet)` but returns `map[OrderIdentifier]float64` where the `OrderIdentifier` includes an arbitrary `OrderId` field. This creates confusion because the `OrderId` in the result has no semantic meaning for position aggregation.

## Location
`filltracker/service.go:249-293`

## Current Behavior
```go
func (s *Service) calculateVenuePositions(snapshot DealSnapshot) map[recomma.OrderIdentifier]float64 {
    venuePositions := make(map[venueKey]float64)
    venueIdentifiers := make(map[venueKey]recomma.OrderIdentifier)

    for _, order := range snapshot.Orders {
        key := venueKey{venue: order.Identifier.VenueID, wallet: order.Identifier.Wallet}

        // Store arbitrary identifier (whichever order is processed last)
        if _, exists := venueIdentifiers[key]; !exists {
            venueIdentifiers[key] = order.Identifier  // ⚠️ Arbitrary OrderId
        }

        // Calculate position...
    }

    // Return with arbitrary OrderId in the key
    result := make(map[recomma.OrderIdentifier]float64)
    for key, netQty := range venuePositions {
        result[venueIdentifiers[key]] = netQty  // OrderId is meaningless here
    }
    return result
}
```

## Expected Behavior
Position aggregation should either:
1. Return `map[venueKey]float64` to make it clear the key is `(venue, wallet)` only
2. Use a sentinel/zero `OrderId` to indicate it's not specific to any order
3. Document clearly that the `OrderId` field is arbitrary and should be ignored

## Impact
- **Confusion**: Callers might think the `OrderId` in the result is meaningful
- **Future Bugs**: Someone might try to use the `OrderId` from the result map, leading to incorrect behavior
- **Type Safety**: The type signature implies OrderId matters when it doesn't

## Current Usage
The function is called from `ReconcileTakeProfits()`:

```go
venuePositions := s.calculateVenuePositions(snapshot)
for ident, venueNetQty := range venuePositions {
    key := venueKey{venue: ident.VenueID, wallet: ident.Wallet}  // Only uses venue+wallet
    // ident.OrderId is ignored ✅
}
```

The code works correctly because callers only extract `venue` and `wallet`, but this is fragile.

## Suggested Fix

### Option 1: Return Correct Type
```go
func (s *Service) calculateVenuePositions(snapshot DealSnapshot) map[venueKey]float64 {
    positions := make(map[venueKey]float64)

    for _, order := range snapshot.Orders {
        key := venueKey{venue: order.Identifier.VenueID, wallet: order.Identifier.Wallet}

        if order.Side == "B" || strings.EqualFold(order.Side, "BUY") {
            positions[key] += order.FilledQty
        } else {
            positions[key] -= order.FilledQty
        }
    }

    return positions
}

// Update ReconcileTakeProfits to use venueKey directly
```

### Option 2: Use Sentinel OrderId
```go
func (s *Service) calculateVenuePositions(snapshot DealSnapshot) map[recomma.OrderIdentifier]float64 {
    // ...
    for key, netQty := range venuePositions {
        // Use zero OrderId to indicate "not specific to any order"
        ident := recomma.NewOrderIdentifier(
            recomma.VenueID(key.venue),
            key.wallet,
            orderid.OrderId{}, // ✅ Explicit zero value
        )
        result[ident] = netQty
    }
    return result
}
```

### Option 3: Add Documentation
```go
// calculateVenuePositions computes net filled quantity per (venue, wallet) pair.
//
// NOTE: The returned map keys include an OrderIdentifier, but the OrderId field
// is ARBITRARY and should be ignored. Only the VenueID and Wallet fields are
// meaningful for position aggregation. Callers should extract venue+wallet only.
func (s *Service) calculateVenuePositions(snapshot DealSnapshot) map[recomma.OrderIdentifier]float64 {
    // ...
}
```

## Recommendation
**Option 1** is cleanest - return the actual type being aggregated.

## Branch
`codex/investigate-multi-wallet-support-for-hyperliquid`

## Related
- `ReconcileTakeProfits()` in `filltracker/service.go:163`
- `venueKey` type definition in `filltracker/service.go:240`
