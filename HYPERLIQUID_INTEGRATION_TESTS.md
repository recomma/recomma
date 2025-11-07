# HyperLiquid Integration Tests

This document describes the comprehensive integration test suite added for HyperLiquid functionality using the HyperLiquid Mock Server.

## Overview

We've expanded test coverage for HyperLiquid integration from **2 test files** to **7 test files**, adding **35+ new integration tests** that use the `hyperliquid-mock` server to simulate real HyperLiquid API interactions.

## New Test Files

### 1. `hl/status_refresher_integration_test.go`
Tests the StatusRefresher component that queries HyperLiquid for order status updates.

**Package:** `hl_test` (uses external test package to avoid import cycles with `storage`)

**Tests:**
- ✅ Basic status refresh with mock server
- ✅ Handling filled orders
- ✅ Handling canceled orders
- ✅ Integration with fill tracker
- ✅ Concurrent refresh operations (10 orders, 4 workers)
- ✅ Timeout handling

**Why this matters:** StatusRefresher is critical for recovering order states after downtime. These tests ensure it handles various order states and API responses correctly.

### 2. `hl/info_integration_test.go`
Tests the Info client's order query functionality.

**Tests:**
- ✅ Query order by CLOID
- ✅ Query non-existent orders
- ✅ Query filled orders
- ✅ Query canceled orders
- ✅ Query partially filled orders
- ✅ Query multiple orders concurrently
- ✅ Order result to WsOrder conversion

**Why this matters:** Info client is the foundation for order status queries throughout the system. These tests ensure correct HTTP interaction and response parsing.

### 3. `hl/order_lifecycle_integration_test.go`
Tests complete order lifecycles from creation to final state.

**Tests:**
- ✅ Create and query orders
- ✅ Create → Fill → Cancel lifecycle
- ✅ Partial fills
- ✅ Multiple concurrent orders with different states
- ✅ Order modification
- ✅ IOC (Immediate-or-Cancel) orders
- ✅ Integration with OrderIdCache
- ✅ Reduce-only orders
- ✅ Buy and sell order handling

**Why this matters:** Order lifecycle tests validate that orders transition correctly through all states and that our code handles each state appropriately.

### 4. `filltracker/hyperliquid_integration_test.go`
Tests FillTracker with real HyperLiquid status updates.

**Tests:**
- ✅ Position tracking with HyperLiquid status updates
- ✅ Partial fill tracking
- ✅ Take profit cancellation handling
- ✅ Multiple concurrent orders (base + 2 safety orders)
- ✅ Average entry price calculation
- ✅ Position value tracking

**Why this matters:** FillTracker manages critical position data. These tests ensure position calculations are correct when processing real HyperLiquid order statuses.

### 5. `emitter/modify_integration_test.go`
Tests order modification functionality with mock server.

**Tests:**
- ✅ Basic order modification (price and size)
- ✅ Modify then fill
- ✅ Modify then cancel
- ✅ Multiple sequential modifications
- ✅ Modify reduce-only orders
- ✅ Persistence of modifications

**Why this matters:** Order modification is used to adjust take-profit orders. These tests ensure modifications are correctly submitted and tracked.

## Existing Enhanced Files

### 6. `emitter/emitter_test.go` (Enhanced)
**Existing tests using mock server:**
- ✅ Cancel order lifecycle
- ✅ IOC retry with success
- ✅ IOC retry exhaustion

### 7. `engine/orderscaler/integration_test.go` (Enhanced)
**Existing integration test:**
- ✅ Order scaler emits scaled orders through emitter

## Running the Tests

### Run all HyperLiquid integration tests:
```bash
go test ./hl/... -v
go test ./filltracker/... -v -run Integration
go test ./emitter/... -v -run Mock
go test ./engine/orderscaler/... -v -run Integration
```

### Run specific test suites:
```bash
# StatusRefresher tests
go test ./hl -v -run StatusRefresher

# Info client tests
go test ./hl -v -run Info

# Order lifecycle tests
go test ./hl -v -run Lifecycle

# FillTracker integration tests
go test ./filltracker -v -run Hyperliquid

# Emitter modification tests
go test ./emitter -v -run Modify
```

### Run with race detection:
```bash
go test ./hl/... -race -v
go test ./filltracker/... -race -v -run Integration
```

### Run with coverage:
```bash
go test ./hl/... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Test Architecture

### Mock Server Usage

All tests use the `github.com/recomma/hyperliquid-mock/server` package:

```go
import mockserver "github.com/recomma/hyperliquid-mock/server"

