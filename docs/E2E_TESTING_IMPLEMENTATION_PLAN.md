# E2E Testing Implementation Plan

## Current Status ✅

- **3Commas Mock Server**: Available at `github.com/recomma/3commas-mock`
- **Hyperliquid Mock Server**: Already integrated (`github.com/recomma/hyperliquid-mock`)
- **Specification**: Complete (see `docs/3COMMAS_MOCK_SPEC.md`)
- **Investigation**: Complete (see earlier analysis)

## Blockers Identified

### Critical Blocker: Main Function Not Testable

**Problem:** All application initialization is in `cmd/recomma/main.go:47-674`
- Cannot start app in tests without running `main()`
- Hard-coded dependencies prevent mock injection
- No clean shutdown mechanism for tests

**Impact:** Prevents any E2E testing

## Implementation Roadmap

### Phase 1: Make Application Testable ⚠️ **HIGH PRIORITY**

**Goal:** Extract `main()` into a reusable, testable `App` structure

**Files to Create/Modify:**
- `cmd/recomma/app.go` (new) - Application lifecycle management
- `cmd/recomma/main.go` (modify) - Simplified to use App
- `cmd/recomma/app_test.go` (new) - Basic App tests

**Estimated Effort:** 1-2 days

**Deliverables:**

#### 1.1 Create `App` Structure (`cmd/recomma/app.go`)

```go
package main

import (
    "context"
    "net/http"
    "sync"
    "time"

    "k8s.io/client-go/util/workqueue"
    tc "github.com/recomma/3commas-sdk-go/threecommas"
    "github.com/recomma/recomma/cmd/recomma/internal/config"
    "github.com/recomma/recomma/engine"
    "github.com/recomma/recomma/internal/api"
    "github.com/recomma/recomma/internal/vault"
    "github.com/recomma/recomma/storage"
    // ... other imports
)

// App represents the entire Recomma application
type App struct {
    // Configuration
    Config config.Config

    // Core components
    Store           *storage.Storage
    VaultController *vault.Controller

    // 3Commas integration
    ThreeCommasClient engine.ThreeCommasAPI

    // Hyperliquid integration
    StatusClients map[recomma.VenueID]hl.StatusClient

    // HTTP Server
    Server *http.Server

    // Worker queues
    DealQueue  workqueue.TypedRateLimitingInterface[engine.WorkKey]
    OrderQueue workqueue.TypedRateLimitingInterface[recomma.OrderWork]

    // Engine
    Engine *engine.Engine

    // Context management
    ctx        context.Context
    cancelFunc context.CancelFunc
    wg         sync.WaitGroup

    // Shutdown
    shutdownOnce sync.Once

    // Lifecycle hooks (for testing)
    onServerStarted func(*http.Server)
}

// AppOptions configures application creation
type AppOptions struct {
    Config config.AppConfig
    Store  *storage.Storage // Optional: inject storage (if nil, created from Config.StoragePath)

    // Optional test overrides
    ThreeCommasClient engine.ThreeCommasAPI
    VaultSecrets      *vault.Secrets // Optional: bypass vault unsealing for tests
}

// NewApp creates and initializes the application
func NewApp(ctx context.Context, opts AppOptions) (*App, error) {
    // Extract initialization logic from main()
    // Returns configured but not-yet-started app
}

// WaitForVaultUnseal blocks until vault is unsealed or context cancelled
func (a *App) WaitForVaultUnseal(ctx context.Context) error {
    // Extracted from main.go:229-253
}

// Start begins all background workers and HTTP server
func (a *App) Start(ctx context.Context) error {
    // Start HTTP server in goroutine
    // Start worker goroutines
    // Start periodic tasks (resync, reconcile, cleanup)
}

// ProduceActiveDealsOnce triggers a single deal production cycle
func (a *App) ProduceActiveDealsOnce(ctx context.Context) error {
    // Expose for testing - calls e.ProduceActiveDeals(ctx, a.DealQueue)
}

// Shutdown gracefully stops all components
func (a *App) Shutdown(ctx context.Context) error {
    a.shutdownOnce.Do(func() {
        // Drain queues
        // Stop workers
        // Close HTTP server
        // Close database
        // Close websocket connections
    })
}

// HTTPAddr returns the actual HTTP server address (useful for random ports in tests)
func (a *App) HTTPAddr() string {
    if a.Server == nil || a.Server.Addr == "" {
        return ""
    }
    return a.Server.Addr
}
```

