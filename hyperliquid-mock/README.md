# Hyperliquid Mock Server

A minimal Go implementation of the Hyperliquid API for E2E testing without requiring the real Hyperliquid service to be online or having USDT available.

## Features

- **No authentication validation** - Accepts all requests without signature verification
- **In-memory order state** - Tracks orders for realistic responses
- **Unlimited requests** - No rate limiting
- **Simple responses** - Returns success for all valid operations

## Installation

### As a standalone binary

```bash
git clone https://github.com/recomma/hyperliquid-mock.git
cd hyperliquid-mock
go build -o hyperliquid-mock
```

### As a Go module (for testing)

```bash
go get github.com/recomma/hyperliquid-mock
```

Then import in your tests:

```go
import "github.com/recomma/hyperliquid-mock/server"
```

## Usage

### Start the server

```bash
./hyperliquid-mock -addr :8080
```

Or using `go run`:

```bash
go run main.go -addr :8080
```

### Configuration

The server accepts the following flags:

- `-addr` - Server address (default: `:8080`)

### Configure your application

Point your Recomma application to use the mock server by setting:

```bash
export HYPERLIQUIDURL="http://localhost:8080"
```

Or in your secrets configuration file.

## Endpoints

### POST /exchange

Handles trading actions:
- Order creation
- Order modification
- Order cancellation

**Example request (order creation):**
```json
{
  "action": {
    "type": "order",
    "orders": [{
      "coin": "ETH",
      "is_buy": true,
      "sz": 1.5,
      "limit_px": 3000.00,
      "reduce_only": false,
      "order_type": {"limit": {"tif": "Gtc"}},
      "cloid": "0x1234..."
    }]
  },
  "nonce": 1234567890,
  "signature": {
    "r": "0x...",
    "s": "0x...",
    "v": 27
  }
}
```

**Example response:**
```json
{
  "status": "ok",
  "response": {
    "type": "order",
    "data": {
      "statuses": [
        {
          "resting": {
            "oid": 1000001
          }
        }
      ]
    }
  }
}
```

### POST /info

Handles queries:
- `orderStatus` - Query order status by OID or CLOID
- `metaAndAssetCtxs` - Get perpetual futures metadata
- `spotMetaAndAssetCtxs` - Get spot trading metadata

**Example request (order status):**
```json
{
  "type": "orderStatus",
  "oid": 1000001
}
```

**Example response:**
```json
{
  "status": "success",
  "order": {
    "order": {
      "coin": "ETH",
      "side": "B",
      "limitPx": "3000.00",
      "sz": "1.5",
      "oid": 1000001,
      "timestamp": 1234567890000,
      "origSz": "1.5",
      "cloid": "0x1234..."
    },
    "status": "open",
    "statusTimestamp": 1234567890000
  }
}
```

### GET /health

Health check endpoint.

**Example response:**
```json
{
  "status": "healthy",
  "time": "1234567890"
}
```

## Using in Go Tests

The mock server can be embedded directly in your Go tests with automatic request capture and inspection.

### Basic Test Usage

```go
import (
    "context"
    "crypto/ecdsa"
    "testing"

    "github.com/ethereum/go-ethereum/crypto"
    "github.com/recomma/hyperliquid-mock/server"
    "github.com/sonirico/go-hyperliquid"
)

func TestMyHyperliquidCode(t *testing.T) {
    // Create isolated test server (auto-cleanup)
    ts := server.NewTestServer(t)
    ctx := context.Background()

    // Configure Hyperliquid client to use the mock
    privateKey, _ := crypto.HexToECDSA("your-private-key-hex")
    pub := privateKey.Public()
    pubECDSA, _ := pub.(*ecdsa.PublicKey)
    accountAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

    exchange := hyperliquid.NewExchange(
        ctx,
        privateKey,
        ts.URL(), // Use mock server URL
        nil, "", accountAddr, nil,
    )

    // Run your test code
    status, err := exchange.Order(ctx, hyperliquid.CreateOrderRequest{
        Coin: "ETH", IsBuy: true, Size: 1.0, Price: 3000.0,
        OrderType: hyperliquid.OrderType{
            Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
        },
        Cloid: strPtr("my-cloid"),
    }, nil)

    // Inspect what was sent to the mock server
    requests := ts.GetExchangeRequests()
    assert.Len(t, requests, 1)

    // Check server state
    order, exists := ts.GetOrder("my-cloid")
    assert.True(t, exists)
    assert.Equal(t, "open", order.Status)
}
```

