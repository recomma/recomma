package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net/http"
	"testing"
	"time"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	threecommasmock "github.com/recomma/3commas-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	hlmock "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/cmd/recomma/internal/config"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/stretchr/testify/require"
)

// generateTestRSAKeyPEM generates an RSA private key and returns it as PEM-encoded bytes
func generateTestRSAKeyPEM(t *testing.T) []byte {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	return privateKeyPEM
}

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

	// Test vault secrets for programmatic unseal
	testSecrets *vault.Secrets
}

// NewE2ETestHarness creates a new E2E test harness with all dependencies
func NewE2ETestHarness(t *testing.T, ctx context.Context) *E2ETestHarness {
	t.Helper()

	// Create mock servers
	tcMock := threecommasmock.NewTestServer(t)
	hlMock := hlmock.NewTestServer(t)

	// Generate ThreeCommas RSA credentials
	rsaKeyPEM := generateTestRSAKeyPEM(t)

	// Generate Hyperliquid test credentials
	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	require.True(t, ok)
	wallet := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	venueID := "hyperliquid:test"

	// Build test vault secrets for programmatic unseal
	now := time.Now().UTC()
	testSecrets := &vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-api-key",
			THREECOMMASPRIVATEKEY: string(rsaKeyPEM),
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{
					ID:           venueID,
					Type:         "hyperliquid",
					DisplayName:  "Test Hyperliquid",
					Wallet:       wallet,
					PrivateKey:   hex.EncodeToString(gethCrypto.FromECDSA(privateKey)),
					APIURL:       hlMock.URL(),
					WebsocketURL: hlMock.WebSocketURL(),
					Primary:      true,
				},
			},
		},
		ReceivedAt: now,
	}

	// Create 3commas client pointing to mock
	tcClient, err := tc.New3CommasClient(
		tc.WithClientOption(tc.WithBaseURL(tcMock.URL())),
		tc.WithAPIKey("test-api-key"),
		tc.WithPrivatePEM(rsaKeyPEM),
		tc.WithPlanTier(recomma.ThreeCommasPlanTierExpert.SDKTier()),
	)
	require.NoError(t, err)

	// Create test configuration
	cfg := config.DefaultConfig()
	cfg.HTTPListen = "127.0.0.1:0" // Random port
	cfg.StoragePath = ":memory:"
	cfg.Debug = false
	cfg.OrderWorkers = 2
	cfg.OrderScalerMaxMultiplier = 10.0

	// Create app with test dependencies - vault will be unsealed programmatically
	app, err := NewApp(ctx, AppOptions{
		Config:            cfg,
		ThreeCommasClient: tcClient,
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
		testSecrets:     testSecrets,
	}
}

// Start starts the application (non-blocking)
func (h *E2ETestHarness) Start(ctx context.Context) {
	h.t.Helper()

	// Start HTTP server
	h.App.StartHTTPServer()

	// Unseal vault using production API (same as UI does)
	err := h.App.VaultController.Unseal(*h.testSecrets, nil)
	require.NoError(h.t, err)

	// Start all services (workers, periodic tasks, etc.)
	err = h.App.Start(ctx)
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

// WaitForOrderSubmission polls until the specified order appears in Hyperliquid mock
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
