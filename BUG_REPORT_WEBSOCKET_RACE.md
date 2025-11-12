# Bug Report: WebSocket Order Updates Not Delivered with Concurrent Wallet Connections

> **UPDATE**: This issue has been fixed in `hyperliquid-mock v0.1.1`. Upgrade to v0.1.1 or later to resolve the WebSocket race condition.

## Summary
After upgrading from `hyperliquid-mock v0.0.3` to `v0.1.0` (which added wallet isolation), WebSocket clients intermittently fail to receive `orderUpdates` when multiple wallets are connected concurrently. This appears to be a race condition in the WebSocket update broadcasting mechanism.

## Environment
- **hyperliquid-mock version**: v0.1.0
- **Test framework**: Go 1.25, running tests with `t.Parallel()`
- **Scenario**: Multiple WebSocket clients subscribing to different wallets simultaneously

## Symptoms

### Failing Tests (Intermittent)
1. **TestWebSocketOrderFillUpdates** - Timeout waiting for initial order creation update
2. **TestWebSocketReconnection** - Timeout waiting for order after reconnection

### Passing Tests (Identical Pattern)
- **TestWebSocketOrderUpdates** - Works perfectly with exact same structure
- **TestWebSocketWithFillTracker** - Passes
- **TestWebSocketMultipleOrders** - Passes

## Evidence This is a Mock Server Bug

### 1. Identical Test Structure
All WebSocket tests follow this pattern:
```go
func TestWebSocketXXX(t *testing.T) {
    t.Parallel()
    ctx := context.WithTimeout(context.Background(), 30*time.Second)

    ts := mockserver.NewTestServer(t)
    exchange, wallet := newMockExchange(t, ts.URL())

    // WebSocket subscribes to same wallet as exchange (post-isolation fix)
    wsClient, err := ws.New(ctx, store, nil, venueID, wallet, ts.WebSocketURL())

    // Create order
    _, err = exchange.Order(ctx, order, nil)

    // Wait for WebSocket to receive update
    require.Eventually(t, func() bool {
        return wsClient.Exists(ctx, oid)
    }, 5*time.Second, 100*time.Millisecond)  // ❌ FAILS HERE
}
```

**Key observation**: The failing tests timeout at line 135 (`TestWebSocketOrderFillUpdates`) and line 371 (`TestWebSocketReconnection`) waiting for the **initial order creation event**, not subsequent fill/modify events. This indicates the WebSocket isn't receiving ANY updates for that wallet.

### 2. Parallel Execution Pattern
Looking at the mock server logs from the failing run:

```
{"level":"INFO","msg":"websocket connection established","remote":"127.0.0.1:65380"}
{"level":"INFO","msg":"subscribed to orderUpdates","user":"0xeb5Df7323c643f01b8C0643bE808a0e6486621e8"}

{"level":"INFO","msg":"websocket connection established","remote":"127.0.0.1:65381"}
{"level":"INFO","msg":"subscribed to orderUpdates","user":"0x628f0bE408bdf24451a1c30C452abbB9cfb50A18"}

{"level":"INFO","msg":"recovered wallet address from signature","wallet":"0xeb5Df7323c643f01b8C0643bE808a0e6486621e8"}
// Order created successfully, status returned to HTTP client

// ❌ BUT: No WebSocket message sent to 0xeb5Df7323c643f01b8C0643bE808a0e6486621e8
```

**What we see**:
- WebSocket connections established ✓
- Subscriptions registered ✓
- Orders created successfully via `/exchange` endpoint ✓
- Order response returned via HTTP ✓
- **WebSocket `orderUpdates` message NOT sent** ❌

### 3. Timing/Race Condition Indicators

**Pattern**: When 3+ tests run in parallel:
- First 1-2 WebSocket tests: Pass ✓
- Next 2 WebSocket tests: Fail ❌
- Last tests: Pass again ✓

This suggests a resource contention or synchronization issue, not a systematic bug.

**TestWebSocketReconnection specifically**:
- First order (before reconnect): Update received ✓
- Second order (after reconnect): Update NOT received ❌
- Same wallet, same mock server instance, different behavior

This points to state management issues around WebSocket connection lifecycle.

## Suspected Root Causes

### Hypothesis 1: Subscription Registry Race Condition
When multiple WebSocket clients subscribe simultaneously to different wallets:

```go
// Pseudocode of suspected issue in mock server
func (s *Server) subscribeOrderUpdates(wallet string, conn *websocket.Conn) {
    s.mu.Lock()
    s.subscriptions[wallet] = conn  // ⚠️ Race: might overwrite or lose subscription
    s.mu.Unlock()
}

func (s *Server) broadcastOrderUpdate(wallet string, order Order) {
    s.mu.RLock()
    conn := s.subscriptions[wallet]  // ⚠️ Race: might get stale/nil connection
    s.mu.RUnlock()

    if conn != nil {
        conn.WriteJSON(order)  // ⚠️ May write to closed/wrong connection
    }
}
```

**Possible issues**:
1. Map access without proper locking during concurrent subscribe/unsubscribe
2. Connection pointer stored but not updated when reconnecting
3. Broadcast happening before subscription is fully registered

