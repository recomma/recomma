package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"testing"
	"time"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	threecommasmock "github.com/recomma/3commas-mock/server"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/cmd/recomma/internal/config"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/recomma"
	"github.com/stretchr/testify/require"
)

// TestApp_With3CommasMock verifies that the App can be created and started with a 3commas-mock server
func TestApp_With3CommasMock(t *testing.T) {
	t.Parallel()

	// Create mock 3commas server
	mockServer := threecommasmock.NewTestServer(t)
	defer mockServer.Close()

	// Configure mock state - add a simple bot
	mockServer.AddBot(threecommasmock.Bot{
		ID:      1,
		Name:    "Test Bot",
		Enabled: true,
	})

	// Generate test RSA key
	rsaKeyPEM := generateTestRSAKeyPEM(t)

	// Generate Hyperliquid test credentials
	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)
	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	require.True(t, ok)
	wallet := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	// Build test vault secrets
	testSecrets := &vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: string(rsaKeyPEM),
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{
					ID:          "hyperliquid:test",
					Type:        "hyperliquid",
					DisplayName: "Test Hyperliquid",
					Wallet:      wallet,
					PrivateKey:  hex.EncodeToString(gethCrypto.FromECDSA(privateKey)),
					APIURL:      "http://localhost:9999",
					Primary:     true,
				},
			},
		},
		ReceivedAt: time.Now().UTC(),
	}

	// Create SDK client pointing to mock
	client, err := tc.New3CommasClient(
		tc.WithClientOption(tc.WithBaseURL(mockServer.URL())),
		tc.WithAPIKey("test-key"),
		tc.WithPrivatePEM(rsaKeyPEM),
	)
	require.NoError(t, err)

	// Create test configuration
	cfg := config.DefaultConfig()
	cfg.StoragePath = ":memory:"
	cfg.HTTPListen = "127.0.0.1:0" // Random port
	cfg.Debug = false

	// Create app with mock client
	ctx := context.Background()
	app, err := NewApp(ctx, AppOptions{
		Config:            cfg,
		ThreeCommasClient: client,
	})
	require.NoError(t, err)
	require.NotNil(t, app)

	// Unseal vault using production API
	err = app.VaultController.Unseal(*testSecrets, nil)
	require.NoError(t, err)

	// Verify that the app was configured with the mock client
	require.NotNil(t, app.ThreeCommasClient)
	require.Equal(t, client, app.ThreeCommasClient)

	// Clean shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = app.Shutdown(shutdownCtx)
	require.NoError(t, err)

	t.Log("✅ Phase 2 test passed: App can be created with 3commas-mock")
}

// TestApp_StartWithMock verifies the app can start with mock dependencies
func TestApp_StartWithMock(t *testing.T) {
	t.Parallel()

	// Create mock server
	mockServer := threecommasmock.NewTestServer(t)
	defer mockServer.Close()

	// Add test data
	mockServer.AddBot(threecommasmock.Bot{
		ID:      1,
		Name:    "Integration Test Bot",
		Enabled: true,
	})

	mockServer.AddDeal(1, threecommasmock.Deal{
		ID:     101,
		BotID:  1,
		Status: "active",
		Pair:   "USDT_BTC",
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

	// Generate test RSA key
	rsaKeyPEM := generateTestRSAKeyPEM(t)

	// Generate Hyperliquid test credentials
	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)
	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	require.True(t, ok)
	wallet := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	// Build test vault secrets
	testSecrets := &vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: string(rsaKeyPEM),
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{
					ID:          "hyperliquid:test",
					Type:        "hyperliquid",
					DisplayName: "Test Hyperliquid",
					Wallet:      wallet,
					PrivateKey:  hex.EncodeToString(gethCrypto.FromECDSA(privateKey)),
					APIURL:      "http://localhost:9999",
					Primary:     true,
				},
			},
		},
		ReceivedAt: time.Now().UTC(),
	}

	// Create client
	client, err := tc.New3CommasClient(
		tc.WithClientOption(tc.WithBaseURL(mockServer.URL())),
		tc.WithAPIKey("test-key"),
		tc.WithPrivatePEM(rsaKeyPEM),
	)
	require.NoError(t, err)

	// Create app
	cfg := config.DefaultConfig()
	cfg.StoragePath = ":memory:"
	cfg.HTTPListen = "127.0.0.1:0"
	cfg.Debug = false

	ctx := context.Background()
	app, err := NewApp(ctx, AppOptions{
		Config:            cfg,
		ThreeCommasClient: client,
	})
	require.NoError(t, err)

	// Unseal vault using production API
	err = app.VaultController.Unseal(*testSecrets, nil)
	require.NoError(t, err)

	// Note: We can't call app.Start() here because it requires Hyperliquid venues
	// This test validates that the App can be created with mock dependencies
	// Full E2E tests will be in Phase 3-4

	// Shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = app.Shutdown(shutdownCtx)
	require.NoError(t, err)

	t.Log("✅ App can be created and shutdown with 3commas-mock and vault secrets")
}
