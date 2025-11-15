//go:build integration

package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"io"
	"net/http"
	"testing"
	"time"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	threecommasmock "github.com/recomma/3commas-mock/server"
	hlmock "github.com/recomma/hyperliquid-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/cmd/recomma/internal/config"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/storage"
	"github.com/stretchr/testify/require"
)

// E2ETestHarness manages all components for end-to-end testing
type E2ETestHarness struct {
	t *testing.T

	// Application under test
	App *App

	// Mock servers
	ThreeCommasMock *threecommasmock.TestServer
	HyperliquidMock *hlmock.TestServer

	// Direct access for assertions
	Store *storage.Storage

	// HTTP client for API testing
	HTTPClient *http.Client

	// Generated test credentials
	HLPrivateKey *ecdsa.PrivateKey
	HLWallet     string
	VenueID      string
}

// NewE2ETestHarness creates a new E2E test harness with all dependencies
func NewE2ETestHarness(t *testing.T) *E2ETestHarness {
	t.Helper()

	// Create mock servers
	tcMock := threecommasmock.NewTestServer(t)
	hlMock := hlmock.NewTestServer(t)

	// Generate Hyperliquid test credentials
	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	require.True(t, ok)
	wallet := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	venueID := "hyperliquid:test"

	// Create test secrets with Hyperliquid venue
	secrets := &vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-api-key",
			THREECOMMASPRIVATEKEY: "test-private-key",
			THREECOMMASPLANTIER:   "expert",
			Venues: []vault.VenueSecret{
				{
					ID:          venueID,
					Type:        "hyperliquid",
					DisplayName: "Test Hyperliquid",
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
		tc.WithPlanTier("expert"),
	)
	require.NoError(t, err)

	// Create test configuration
	cfg := config.DefaultConfig()
	cfg.HTTPListen = "127.0.0.1:0" // Random port
	cfg.StoragePath = ":memory:"
	cfg.Debug = true
	cfg.OrderWorkers = 2
	cfg.OrderScalerMaxMultiplier = 10.0

	// Create app with test dependencies
	ctx := context.Background()
	app, err := NewApp(ctx, AppOptions{
		Config:            cfg,
		ThreeCommasClient: tcClient,
		VaultSecrets:      secrets, // Bypass vault authentication
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
		VenueID:         venueID,
	}
}

// Start starts the application (non-blocking)
func (h *E2ETestHarness) Start(ctx context.Context) {
	h.t.Helper()

	// Start HTTP server
	h.App.StartHTTPServer()

	// Start all services (workers, periodic tasks, etc.)
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

	if h.App != nil {
		err := h.App.Shutdown(ctx)
		if err != nil {
			h.t.Logf("shutdown warning: %v", err)
		}
	}

	if h.ThreeCommasMock != nil {
		h.ThreeCommasMock.Close()
	}

	if h.HyperliquidMock != nil {
		h.HyperliquidMock.Close()
	}
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

// WaitForOrderSubmission polls until at least one order exists
func (h *E2ETestHarness) WaitForOrderSubmission(timeout time.Duration) {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.t.Fatal("timeout waiting for order submission")
		case <-ticker.C:
			orders := h.HyperliquidMock.GetAllOrders()
			if len(orders) > 0 {
				return
			}
		}
	}
}

// WaitForOrderInDatabase polls until order appears in database
func (h *E2ETestHarness) WaitForOrderInDatabase(timeout time.Duration) {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.t.Fatal("timeout waiting for order in database")
		case <-ticker.C:
			orders, _, err := h.Store.ListOrders(ctx, api.ListOrdersOptions{})
			if err != nil {
				continue
			}
			if len(orders) > 0 {
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

// TriggerDealProduction triggers a single deal production cycle
func (h *E2ETestHarness) TriggerDealProduction(ctx context.Context) {
	h.t.Helper()

	h.App.ProduceActiveDealsOnce(ctx)
}