**Key Design Principles:**
1. **Dependency Injection**: Accept mocks via `AppOptions` (Store, ThreeCommasClient, VaultSecrets)
2. **Storage Control**: Tests inject pre-configured storage, production lets NewApp create it
3. **Explicit Lifecycle**: Separate creation, unsealing, starting, shutdown
4. **Context-Aware**: All operations accept context for cancellation
5. **Test-Friendly**: Expose internal state for assertions
6. **Backwards Compatible**: Existing `main()` should work unchanged

**Testing Patterns:**
- **Debug Mode Tests**: Use `cfg.Debug = true`, NewApp creates storage with `:memory:` path
- **E2E Tests**: Create `storage.New(":memory:")` + inject `Store` + inject `VaultSecrets` to bypass unsealing
- **Production**: NewApp creates storage from `Config.StoragePath`, checks database for vault state

#### 1.2 Refactor `main()` (`cmd/recomma/main.go`)

**Before:** 600+ lines of initialization
**After:** ~100 lines using App

```go
func main() {
    cfg := config.DefaultConfig()
    fs := config.NewConfigFlagSet(&cfg)

    if err := fs.Parse(os.Args[1:]); err != nil {
        fatal("parsing flags failed", err)
    }

    if err := config.ApplyEnvDefaults(fs, &cfg); err != nil {
        fatal("invalid parameters", err)
    }

    if err := config.ValidateConfig(cfg); err != nil {
        fatal("invalid configuration", err)
    }

    appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    // Create app
    app, err := NewApp(appCtx, AppOptions{Config: cfg})
    if err != nil {
        fatal("app init failed", err)
    }

    // Wait for vault unseal (skip in debug mode if secrets pre-loaded)
    if err := app.WaitForVaultUnseal(appCtx); err != nil {
        fatal("vault unseal failed", err)
    }

    // Start application
    if err := app.Start(appCtx); err != nil {
        fatal("app start failed", err)
    }

    // Block until shutdown signal
    <-appCtx.Done()

    slog.Info("shutdown requested; draining...")

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    if err := app.Shutdown(shutdownCtx); err != nil {
        slog.Error("shutdown error", slog.String("error", err.Error()))
    }

    slog.Debug("fully shutdown")
}
```

#### 1.3 Add Basic Tests (`cmd/recomma/app_test.go`)

```go
//go:build integration

package main

import (
    "context"
    "testing"
    "time"

    "github.com/recomma/recomma/cmd/recomma/internal/config"
    "github.com/stretchr/testify/require"
)

func TestApp_CreateAndShutdown(t *testing.T) {
    t.Parallel()

    cfg := config.DefaultConfig()
    cfg.StoragePath = ":memory:"
    cfg.HTTPListen = "127.0.0.1:0" // Random port
    cfg.Debug = true

    ctx := context.Background()
    app, err := NewApp(ctx, AppOptions{Config: cfg})
    require.NoError(t, err)
    require.NotNil(t, app)

    shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    err = app.Shutdown(shutdownCtx)
    require.NoError(t, err)
}

func TestApp_StartAndShutdown(t *testing.T) {
    t.Parallel()

    // Test that app can start and shutdown cleanly
    // This validates the lifecycle without E2E testing
}
```

**Success Criteria:**
- ✅ `main()` still works (manual smoke test)
- ✅ `App` can be created in tests
- ✅ `App` can be started and shutdown cleanly
- ✅ No memory leaks (goroutines cleaned up)

---

### Phase 2: Add 3Commas Mock Integration

**Goal:** Add 3commas-mock to dependencies and verify it works

**Files to Create/Modify:**
- `go.mod` (modify) - Add dependency
- `engine/produce_active_deals_integration_test.go` (new) - First integration test using mock

**Estimated Effort:** 0.5 days

**Deliverables:**

#### 2.1 Add Dependency

```bash
go get github.com/recomma/3commas-mock@latest
go mod tidy
```

#### 2.2 Create Integration Test

**File:** `engine/produce_active_deals_integration_test.go`