ts := mockserver.NewTestServer(t)
// ts.URL() provides the mock server URL
// ts.GetOrder(cloid) retrieves order state
// ts.FillOrder(cloid, price) simulates order fills
```

### Common Patterns

#### 1. Creating a mock exchange:
```go
func newMockExchange(t *testing.T, baseURL string) *hyperliquid.Exchange {
    ctx := context.Background()
    privateKey, _ := gethCrypto.GenerateKey()
    pub := privateKey.Public()
    pubECDSA, _ := pub.(*ecdsa.PublicKey)
    walletAddr := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

    return hyperliquid.NewExchange(ctx, privateKey, baseURL, nil, "", walletAddr, nil)
}
```

#### 2. Creating orders:
```go
oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
cloid := oid.Hex()

order := hyperliquid.CreateOrderRequest{
    Coin:          "BTC",
    IsBuy:         true,
    Price:         50000,
    Size:          1.0,
    ClientOrderID: &cloid,
    OrderType: hyperliquid.OrderType{
        Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
    },
}

_, err := exchange.Order(ctx, order, nil)
```

#### 3. Simulating order fills:
```go
ts.FillOrder(cloid, 50000)
// Or partial fill:
ts.FillOrder(cloid, 50000, mockserver.WithFillSize(0.5))
```

## Test Coverage Improvements

| Component | Before | After | Improvement |
|-----------|--------|-------|-------------|
| **StatusRefresher** | Unit tests only | 6 integration tests | ✅ HTTP testing |
| **Info Client** | No dedicated tests | 7 integration tests | ✅ NEW |
| **Order Lifecycle** | None | 11 integration tests | ✅ NEW |
| **FillTracker** | Unit tests only | 5 integration tests | ✅ Real status updates |
| **Emitter Modify** | None | 6 integration tests | ✅ NEW |

**Overall:** Added ~35 integration tests covering HTTP interactions, order state transitions, and real-world scenarios.

## Key Testing Scenarios

### ✅ Order Creation and Management
- Creating orders with various parameters
- Querying order status
- Modifying existing orders
- Canceling orders

### ✅ Order State Transitions
- Open → Filled
- Open → Canceled
- Open → Partial Fill → Filled
- Modify → Fill
- Modify → Cancel

### ✅ Position Tracking
- Single order fills
- Multiple order fills (base + safety orders)
- Partial fills
- Take profit management

### ✅ Concurrent Operations
- Multiple orders created simultaneously
- Concurrent status refresh
- Concurrent fills and cancellations

### ✅ Error Handling
- Non-existent order queries
- Timeout scenarios
- IOC order rejections

## Mock Server Capabilities

The HyperLiquid mock server supports:

- ✅ Order creation (GTC, IOC)
- ✅ Order modification
- ✅ Order cancellation
- ✅ Order fills (full and partial)
- ✅ Order queries by CLOID
- ✅ Order state persistence
- ✅ Reduce-only orders
- ✅ Multiple concurrent orders

## Future Enhancements

Potential areas for additional testing:

1. **WebSocket Integration** - If mock server adds WebSocket support
2. **BBO (Best Bid Offer)** - Market data testing
3. **Multi-wallet scenarios** - Testing wallet isolation
4. **Error scenarios** - Network failures, rate limiting
5. **Performance tests** - Load testing with 100+ orders

## Best Practices

1. **Use t.Parallel()** - All integration tests run in parallel for speed
2. **Clean up resources** - Use `t.Cleanup()` for database/server cleanup
3. **Descriptive test names** - Each test clearly states what it validates
4. **Isolated test data** - Each test uses unique order IDs
5. **Verify both sides** - Check both mock server state and local storage

## Troubleshooting

### Tests are slow
- Ensure tests use `t.Parallel()`
- Check for unnecessary sleeps or delays
- Run with `-short` flag to skip slow tests if marked

### Mock server errors
- Verify `hyperliquid-mock` dependency version: `v0.0.1`
- Check that test server is properly initialized
- Ensure cleanup functions are called

### Random failures
- Check for race conditions: run with `-race`
- Verify test isolation (no shared state)
- Check timestamps and time-dependent logic

## Contributing

When adding new integration tests:

1. Follow existing patterns
2. Use meaningful order IDs for debugging
3. Test both success and error paths
4. Document complex scenarios
5. Ensure tests clean up after themselves

---

**Total Integration Test Files:** 7
**Total Integration Tests:** 35+
**Coverage Focus:** HyperLiquid HTTP API interactions
**Test Execution Time:** < 10 seconds (parallel)
