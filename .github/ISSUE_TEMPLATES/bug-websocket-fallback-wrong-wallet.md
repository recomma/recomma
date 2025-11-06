# Bug: WebSocket Client Fallback May Return Wrong Wallet's Data

## Description
The `HyperLiquidEmitter.wsFor()` method has fallback logic that may return a websocket client for a different wallet than the target venue. This could cause incorrect BBO (best bid/offer) pricing or order status lookups when fetching market data.

## Location
`emitter/emitter.go:193-213`

## Current Behavior
```go
func (e *HyperLiquidEmitter) wsFor(venue recomma.VenueID) *ws.Client {
    e.wsMu.RLock()
    defer e.wsMu.RUnlock()

    if len(e.wsClients) == 0 {
        return nil
    }

    // Try exact match first
    if client, ok := e.wsClients[venue]; ok && client != nil {
        return client
    }

    // Fallback to primary venue
    if e.primaryVenue != "" {
        if client, ok := e.wsClients[e.primaryVenue]; ok && client != nil {
            return client  // ⚠️ May be different wallet
        }
    }

    // Fallback to ANY client
    for _, client := range e.wsClients {
        if client != nil {
            return client  // ⚠️ May be different wallet
        }
    }

    return nil
}
```

## Problematic Scenario

### Setup
- Venue A: `venue-wallet-1`, wallet = `0xAAA`
- Venue B: `venue-wallet-2`, wallet = `0xBBB`
- Primary: `venue-wallet-1`

### Issue
```go
// Emitter for Venue B tries to fetch BBO
wsClient := e.wsFor("venue-wallet-2")  // No direct match
// Falls back to primary venue → returns websocket for wallet 0xAAA
// But order is for wallet 0xBBB!
```

### Impact on Code
```go
// In Emit() method (line 247)
func (e *HyperLiquidEmitter) setMarketPrice(ctx context.Context, wsClient *ws.Client, order hyperliquid.CreateOrderRequest) {
    if wsClient == nil {
        return order
    }

    // This BBO might be from the WRONG wallet's subscription
    wsClient.EnsureBBO(order.Coin)
    bbo := wsClient.WaitForBestBidOffer(ctx, order.Coin)
    // ...
}
```

## Expected Behavior

BBO data is typically **wallet-agnostic** (same across all accounts), so this may not cause incorrect pricing in practice. However:

1. **Order status lookups** could return wrong results if using wrong wallet's websocket
2. **Code clarity** - the fallback suggests any websocket is acceptable when it may not be
3. **Future issues** - if Hyperliquid adds wallet-specific websocket data, this will break

## Current Usage Analysis

The websocket client is used for:
1. **BBO price fetching** (line 253-264) - Should be wallet-agnostic ✅
2. **Order status lookup** (line 338) - `wsClient.Get(ctx, w.OrderId)` - May be wallet-specific ⚠️

```go
if wsClient != nil {
    if status, ok := wsClient.Get(ctx, w.OrderId); ok && isLiveStatus(status) {
        // This could return status for WRONG wallet if fallback occurred
    }
}
```

## Severity
**Low to Medium** - depends on whether order statuses are wallet-specific in Hyperliquid's websocket implementation.

## Suggested Fix

### Option 1: No Fallback
```go
func (e *HyperLiquidEmitter) wsFor(venue recomma.VenueID) *ws.Client {
    e.wsMu.RLock()
    defer e.wsMu.RUnlock()

    // Only return exact match or nil
    if client, ok := e.wsClients[venue]; ok && client != nil {
        return client
    }

    return nil  // No fallback
}
```

### Option 2: Wallet-Aware Fallback
```go
func (e *HyperLiquidEmitter) wsFor(venue recomma.VenueID, wallet string) *ws.Client {
    e.wsMu.RLock()
    defer e.wsMu.RUnlock()

    // Try exact venue match
    if client, ok := e.wsClients[venue]; ok && client != nil {
        return client
    }

    // Fallback only to clients with SAME wallet
    for _, client := range e.wsClients {
        if client != nil && client.Wallet == wallet {
            return client
        }
    }

    return nil
}
```

### Option 3: Separate Methods
```go
// For wallet-agnostic data (BBO)
func (e *HyperLiquidEmitter) getBBOClient(coin string) *ws.Client {
    // Any client is fine
}

// For wallet-specific data (order status)
func (e *HyperLiquidEmitter) getOrderClient(venue recomma.VenueID) *ws.Client {
    // Must match venue exactly
}
```

## Investigation Needed
Determine if Hyperliquid websocket order status is:
- **Wallet-specific**: Only shows orders for the subscribed wallet → fallback is **incorrect**
- **Global**: Shows orders by CLOID regardless of wallet → fallback is **safe**

## Branch
`codex/investigate-multi-wallet-support-for-hyperliquid`

## Related
- WebSocket client implementation in `hl/ws/ws.go`
- Order status usage in `emitter/emitter.go:338`
- BBO fetching in `emitter/emitter.go:247-267`