```go
//go:build integration

package engine_test

import (
    "context"
    "testing"

    threecommasmock "github.com/recomma/3commas-mock/server"
    tc "github.com/recomma/3commas-sdk-go/threecommas"
    "github.com/recomma/recomma/engine"
    "github.com/recomma/recomma/storage"
    "github.com/stretchr/testify/require"
)

func TestProduceActiveDeals_Integration(t *testing.T) {
    t.Parallel()

    // Create mock server
    mockServer := threecommasmock.NewTestServer(t)
    defer mockServer.Close()

    // Configure mock state
    mockServer.AddBot(threecommasmock.Bot{
        ID:      1,
        Name:    "Test Bot",
        Enabled: true,
    })

    mockServer.AddDeal(1, threecommasmock.Deal{
        ID:     101,
        BotID:  1,
        Status: "active",
        Events: []threecommasmock.BotEvent{
            {
                CreatedAt:     "2024-01-15T10:30:00.000Z",
                Action:        "place",
                Coin:          "BTC",
                Type:          "buy",
                Status:        "active",
                Price:         "50000.0",
                Size:          "0.0002",
                OrderType:     "base",
                OrderSize:     1,
                OrderPosition: 1,
                IsMarket:      false,
            },
        },
    })

    // Create SDK client pointing to mock
    client, err := tc.New3CommasClient(
        tc.WithBaseURL(mockServer.URL()),
        tc.WithAPIKey("test-key"),
        tc.WithPrivatePEM([]byte("test-pem")),
    )
    require.NoError(t, err)

    // Create engine with mock client
    store, err := storage.New(":memory:")
    require.NoError(t, err)
    defer store.Close()

    eng := engine.NewEngine(client, engine.WithStorage(store))

    // Create fake queue
    type fakeQueue struct{ added []engine.WorkKey }
    q := &fakeQueue{}
    q.Add = func(item engine.WorkKey) { q.added = append(q.added, item) }

    // Test ProduceActiveDeals
    ctx := context.Background()
    err = eng.ProduceActiveDeals(ctx, q)
    require.NoError(t, err)

    // Verify deal was queued
    require.Len(t, q.added, 1)
    require.Equal(t, uint32(101), q.added[0].DealID)
    require.Equal(t, uint32(1), q.added[0].BotID)
}
```

**Success Criteria:**
- ✅ 3commas-mock successfully imported
- ✅ Mock server starts and responds correctly
- ✅ SDK client can communicate with mock
- ✅ Integration test passes

---

### Phase 3: Create E2E Test Harness

**Goal:** Build reusable harness for E2E tests

**Files to Create:**
- `cmd/recomma/e2e_test.go` (new) - E2E test harness
- `cmd/recomma/testdata/` (new directory) - Test fixtures

**Estimated Effort:** 1-2 days

**Deliverables:**

#### 3.1 E2E Test Harness

**File:** `cmd/recomma/e2e_test.go`