### Test Isolation

Each test gets its own server instance on a random port with isolated request history:

```go
func TestOrderCreation(t *testing.T) {
    ts := server.NewTestServer(t)
    // ts.RequestCount() == 0
    // Make requests...
}

func TestOrderCancellation(t *testing.T) {
    ts := server.NewTestServer(t)
    // Completely separate from TestOrderCreation
    // ts.RequestCount() == 0
}
```

### Request Inspection

Capture and inspect all requests sent to the mock:

```go
// Get typed exchange requests
exchangeReqs := ts.GetExchangeRequests()

// Get typed info requests
infoReqs := ts.GetInfoRequests()

// Get raw requests with headers/body
rawReqs := ts.GetRequests()
for _, req := range rawReqs {
    fmt.Println(req.Method, req.Path)
    fmt.Println(string(req.Body))
    fmt.Println(req.Headers)
}
```

### State Inspection

Check the server's internal order state:

```go
// Get order by CLOID
order, exists := ts.GetOrder("my-cloid-123")
if exists {
    fmt.Println(order.Order.Coin)    // "ETH"
    fmt.Println(order.Status)         // "open"
    fmt.Println(order.Order.Oid)      // 1000001
}

// Get order by OID
order, exists := ts.GetOrderByOid(1000001)
```

### Clear Request History

Clear captured requests between test phases:

```go
// Phase 1: Setup
makeOrder(ts, "ETH", 1.0)
assert.Equal(t, 1, ts.RequestCount())

// Clear history
ts.ClearRequests()
assert.Equal(t, 0, ts.RequestCount())

// Phase 2: Test actual behavior
makeOrder(ts, "BTC", 0.5)
assert.Equal(t, 1, ts.RequestCount())
```

### Example: Full Integration Test

See `examples_test.go` for complete working examples including:
- Testing with real go-hyperliquid library
- Order creation, modification, cancellation
- Order status queries
- Parallel test safety
- Request inspection patterns

## Manual Testing with curl

You can also test the mock server manually using curl:

```bash
# Health check
curl http://localhost:8080/health

# Create an order
curl -X POST http://localhost:8080/exchange \
  -H "Content-Type: application/json" \
  -d '{
    "action": {
      "type": "order",
      "orders": [{
        "coin": "ETH",
        "is_buy": true,
        "sz": 1.0,
        "limit_px": 3000.00,
        "cloid": "test-order-1"
      }]
    },
    "nonce": 123,
    "signature": {"r": "0x", "s": "0x", "v": 27}
  }'

# Query metadata
curl -X POST http://localhost:8080/info \
  -H "Content-Type: application/json" \
  -d '{"type": "metaAndAssetCtxs"}'
```

## Limitations

- **No signature validation** - All requests are accepted regardless of signature
- **No real order matching** - Orders are just stored in memory
- **No WebSocket support** - Only HTTP REST endpoints
- **Simplified responses** - Some fields may be omitted or simplified
- **No persistence** - All state is lost when the server restarts
- **No rate limiting** - Unlimited requests accepted

## Development

### Project structure

```
hyperliquid-mock/
├── main.go                    # Binary entry point
├── go.mod                     # Go module file
├── openapi.yaml               # OpenAPI specification
├── README.md                  # This file
├── Makefile                   # Build automation
├── examples_test.go           # Integration test examples
└── server/
    ├── server.go              # HTTP server setup
    ├── handlers.go            # Request handlers
    ├── types.go               # Request/response types
    ├── state.go               # In-memory state management
    ├── testserver.go          # Test helpers & request capture
    └── testserver_test.go     # Test server examples
```

### Adding new endpoints

1. Add types to `server/types.go`
2. Implement handler in `server/handlers.go`
3. Register route in `server/server.go`

## License

This is a testing utility for the Recomma project.
