package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"

	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
)

type vaultUpdateStubStore struct {
	user             *vault.User
	payload          *vault.Payload
	upsertErr        error
	upsertCallCount  int
	lastPayloadInput *vault.PayloadInput
	venuesUpserted   []string
	stream           *StreamController
}

func newVaultUpdateStubStore(stream *StreamController) *vaultUpdateStubStore {
	return &vaultUpdateStubStore{
		stream:          stream,
		venuesUpserted:  []string{},
		upsertCallCount: 0,
	}
}

func issueSessionCookie(t *testing.T, handler *ApiHandler, req *http.Request) {
	t.Helper()

	require.NotNil(t, handler.session)

	expiry := time.Now().Add(30 * time.Minute)
	token, err := handler.session.Issue(expiry)
	require.NoError(t, err)

	req.AddCookie(&http.Cookie{
		Name:  vaultSessionCookieName,
		Value: token,
		Path:  "/",
	})
}

// Implement Store interface methods
func (s *vaultUpdateStubStore) ListBots(context.Context, ListBotsOptions) ([]BotItem, *string, error) {
	return nil, nil, nil
}

func (s *vaultUpdateStubStore) ListDeals(context.Context, ListDealsOptions) ([]tc.Deal, *string, error) {
	return nil, nil, nil
}

func (s *vaultUpdateStubStore) ListOrders(context.Context, ListOrdersOptions) ([]OrderItem, *string, error) {
	return nil, nil, nil
}

func (s *vaultUpdateStubStore) ListOrderScalers(context.Context, ListOrderScalersOptions) ([]OrderScalerConfigItem, *string, error) {
	return nil, nil, nil
}

func (s *vaultUpdateStubStore) GetDefaultOrderScaler(context.Context) (OrderScalerState, error) {
	return OrderScalerState{}, nil
}

func (s *vaultUpdateStubStore) UpsertDefaultOrderScaler(context.Context, float64, string, *string) (OrderScalerState, error) {
	return OrderScalerState{}, nil
}

func (s *vaultUpdateStubStore) GetBotOrderScalerOverride(context.Context, uint32) (*OrderScalerOverride, bool, error) {
	return nil, false, nil
}

func (s *vaultUpdateStubStore) UpsertBotOrderScalerOverride(context.Context, uint32, *float64, *string, string) (OrderScalerOverride, error) {
	return OrderScalerOverride{}, nil
}

func (s *vaultUpdateStubStore) DeleteBotOrderScalerOverride(context.Context, uint32, string) error {
	return nil
}

func (s *vaultUpdateStubStore) ResolveEffectiveOrderScalerConfig(context.Context, orderid.OrderId) (EffectiveOrderScaler, error) {
	return EffectiveOrderScaler{}, nil
}

func (s *vaultUpdateStubStore) LoadHyperliquidSubmission(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error) {
	return recomma.Action{}, false, nil
}

func (s *vaultUpdateStubStore) LoadHyperliquidStatus(context.Context, recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	return nil, false, nil
}

func (s *vaultUpdateStubStore) ListSubmissionIdentifiersForOrder(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
	return nil, nil
}

func (s *vaultUpdateStubStore) ListVenues(context.Context) ([]VenueRecord, error) {
	return nil, nil
}

func (s *vaultUpdateStubStore) UpsertVenue(ctx context.Context, venueID string, payload VenueUpsertRequest) (VenueRecord, error) {
	s.venuesUpserted = append(s.venuesUpserted, venueID)
	return VenueRecord{VenueId: venueID}, nil
}

func (s *vaultUpdateStubStore) DeleteVenue(context.Context, string) error {
	return nil
}

func (s *vaultUpdateStubStore) ListVenueAssignments(context.Context, string) ([]VenueAssignmentRecord, error) {
	return nil, nil
}

func (s *vaultUpdateStubStore) UpsertVenueAssignment(context.Context, string, int64, bool) (VenueAssignmentRecord, error) {
	return VenueAssignmentRecord{}, nil
}

func (s *vaultUpdateStubStore) DeleteVenueAssignment(context.Context, string, int64) error {
	return nil
}

func (s *vaultUpdateStubStore) ListBotVenues(context.Context, int64) ([]BotVenueAssignmentRecord, error) {
	return nil, nil
}