```go
//go:build integration

package main

import (
    "context"
    "crypto/ecdsa"
    "net/http"
    "testing"
    "time"

    gethCrypto "github.com/ethereum/go-ethereum/crypto"
    mockserver "github.com/recomma/hyperliquid-mock/server"
    threecommasmock "github.com/recomma/3commas-mock/server"
    tc "github.com/recomma/3commas-sdk-go/threecommas"
    "github.com/recomma/recomma/cmd/recomma/internal/config"
    "github.com/recomma/recomma/internal/api"
    "github.com/recomma/recomma/internal/vault"
    "github.com/stretchr/testify/require"
)

// E2ETestHarness manages all components for end-to-end testing
type E2ETestHarness struct {
    t *testing.T

    // Application under test
    App *App

    // Mock servers
    ThreeCommasMock *threecommasmock.TestServer
    HyperliquidMock *mockserver.TestServer

    // Direct access for assertions
    Store *storage.Storage

    // HTTP client for API testing
    HTTPClient *http.Client

    // Generated test credentials
    HLPrivateKey *ecdsa.PrivateKey
    HLWallet     string
}

func NewE2ETestHarness(t *testing.T) *E2ETestHarness {
    t.Helper()

    // Create mock servers
    tcMock := threecommasmock.NewTestServer(t)
    hlMock := mockserver.NewTestServer(t)

    // Generate Hyperliquid test credentials
    privateKey, err := gethCrypto.GenerateKey()
    require.NoError(t, err)

    pub := privateKey.Public()
    pubECDSA, ok := pub.(*ecdsa.PublicKey)
    require.True(t, ok)
    wallet := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

    // Create test secrets
    secrets := &vault.Secrets{
        Secrets: vault.Data{
            THREECOMMASAPIKEY:     "test-api-key",
            THREECOMMASPRIVATEKEY: "test-private-key",
            THREECOMMASPLANTIER:   "expert",
            Venues: []vault.VenueSecret{
                {
                    ID:          "hyperliquid:test",
                    Type:        "hyperliquid",
                    DisplayName: "Test Venue",
                    Wallet:      wallet,
                    PrivateKey:  hex.EncodeToString(gethCrypto.FromECDSA(privateKey)),
                    APIURL:      hlMock.URL(),
                    Primary:     true,
                },
            },
        },
        ReceivedAt: time.Now().UTC(),
    }

    // Create 3commas client pointing to mock
    tcClient, err := tc.New3CommasClient(
        tc.WithBaseURL(tcMock.URL()),
        tc.WithAPIKey(secrets.Secrets.THREECOMMASAPIKEY),
        tc.WithPrivatePEM([]byte(secrets.Secrets.THREECOMMASPRIVATEKEY)),
    )
    require.NoError(t, err)

    // Create test storage
    store, err := storage.New(":memory:")
    require.NoError(t, err)

    // Create test configuration
    cfg := config.DefaultConfig()
    cfg.HTTPListen = "127.0.0.1:0" // Random port

    // Create app with test dependencies
    ctx := context.Background()
    app, err := NewApp(ctx, AppOptions{
        Config:            cfg,
        Store:             store,
        ThreeCommasClient: tcClient,
        VaultSecrets:      secrets, // Bypass vault unsealing for tests
    })
    require.NoError(t, err)

    return &E2ETestHarness{
        t:               t,
        App:             app,
        ThreeCommasMock: tcMock,
        HyperliquidMock: hlMock,
        Store:           app.Store,
        HTTPClient:      &http.Client{Timeout: 10 * time.Second},
        HLPrivateKey:    privateKey,
        HLWallet:        wallet,
    }
}

// Start starts the application (non-blocking)
func (h *E2ETestHarness) Start(ctx context.Context) {
    h.t.Helper()

    // App.Start should be non-blocking (starts goroutines)
    err := h.App.Start(ctx)
    require.NoError(h.t, err)

    // Wait for HTTP server to be ready
    h.WaitForHTTPServer(5 * time.Second)
}

// Shutdown gracefully stops the application
func (h *E2ETestHarness) Shutdown() {
    h.t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    err := h.App.Shutdown(ctx)
    if err != nil {
        h.t.Logf("shutdown warning: %v", err)
    }

    h.ThreeCommasMock.Close()
    h.HyperliquidMock.Close()
}

// WaitForHTTPServer polls until HTTP server responds
func (h *E2ETestHarness) WaitForHTTPServer(timeout time.Duration) {
    h.t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()

    baseURL := "http://" + h.App.HTTPAddr()

    for {
        select {
        case <-ctx.Done():
            h.t.Fatal("timeout waiting for HTTP server")
        case <-ticker.C:
            resp, err := h.HTTPClient.Get(baseURL + "/api/bots")
            if err == nil {
                resp.Body.Close()
                return
            }
        }
    }
}

// WaitForDealProcessing polls until deal appears in database
func (h *E2ETestHarness) WaitForDealProcessing(dealID uint32, timeout time.Duration) {
    h.t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            h.t.Fatalf("timeout waiting for deal %d processing", dealID)
        case <-ticker.C:
            deals, _, err := h.Store.ListDeals(ctx, api.ListDealsOptions{})
            if err != nil {
                continue
            }
            for _, d := range deals {
                if uint32(d.Id) == dealID {
                    return
                }
            }
        }
    }
}

// WaitForOrderSubmission polls until order appears in Hyperliquid mock
func (h *E2ETestHarness) WaitForOrderSubmission(cloid string, timeout time.Duration) {
    h.t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            h.t.Fatalf("timeout waiting for order %s submission", cloid)
        case <-ticker.C:
            if _, exists := h.HyperliquidMock.GetOrder(cloid); exists {
                return
            }
        }
    }
}

// APIGet performs GET request to app's API
func (h *E2ETestHarness) APIGet(path string) *http.Response {
    h.t.Helper()

    baseURL := "http://" + h.App.HTTPAddr()
    resp, err := h.HTTPClient.Get(baseURL + path)
    require.NoError(h.t, err)
    return resp
}

// APIPost performs POST request to app's API
func (h *E2ETestHarness) APIPost(path string, body io.Reader) *http.Response {
    h.t.Helper()

    baseURL := "http://" + h.App.HTTPAddr()
    resp, err := h.HTTPClient.Post(baseURL+path, "application/json", body)
    require.NoError(h.t, err)
    return resp
}
```