### Hypothesis 2: Wallet Address Recovery Timing
From logs, wallet recovery happens AFTER order processing:

```
// 1. Order request received
{"msg":"exchange request received"}

// 2. Wallet recovered from signature
{"msg":"recovered wallet address from signature","wallet":"0xeb5..."}

// 3. Response sent
{"msg":"exchange response"}

// ⚠️ Question: Does broadcast happen between steps 2-3?
// If subscription lookup uses a different wallet key, broadcast could fail
```

**Potential timing issue**:
- Order stored under recovered wallet address
- Broadcast attempts to find subscription by wallet
- If subscription was registered under a different representation of the address (case sensitivity? checksum?), lookup fails

### Hypothesis 3: Connection Cleanup Race
When `TestWebSocketReconnection` closes and reopens:

```go
wsClient.Close()  // Unsubscribes from wallet
// ⚠️ Order might still be processing or queued for broadcast

wsClient = ws.New(...)  // Subscribes again to same wallet
// ⚠️ New subscription might conflict with old one being cleaned up
```

## Reproduction Steps

### Minimal Reproduction

1. **Start mock server**:
```bash
ts := mockserver.NewTestServer(t)
```

2. **Create 3 concurrent wallets with WebSocket subscriptions**:
```go
var wg sync.WaitGroup
for i := 0; i < 3; i++ {
    wg.Add(1)
    go func(idx int) {
        defer wg.Done()

        // Generate unique wallet
        privateKey, _ := crypto.GenerateKey()
        walletAddr := crypto.PubkeyToAddress(privateKey.Public().(ecdsa.PublicKey)).Hex()

        // Subscribe to orderUpdates
        wsClient := connectWebSocket(ts.WebSocketURL(), walletAddr)

        // Create order
        exchange := newExchange(privateKey, ts.URL(), walletAddr)
        exchange.Order(ctx, orderRequest)

        // Wait for WebSocket update
        time.Sleep(2 * time.Second)

        // ❌ Some wallets will NOT receive their order updates
    }(i)
}
wg.Wait()
```

3. **Expected**: All 3 WebSocket clients receive their respective order updates
4. **Actual**: 1-2 clients don't receive updates (appears random/timing-dependent)

## Expected Behavior

When an order is created via `/exchange` endpoint:
1. Mock server recovers wallet address from signature
2. Mock server stores order under that wallet
3. Mock server looks up WebSocket subscriptions for that wallet
4. Mock server broadcasts `orderUpdates` message to ALL active connections subscribed to that wallet
5. WebSocket clients receive the update **reliably, every time**

## Actual Behavior

- Orders are stored correctly (verified via `ts.GetOrder()`)
- HTTP responses return success
- WebSocket subscriptions are registered (logs show "subscribed to orderUpdates")
- **But**: `orderUpdates` messages are not sent to some WebSocket connections intermittently

## Debug Data Needed

To diagnose this, the following instrumentation would help:

### 1. Subscription Tracking
```go
// Log every subscription operation
log.Debug("subscription registered",
    "wallet", wallet,
    "conn_id", connID,  // Unique ID for this connection
    "active_subs", len(subscriptions),
)

// Log every broadcast attempt
log.Debug("attempting broadcast",
    "wallet", wallet,
    "order_cloid", cloid,
    "found_subscription", found,
    "conn_id", connID,
)
```

### 2. Order Processing Timeline
```go
// Add timestamps to trace order lifecycle
log.Debug("order processing started", "cloid", cloid, "time", time.Now())
log.Debug("wallet recovered", "wallet", wallet, "time", time.Now())
log.Debug("order stored", "cloid", cloid, "time", time.Now())
log.Debug("broadcast initiated", "wallet", wallet, "time", time.Now())
log.Debug("broadcast completed", "success", success, "time", time.Now())
```

### 3. Connection State
```go
// Track WebSocket connection lifecycle
log.Debug("ws conn opened", "remote", remote, "conn_id", connID)
log.Debug("ws subscription added", "wallet", wallet, "conn_id", connID)
log.Debug("ws subscription removed", "wallet", wallet, "conn_id", connID)
log.Debug("ws conn closed", "conn_id", connID)
```

## Workaround (Temporary)

Disabling parallel test execution makes the failures disappear:
```go
// Remove t.Parallel() from WebSocket tests
func TestWebSocketOrderFillUpdates(t *testing.T) {
    // t.Parallel()  // ← Comment out
    // ... rest of test
}
```

This confirms it's a concurrency issue in the mock server, not our test code.

## Impact

- Blocks adoption of hyperliquid-mock v0.1.0 with wallet isolation
- Integration tests have ~40% flake rate (2 out of 5 WebSocket tests fail intermittently)
- Cannot reliably test multi-wallet scenarios in CI/CD

## Questions for Mock Server Maintainers

1. How are WebSocket subscriptions stored? Map with wallet as key?
2. What synchronization primitives protect the subscription registry?
3. Is there a separate goroutine handling broadcasts? If so, how is it synchronized with subscription changes?
4. When a connection closes/reopens to the same wallet, is the old subscription cleaned up before the new one is registered?
5. Are wallet addresses normalized (lowercase/checksummed) consistently across subscription and broadcast paths?