// Implement vaultStorage interface
func (s *vaultUpdateStubStore) EnsureVaultUser(ctx context.Context, username string) (vault.User, error) {
	if s.user != nil {
		return *s.user, nil
	}
	return vault.User{}, errors.New("user not found")
}

func (s *vaultUpdateStubStore) GetVaultPayloadForUser(ctx context.Context, userID int64) (*vault.Payload, error) {
	if s.payload != nil && s.payload.UserID == userID {
		return s.payload, nil
	}
	return nil, nil
}

func (s *vaultUpdateStubStore) UpsertVaultPayload(ctx context.Context, input vault.PayloadInput) error {
	s.upsertCallCount++
	s.lastPayloadInput = &input
	if s.upsertErr != nil {
		return s.upsertErr
	}

	// Update the stored payload
	if s.payload == nil {
		s.payload = &vault.Payload{}
	}
	s.payload.UserID = input.UserID
	s.payload.Version = input.Version
	s.payload.Ciphertext = input.Ciphertext
	s.payload.Nonce = input.Nonce
	s.payload.AssociatedData = input.AssociatedData
	s.payload.PRFParams = input.PRFParams
	s.payload.UpdatedAt = input.UpdatedAt

	return nil
}

func TestUpdateVaultPayload_Success(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user
	store.payload = &vault.Payload{
		UserID:     1,
		Version:    "v1",
		Ciphertext: []byte("old-ciphertext"),
		Nonce:      []byte("old-nonce"),
	}

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{
					ID:         "hyperliquid:main",
					Type:       "hyperliquid",
					Wallet:     "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					Primary:    true,
					APIURL:     "https://api.hyperliquid.xyz",
				},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("new-ciphertext"),
				Nonce:      []byte("new-nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER(recomma.ThreeCommasPlanTierExpert),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	successResp, ok := resp.(UpdateVaultPayload200JSONResponse)
	require.True(t, ok, "expected 200 response")
	require.NotNil(t, successResp.Message)
	require.Equal(t, "Vault payload updated successfully", *successResp.Message)

	// Verify the payload was persisted
	require.Equal(t, 1, store.upsertCallCount)
	require.NotNil(t, store.lastPayloadInput)
	require.Equal(t, int64(1), store.lastPayloadInput.UserID)
	require.Equal(t, "v1", store.lastPayloadInput.Version)
	require.Equal(t, []byte("new-ciphertext"), store.lastPayloadInput.Ciphertext)
	require.Equal(t, []byte("new-nonce"), store.lastPayloadInput.Nonce)

	// Verify venue metadata was synced
	require.Contains(t, store.venuesUpserted, "hyperliquid:main")
}

func TestUpdateVaultPayload_MissingSession(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	// Create handler WITHOUT debug mode (requires real session)
	handler := NewHandler(store, stream, WithVaultController(vaultCtrl))

	req := UpdateVaultPayloadRequestObject{Body: &UpdateVaultPayloadJSONRequestBody{}}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload401Response)
	require.True(t, ok, "expected 401 response")
}

func TestUpdateVaultPayload_VaultSealed(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	require.NoError(t, vaultCtrl.Seal())

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{Body: &UpdateVaultPayloadJSONRequestBody{}}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload403Response)
	require.True(t, ok, "expected 403 response")
}

func TestUpdateVaultPayload_NoPrimaryVenue(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER(recomma.ThreeCommasPlanTierExpert),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  false, // No primary!
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload400Response)
	require.True(t, ok, "expected 400 response for no primary venue")

	// Verify payload was NOT persisted
	require.Equal(t, 0, store.upsertCallCount)
}

func TestUpdateVaultPayload_MissingThreeCommasPlanTier(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true, APIURL: "https://api.hyperliquid.xyz"},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   "",
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload400Response)
	require.True(t, ok, "expected 400 response for missing plan tier")
}

func TestUpdateVaultPayload_InvalidThreeCommasPlanTier(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true, APIURL: "https://api.hyperliquid.xyz"},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER("enterprise"),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload400Response)
	require.True(t, ok, "expected 400 response for invalid plan tier")
}