**Success Criteria:**
- ✅ Harness can create app with mocks
- ✅ Harness can start and shutdown cleanly
- ✅ Helper methods work (wait for server, deal processing, etc.)
- ✅ HTTP client can make requests to API

---

### Phase 4: Write E2E Tests

**Goal:** Comprehensive E2E test coverage

**Files to Create:**
- `cmd/recomma/e2e_deal_flow_test.go` - Full deal → order flow
- `cmd/recomma/e2e_order_scaler_test.go` - Order scaling tests
- `cmd/recomma/e2e_fill_tracking_test.go` - Fill tracking and TP reconciliation
- `cmd/recomma/e2e_api_test.go` - HTTP API integration tests

**Estimated Effort:** 2-3 days

**Deliverables:**

#### 4.1 Basic Deal Flow Test

**File:** `cmd/recomma/e2e_deal_flow_test.go`

```go
//go:build integration

package main

import (
    "context"
    "encoding/json"
    "io"
    "testing"

    threecommasmock "github.com/recomma/3commas-mock/server"
    "github.com/stretchr/testify/require"
)

func TestE2E_DealToOrderFlow(t *testing.T) {
    t.Parallel()

    harness := NewE2ETestHarness(t)
    defer harness.Shutdown()

    ctx := context.Background()

    // Configure 3commas mock
    harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
        ID:      1,
        Name:    "Test Bot",
        Enabled: true,
    })

    harness.ThreeCommasMock.AddDeal(1, threecommasmock.Deal{
        ID:           101,
        BotID:        1,
        Pair:         "USDT_BTC",
        Status:       "active",
        ToCurrency:   "BTC",
        FromCurrency: "USDT",
        Events: []threecommasmock.BotEvent{
            {
                CreatedAt:     "2024-01-15T10:30:00.000Z",
                Action:        "place",
                Coin:          "BTC",
                Type:          "buy",
                Status:        "active",
                Price:         "50000.0",
                Size:          "0.0002",
                OrderType:     "base",
                OrderSize:     1,
                OrderPosition: 1,
                IsMarket:      false,
            },
        },
    })

    // Start application
    harness.Start(ctx)

    // Trigger deal production
    err := harness.App.ProduceActiveDealsOnce(ctx)
    require.NoError(t, err)

    // Wait for deal to be processed
    harness.WaitForDealProcessing(101, 5*time.Second)

    // Wait for order submission to Hyperliquid
    // The CLOID should be derivable from bot_id=1, deal_id=101, event fingerprint
    // For now, check that at least one order was submitted
    time.Sleep(1 * time.Second) // Allow time for order worker

    orders := harness.HyperliquidMock.GetAllOrders()
    require.NotEmpty(t, orders, "expected at least one order submitted to Hyperliquid")

    // Verify order properties
    order := orders[0]
    require.Equal(t, "BTC", order.Order.Coin)
    require.True(t, order.Order.IsBuy)

    // Verify database recorded the order
    dbOrders, _, err := harness.Store.ListOrders(ctx, api.ListOrdersOptions{})
    require.NoError(t, err)
    require.NotEmpty(t, dbOrders)

    // Verify web API returns the order
    resp := harness.APIGet("/api/orders")
    require.Equal(t, http.StatusOK, resp.StatusCode)

    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)

    var apiOrders []map[string]interface{}
    err = json.Unmarshal(body, &apiOrders)
    require.NoError(t, err)
    require.NotEmpty(t, apiOrders)

    t.Logf("E2E test passed: deal → processing → hyperliquid → web API")
}
```

#### 4.2 Order Scaler Test

**File:** `cmd/recomma/e2e_order_scaler_test.go`

