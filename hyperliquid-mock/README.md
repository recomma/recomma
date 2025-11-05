# Hyperliquid Mock Server

A minimal Go implementation of the Hyperliquid API for E2E testing without requiring the real Hyperliquid service to be online or having USDT available.

## Features

- **No authentication validation** - Accepts all requests without signature verification
- **In-memory order state** - Tracks orders for realistic responses
- **Unlimited requests** - No rate limiting
- **Simple responses** - Returns success for all valid operations

## Installation

```bash
cd hyperliquid-mock
go build -o hyperliquid-mock
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

## Testing

You can test the mock server using curl:

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
├── main.go              # Entry point
├── go.mod               # Go module file
├── openapi.yaml         # OpenAPI specification
├── README.md            # This file
└── server/
    ├── server.go        # HTTP server setup
    ├── handlers.go      # Request handlers
    ├── types.go         # Request/response types
    └── state.go         # In-memory state management
```

### Adding new endpoints

1. Add types to `server/types.go`
2. Implement handler in `server/handlers.go`
3. Register route in `server/server.go`

## License

This is a testing utility for the Recomma project.