func TestUpdateVaultPayload_MultiplePrimaryVenues(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER(recomma.ThreeCommasPlanTierExpert),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
						{
							Id:         "hyperliquid:backup",
							Type:       "hyperliquid",
							Wallet:     "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
							PrivateKey: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
							IsPrimary:  true, // Multiple primaries!
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload400Response)
	require.True(t, ok, "expected 400 response for multiple primary venues")

	// Verify payload was NOT persisted
	require.Equal(t, 0, store.upsertCallCount)
}

func TestUpdateVaultPayload_DuplicateVenueIDs(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			THREECOMMASPLANTIER:   string(recomma.ThreeCommasPlanTierExpert),
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER(recomma.ThreeCommasPlanTierExpert),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
						{
							Id:         "hyperliquid:main", // Duplicate ID!
							Type:       "hyperliquid",
							Wallet:     "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
							PrivateKey: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
							IsPrimary:  false,
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload400Response)
	require.True(t, ok, "expected 400 response for duplicate venue IDs")

	// Verify payload was NOT persisted
	require.Equal(t, 0, store.upsertCallCount)
}

func TestUpdateVaultPayload_InvalidWalletAddress(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER(recomma.ThreeCommasPlanTierExpert),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0xinvalid", // Invalid wallet!
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload400Response)
	require.True(t, ok, "expected 400 response for invalid wallet address")

	// Verify payload was NOT persisted
	require.Equal(t, 0, store.upsertCallCount)
}

func TestUpdateVaultPayload_InvalidPrivateKey(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER(recomma.ThreeCommasPlanTierExpert),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "tooshort", // Invalid private key!
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload400Response)
	require.True(t, ok, "expected 400 response for invalid private key")

	// Verify payload was NOT persisted
	require.Equal(t, 0, store.upsertCallCount)
}

func TestUpdateVaultPayload_DatabaseError(t *testing.T) {
	stream := NewStreamController()
	store := newVaultUpdateStubStore(stream)
	store.upsertErr = errors.New("database error")

	user := vault.User{ID: 1, Username: "testuser"}
	store.user = &user

	vaultCtrl := vault.NewController(vault.StateSealed)
	vaultCtrl.SetUser(&user)
	secrets := vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     "test-key",
			THREECOMMASPRIVATEKEY: "test-secret",
			Venues: []vault.VenueSecret{
				{ID: "test", Type: "hyperliquid", Wallet: "0x1234567890123456789012345678901234567890",
					PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Primary: true},
			},
		},
	}
	expiry := time.Now().Add(30 * time.Minute)
	require.NoError(t, vaultCtrl.Unseal(secrets, &expiry))

	handler := NewHandler(store, stream, WithVaultController(vaultCtrl), WithDebugMode(true))

	req := UpdateVaultPayloadRequestObject{
		Body: &UpdateVaultPayloadJSONRequestBody{
			EncryptedPayload: VaultEncryptedPayload{
				Version:    "v1",
				Ciphertext: []byte("ciphertext"),
				Nonce:      []byte("nonce"),
			},
			DecryptedPayload: VaultSecretsBundle{
				NotSecret: VaultSecretsBundleNotSecret{Username: "testuser"},
				Secrets: VaultSecretsBundleSecrets{
					THREECOMMASAPIKEY:     "test-key",
					THREECOMMASPRIVATEKEY: "test-secret",
					THREECOMMASPLANTIER:   VaultSecretsBundleSecretsTHREECOMMASPLANTIER(recomma.ThreeCommasPlanTierExpert),
					Venues: []VaultVenueSecret{
						{
							Id:         "hyperliquid:main",
							Type:       "hyperliquid",
							Wallet:     "0x1234567890123456789012345678901234567890",
							PrivateKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
							IsPrimary:  true,
							ApiUrl:     stringPtr("https://api.hyperliquid.xyz"),
						},
					},
				},
			},
		},
	}

	httpReq := httptest.NewRequest(http.MethodPut, "/vault/payload", nil)
	issueSessionCookie(t, handler, httpReq)
	ctx := context.WithValue(context.Background(), httpRequestContextKey, httpReq)

	resp, err := handler.UpdateVaultPayload(ctx, req)
	require.NoError(t, err)

	_, ok := resp.(UpdateVaultPayload500Response)
	require.True(t, ok, "expected 500 response for database error")
}

func stringPtr(s string) *string {
	return &s
}