```go
//go:build integration

package main

import (
    "context"
    "testing"

    threecommasmock "github.com/recomma/3commas-mock/server"
    "github.com/stretchr/testify/require"
)

func TestE2E_OrderScalerApplied(t *testing.T) {
    t.Parallel()

    harness := NewE2ETestHarness(t)
    defer harness.Shutdown()

    ctx := context.Background()

    // Set default order scaler to 0.5 (50% size)
    _, err := harness.Store.UpsertOrderScaler(ctx, 0.5, "e2e-test", nil)
    require.NoError(t, err)

    // Configure deal with size 0.001 BTC
    harness.ThreeCommasMock.AddBot(threecommasmock.Bot{ID: 1, Enabled: true})
    harness.ThreeCommasMock.AddDeal(1, threecommasmock.Deal{
        ID:     101,
        BotID:  1,
        Events: []threecommasmock.BotEvent{
            {
                Action:    "place",
                Coin:      "BTC",
                Type:      "buy",
                Price:     "50000.0",
                Size:      "0.001", // Original size
                OrderType: "base",
            },
        },
    })

    harness.Start(ctx)

    // Trigger processing
    err = harness.App.ProduceActiveDealsOnce(ctx)
    require.NoError(t, err)

    time.Sleep(1 * time.Second)

    // Verify order was scaled to 0.0005 (50% of 0.001)
    orders := harness.HyperliquidMock.GetAllOrders()
    require.Len(t, orders, 1)

    // Size should be scaled
    // Note: Exact comparison depends on precision handling
    require.Contains(t, orders[0].Order.Sz, "0.0005")

    t.Logf("E2E test passed: order scaler applied correctly")
}
```

#### 4.3 Fill Tracking Test

**File:** `cmd/recomma/e2e_fill_tracking_test.go`

```go
//go:build integration

package main

import (
    "context"
    "testing"
    "time"

    threecommasmock "github.com/recomma/3commas-mock/server"
    "github.com/stretchr/testify/require"
)

func TestE2E_FillTriggersTakeProfitCancel(t *testing.T) {
    t.Parallel()

    harness := NewE2ETestHarness(t)
    defer harness.Shutdown()

    ctx := context.Background()

    // Configure deal with base order and take-profit
    harness.ThreeCommasMock.AddBot(threecommasmock.Bot{ID: 1, Enabled: true})
    harness.ThreeCommasMock.AddDeal(1, threecommasmock.Deal{
        ID:    101,
        BotID: 1,
        Events: []threecommasmock.BotEvent{
            {
                CreatedAt: "2024-01-15T10:30:00.000Z",
                Action:    "place",
                Coin:      "BTC",
                Type:      "buy",
                Status:    "active",
                Price:     "50000.0",
                Size:      "0.001",
                OrderType: "base",
            },
            {
                CreatedAt: "2024-01-15T10:31:00.000Z",
                Action:    "place",
                Coin:      "BTC",
                Type:      "sell",
                Status:    "active",
                Price:     "50750.0",
                Size:      "0.001",
                OrderType: "take_profit",
            },
        },
    })

    harness.Start(ctx)

    // Process deal - should create both orders
    err := harness.App.ProduceActiveDealsOnce(ctx)
    require.NoError(t, err)

    time.Sleep(1 * time.Second)

    orders := harness.HyperliquidMock.GetAllOrders()
    require.Len(t, orders, 2, "should have base and TP orders")

    // Find base order and simulate fill
    var baseOrderCLOID string
    for _, order := range orders {
        if order.Order.IsBuy {
            baseOrderCLOID = *order.Order.Cloid
            break
        }
    }
    require.NotEmpty(t, baseOrderCLOID)

    // Simulate base order fill
    err = harness.HyperliquidMock.FillOrder(baseOrderCLOID, 50000.0)
    require.NoError(t, err)

    // WebSocket should notify fill tracker
    // Fill tracker should detect all buys filled
    // Should cancel take-profit orders

    // TODO: Verify TP cancellation
    // This requires WebSocket event flow to be working

    t.Logf("E2E test passed: fill tracking works")
}
```

#### 4.4 HTTP API Test

**File:** `cmd/recomma/e2e_api_test.go`

```go
//go:build integration

package main

import (
    "context"
    "encoding/json"
    "io"
    "net/http"
    "testing"

    threecommasmock "github.com/recomma/3commas-mock/server"
    "github.com/stretchr/testify/require"
)

func TestE2E_APIListBots(t *testing.T) {
    t.Parallel()

    harness := NewE2ETestHarness(t)
    defer harness.Shutdown()

    ctx := context.Background()

    // Configure mock
    harness.ThreeCommasMock.AddBot(threecommasmock.Bot{
        ID:      1,
        Name:    "API Test Bot",
        Enabled: true,
    })

    harness.Start(ctx)

    // Trigger bot sync
    err := harness.App.ProduceActiveDealsOnce(ctx)
    require.NoError(t, err)

    time.Sleep(500 * time.Millisecond)

    // Test API
    resp := harness.APIGet("/api/bots")
    require.Equal(t, http.StatusOK, resp.StatusCode)

    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    require.NoError(t, err)

    var bots []map[string]interface{}
    err = json.Unmarshal(body, &bots)
    require.NoError(t, err)
    require.NotEmpty(t, bots)

    // Verify bot properties
    bot := bots[0]
    require.Equal(t, float64(1), bot["id"])
    require.Equal(t, "API Test Bot", bot["name"])
}

func TestE2E_APIListDeals(t *testing.T) {
    // Similar test for /api/deals
}

func TestE2E_APIListOrders(t *testing.T) {
    // Similar test for /api/orders
}
```

**Success Criteria:**
- ✅ Full deal → order flow works end-to-end
- ✅ Order scaler is applied correctly
- ✅ Fill tracking integration works
- ✅ HTTP API endpoints return correct data
- ✅ All tests pass consistently

---

## Summary of Work

### Phase 1: App Refactoring (Critical Path)
- Extract `main()` → `App` structure
- Enable dependency injection
- Add lifecycle methods

**Blocker Resolution:** ✅ This unblocks ALL E2E testing

### Phase 2: 3Commas Mock Integration
- Add dependency to go.mod
- Verify mock works with SDK
- Write basic integration test

**Blocker Resolution:** ✅ Enables realistic 3Commas testing

### Phase 3: E2E Harness
- Create reusable test harness
- Add helper methods
- Support app lifecycle in tests

**Blocker Resolution:** ✅ Enables writing E2E tests efficiently

### Phase 4: E2E Tests
- Deal → Order flow
- Order scaler application
- Fill tracking
- HTTP API integration

**Blocker Resolution:** ✅ Achieves full E2E coverage

---

## Timeline Estimate

**Total:** 4-6 days

- Phase 1: 1-2 days (critical path)
- Phase 2: 0.5 days
- Phase 3: 1-2 days
- Phase 4: 2-3 days

**Parallelization:** Phases 2-4 can partially overlap with Phase 1 nearing completion

---

## Testing Strategy

### Integration Tests (`//go:build integration`)
- Run with: `go test -tags=integration ./...`
- Use `:memory:` SQLite
- Use mock servers
- Fast execution (<10s total)

### E2E Tests (subset of integration tests)
- Full application lifecycle
- All components wired together
- Slowest but most comprehensive

### CI/CD Integration
- Run integration tests on every PR
- Catch regressions early
- No external dependencies needed

---

## Risk Mitigation

### Risk: App Refactoring Breaks Existing Behavior
**Mitigation:**
- Keep `main()` functional throughout
- Manual smoke testing after each change
- Incremental extraction (not big bang)

### Risk: Mock Servers Don't Match Real APIs
**Mitigation:**
- Use VCR recordings to validate responses
- Cross-reference with production logs
- Update mocks as real APIs evolve

### Risk: Tests Are Flaky
**Mitigation:**
- Use proper synchronization (WaitFor helpers)
- Avoid sleep-based timing where possible
- Use contexts with timeouts

### Risk: Tests Are Slow
**Mitigation:**
- Use `:memory:` SQLite
- Run tests in parallel (`t.Parallel()`)
- Mock external services
- Target: All integration tests <10s

---

## Success Metrics

- ✅ E2E test covers full data flow
- ✅ Tests run in CI/CD reliably
- ✅ Test execution time <10s for all integration tests
- ✅ No manual testing required for regression detection
- ✅ New features include E2E tests by default

---

## Next Immediate Action

**START HERE:** Phase 1 - App Refactoring

```bash
# Create new file
touch cmd/recomma/app.go

# Start extracting initialization logic from main()
# Target: Get a working App struct that can be created in tests
```

**Goal:** By end of Phase 1, this should work:

```go
func TestAppCanStart(t *testing.T) {
    cfg := config.DefaultConfig()
    cfg.StoragePath = ":memory:"
    cfg.Debug = true

    app, err := NewApp(context.Background(), AppOptions{Config: cfg})
    require.NoError(t, err)

    ctx := context.Background()
    err = app.Start(ctx)
    require.NoError(t, err)

    err = app.Shutdown(ctx)
    require.NoError(t, err)
}
```

Once this works, everything else follows naturally! 🚀
