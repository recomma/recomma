package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/recomma/recomma/internal/vault"
)

const (
	vaultSessionCookieName  = "recomma_session"
	vaultSessionTokenLength = 32
	defaultVaultSessionTTL  = 30 * time.Minute
	maxVaultSessionTTL      = 24 * time.Hour
	minVaultSessionTTL      = 1 * time.Minute
)

type vaultSession struct {
	token     string
	expiresAt time.Time
}

type vaultSessionManager struct {
	mu      sync.Mutex
	current *vaultSession
}

func newVaultSessionManager() *vaultSessionManager {
	return &vaultSessionManager{}
}

func (m *vaultSessionManager) Issue(expiry time.Time) (string, error) {
	if expiry.IsZero() {
		return "", errors.New("session expiry is required")
	}
	buf := make([]byte, vaultSessionTokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	m.mu.Lock()
	m.current = &vaultSession{token: token, expiresAt: expiry}
	m.mu.Unlock()
	return token, nil
}

func (m *vaultSessionManager) Validate(token string, now time.Time) (valid bool, expired bool) {
	if token == "" {
		return false, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil || m.current.token != token {
		return false, false
	}

	if now.After(m.current.expiresAt) {
		m.current = nil
		return false, true
	}

	return true, false
}

func (m *vaultSessionManager) Clear() {
	m.mu.Lock()
	m.current = nil
	m.mu.Unlock()
}

func (m *vaultSessionManager) Expiry() *time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	exp := m.current.expiresAt
	return &exp
}

type vaultStorage interface {
	EnsureVaultUser(ctx context.Context, username string) (vault.User, error)
	GetVaultPayloadForUser(ctx context.Context, userID int64) (*vault.Payload, error)
	UpsertVaultPayload(ctx context.Context, input vault.PayloadInput) error
}

func (h *ApiHandler) GetVaultPayload(ctx context.Context, request GetVaultPayloadRequestObject) (GetVaultPayloadResponseObject, error) {
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			h.logger.InfoContext(ctx, "GetVaultPayload unauthorized", slog.String("reason", "session expired"))
			return GetVaultPayload401Response{}, nil
		}
		h.logger.InfoContext(ctx, "GetVaultPayload unauthorized", slog.String("reason", "missing or invalid session"))
		return GetVaultPayload401Response{}, nil
	}

	st, err := h.controllerStatus(ctx)
	if err != nil {
		return GetVaultPayload500Response{}, nil
	}

	if st.State == vault.StateSetupRequired || st.User == nil {
		return GetVaultPayload423Response{}, nil
	}

	store, ok := h.store.(vaultStorage)
	if !ok {
		h.logger.ErrorContext(ctx, "vault payload without backing store")
		return GetVaultPayload500Response{}, nil
	}

	payload, err := store.GetVaultPayloadForUser(ctx, st.User.ID)
	if err != nil {
		h.logger.ErrorContext(ctx, "load vault payload", slog.String("error", err.Error()))
		return GetVaultPayload500Response{}, nil
	}
	if payload == nil {
		return GetVaultPayload404Response{}, nil
	}

	encrypted, err := convertVaultPayload(*payload)
	if err != nil {
		h.logger.ErrorContext(ctx, "encode vault payload", slog.String("error", err.Error()))
		return GetVaultPayload500Response{}, nil
	}

	return GetVaultPayload200JSONResponse(encrypted), nil
}

func (h *ApiHandler) UpdateVaultPayload(ctx context.Context, request UpdateVaultPayloadRequestObject) (UpdateVaultPayloadResponseObject, error) {
	if request.Body == nil {
		return UpdateVaultPayload400Response{}, nil
	}

	// Require valid session
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			h.logger.InfoContext(ctx, "UpdateVaultPayload unauthorized", slog.String("reason", "session expired"))
			return UpdateVaultPayload401Response{}, nil
		}
		h.logger.InfoContext(ctx, "UpdateVaultPayload unauthorized", slog.String("reason", "missing or invalid session"))
		return UpdateVaultPayload401Response{}, nil
	}

	// Check vault state
	st, err := h.controllerStatus(ctx)
	if err != nil {
		return UpdateVaultPayload500Response{}, nil
	}

	// Vault must be unsealed
	if st.State != vault.StateUnsealed {
		h.logger.InfoContext(ctx, "UpdateVaultPayload forbidden", slog.String("reason", "vault is sealed"))
		return UpdateVaultPayload403Response{}, nil
	}

	if st.User == nil {
		h.logger.ErrorContext(ctx, "UpdateVaultPayload: no user in vault state")
		return UpdateVaultPayload500Response{}, nil
	}

	// Validate the encrypted payload structure
	if err := validateEncryptedPayload(request.Body.EncryptedPayload); err != nil {
		h.logger.InfoContext(ctx, "UpdateVaultPayload invalid encrypted payload structure", slog.String("error", err.Error()))
		return UpdateVaultPayload400Response{}, nil
	}

	// Build and validate the decrypted secrets bundle
	secrets, err := buildSecretsBundle(request.Body.DecryptedPayload, h.now())
	if err != nil {
		h.logger.InfoContext(ctx, "UpdateVaultPayload invalid secrets bundle", slog.String("error", err.Error()))
		return UpdateVaultPayload400Response{}, nil
	}

	// Perform additional validation on the decrypted data
	if validateErr := secrets.Secrets.Validate(); validateErr != nil {
		h.logger.InfoContext(ctx, "UpdateVaultPayload validation failed", slog.String("error", validateErr.Error()))
		return UpdateVaultPayload400Response{}, nil
	}

	// Get storage interface
	store, ok := h.store.(vaultStorage)
	if !ok {
		h.logger.ErrorContext(ctx, "vault update without backing store")
		return UpdateVaultPayload500Response{}, nil
	}

	// Build payload input for storage (using encrypted version)
	save, err := buildPayloadInput(request.Body.EncryptedPayload, st.User.ID, h.now())
	if err != nil {
		h.logger.ErrorContext(ctx, "build payload input", slog.String("error", err.Error()))
		return UpdateVaultPayload500Response{}, nil
	}

	// Persist the updated payload (atomic transaction)
	if err := store.UpsertVaultPayload(ctx, save); err != nil {
		h.logger.ErrorContext(ctx, "update vault payload", slog.String("error", err.Error()))
		return UpdateVaultPayload500Response{}, nil
	}

	// Sync venue metadata to storage
	if syncErr := h.syncVenueMetadata(ctx, secrets.Secrets.Venues); syncErr != nil {
		h.logger.WarnContext(ctx, "sync venue metadata after update", slog.String("error", syncErr.Error()))
		// Don't fail the update if venue sync fails
	}

	h.logger.InfoContext(ctx, "vault payload updated successfully", slog.Int64("user_id", st.User.ID))

	return UpdateVaultPayload200JSONResponse{
		Message: "Vault payload updated successfully",
	}, nil
}

func (h *ApiHandler) GetVaultStatus(ctx context.Context, request GetVaultStatusRequestObject) (GetVaultStatusResponseObject, error) {
	status, err := h.controllerStatus(ctx)
	if err != nil {
		return GetVaultStatus500Response{}, nil
	}

	if h.session != nil {
		if exp := h.session.Expiry(); exp != nil {
			status.SessionExpiresAt = exp
		}
	}

	return GetVaultStatus200JSONResponse(h.convertControllerStatus(status)), nil
}

func (h *ApiHandler) SealVault(ctx context.Context, request SealVaultRequestObject) (SealVaultResponseObject, error) {
	if ok, expired := h.requireSession(ctx); !ok {
		if expired {
			return SealVault401Response{}, nil
		}
		return SealVault401Response{}, nil
	}

	if h.vault == nil {
		h.logger.ErrorContext(ctx, "seal requested without vault controller")
		return SealVault500Response{}, nil
	}

	if err := h.vault.Seal(); err != nil {
		if errors.Is(err, vault.ErrInvalidTransition) {
			return SealVault500Response{}, nil
		}
		h.logger.ErrorContext(ctx, "seal vault", slog.String("error", err.Error()))
		return SealVault500Response{}, nil
	}

	if h.session != nil {
		h.session.Clear()
	}

	status := h.convertControllerStatus(h.vault.Status())
	expiredCookie := (&http.Cookie{
		Name:     vaultSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}).String()

	return sealVault200JSONResponse{
		Body:   status,
		Cookie: expiredCookie,
	}, nil
}

func (h *ApiHandler) SetupVault(ctx context.Context, request SetupVaultRequestObject) (SetupVaultResponseObject, error) {
	if request.Body == nil {
		return SetupVault400Response{}, nil
	}

	if h.vault == nil {
		h.logger.ErrorContext(ctx, "vault setup without controller")
		return SetupVault500Response{}, nil
	}

	if h.vault.State() != vault.StateSetupRequired {
		return SetupVault409Response{}, nil
	}

	username := strings.TrimSpace(request.Body.Username)
	if username == "" {
		return SetupVault400Response{}, nil
	}

	if err := validateEncryptedPayload(request.Body.Payload); err != nil {
		h.logger.ErrorContext(ctx, "invalid setup payload", slog.String("error", err.Error()))
		return SetupVault400Response{}, nil
	}

	store, ok := h.store.(vaultStorage)
	if !ok {
		h.logger.ErrorContext(ctx, "vault setup without backing store")
		return SetupVault500Response{}, nil
	}

	user, err := store.EnsureVaultUser(ctx, username)
	if err != nil {
		h.logger.ErrorContext(ctx, "ensure vault user", slog.String("error", err.Error()))
		return SetupVault500Response{}, nil
	}

	existing, err := store.GetVaultPayloadForUser(ctx, user.ID)
	if err != nil {
		h.logger.ErrorContext(ctx, "fetch existing vault payload", slog.String("error", err.Error()))
		return SetupVault500Response{}, nil
	}
	if existing != nil {
		return SetupVault409Response{}, nil
	}

	save, err := buildPayloadInput(request.Body.Payload, user.ID, h.now())
	if err != nil {
		h.logger.ErrorContext(ctx, "build payload input", slog.String("error", err.Error()))
		return SetupVault400Response{}, nil
	}
	if err := store.UpsertVaultPayload(ctx, save); err != nil {
		h.logger.ErrorContext(ctx, "persist vault payload", slog.String("error", err.Error()))
		return SetupVault500Response{}, nil
	}

	h.vault.SetUser(&user)
	if err := h.vault.Seal(); err != nil {
		h.logger.ErrorContext(ctx, "seal after setup", slog.String("error", err.Error()))
		return SetupVault500Response{}, nil
	}
	if h.session != nil {
		h.session.Clear()
	}

	return SetupVault201JSONResponse(h.convertControllerStatus(h.vault.Status())), nil
}

func (h *ApiHandler) UnsealVault(ctx context.Context, request UnsealVaultRequestObject) (UnsealVaultResponseObject, error) {
	if request.Body == nil {
		return UnsealVault400Response{}, nil
	}

	if h.vault == nil {
		h.logger.ErrorContext(ctx, "vault unseal without controller")
		return UnsealVault500Response{}, nil
	}

	state := h.vault.State()
	if state == vault.StateSetupRequired {
		return UnsealVault423Response{}, nil
	}

	secrets, err := buildSecretsBundle(request.Body.Payload, h.now())
	if err != nil {
		h.logger.ErrorContext(ctx, "invalid secrets bundle", slog.String("error", err.Error()))
		return UnsealVault400Response{}, nil
	}

	if err := h.syncVenueMetadata(ctx, secrets.Secrets.Venues); err != nil {
		h.logger.ErrorContext(ctx, "sync venue metadata", slog.String("error", err.Error()))
		return UnsealVault500Response{}, nil
	}

	ttl := deriveSessionTTL(request.Body.RememberSessionSeconds)
	expiry := h.now().Add(ttl).UTC()

	if err := h.vault.Unseal(secrets, &expiry); err != nil {
		if errors.Is(err, vault.ErrInvalidTransition) {
			return UnsealVault409Response{}, nil
		}
		h.logger.ErrorContext(ctx, "unseal vault", slog.String("error", err.Error()))
		return UnsealVault500Response{}, nil
	}

	if h.session != nil {
		token, issueErr := h.session.Issue(expiry)
		if issueErr == nil {
			cookie := (&http.Cookie{
				Name:     vaultSessionCookieName,
				Value:    token,
				Path:     "/",
				Expires:  expiry,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			}).String()

			status := h.convertControllerStatus(h.vault.Status())
			status.SessionExpiresAt = &expiry

			return UnsealVault200JSONResponse{
				Body:    status,
				Headers: UnsealVault200ResponseHeaders{SetCookie: cookie},
			}, nil
		}
		h.logger.ErrorContext(ctx, "issue session token", slog.String("error", issueErr.Error()))
	}

	return UnsealVault500Response{}, nil
}

func (h *ApiHandler) requireSession(ctx context.Context) (valid bool, expired bool) {
	if h.debug {
		return true, false
	}

	if h.session == nil {
		h.logger.WarnContext(ctx, "requireSession called without session manager")
		return false, false
	}

	req, ok := requestFromContext(ctx)
	if !ok {
		h.logger.WarnContext(ctx, "request context missing http request")
		return false, false
	}

	cookie, err := req.Cookie(vaultSessionCookieName)
	if err != nil {
		if !errors.Is(err, http.ErrNoCookie) {
			h.logger.ErrorContext(ctx, "read session cookie", slog.String("error", err.Error()))
		} else {
			h.logger.DebugContext(ctx, "session cookie missing")
		}
		return false, false
	}

	valid, expired = h.session.Validate(cookie.Value, h.now())
	if !valid && expired && h.vault != nil {
		h.logger.InfoContext(ctx, "session expired")
		if sealErr := h.vault.Seal(); sealErr != nil && !errors.Is(sealErr, vault.ErrInvalidTransition) {
			h.logger.ErrorContext(ctx, "auto seal after session expiry", slog.String("error", sealErr.Error()))
		}
		h.session.Clear()
		return false, true
	}
	if !valid {
		h.logger.InfoContext(ctx, "session invalid")
	}
	return valid, expired
}

func (h *ApiHandler) controllerStatus(ctx context.Context) (vault.ControllerStatus, error) {
	if h.vault == nil {
		h.logger.ErrorContext(ctx, "vault controller unavailable")
		return vault.ControllerStatus{}, errors.New("vault controller unavailable")
	}
	return h.vault.Status(), nil
}

func (h *ApiHandler) convertControllerStatus(status vault.ControllerStatus) VaultStatus {
	apiStatus := VaultStatus{
		State: VaultState(status.State),
	}

	if status.User != nil {
		createdAt := status.User.CreatedAt
		apiStatus.User = &VaultUser{
			CreatedAt: &createdAt,
			Username:  status.User.Username,
		}
	}

	if status.SealedAt != nil {
		apiStatus.SealedAt = status.SealedAt
	}
	if status.UnsealedAt != nil {
		apiStatus.UnsealedAt = status.UnsealedAt
	}
	if status.SessionExpiresAt != nil {
		apiStatus.SessionExpiresAt = status.SessionExpiresAt
	}
	enabled := h.debug
	apiStatus.DebugMode = &enabled
	return apiStatus
}

func convertVaultPayload(payload vault.Payload) (VaultEncryptedPayload, error) {
	encrypted := VaultEncryptedPayload{
		Version:    payload.Version,
		Ciphertext: append([]byte(nil), payload.Ciphertext...),
		Nonce:      append([]byte(nil), payload.Nonce...),
	}

	if payload.AssociatedData != nil {
		ad := append([]byte(nil), payload.AssociatedData...)
		encrypted.AssociatedData = &ad
	}

	if len(payload.PRFParams) > 0 {
		var params map[string]interface{}
		if err := json.Unmarshal(payload.PRFParams, &params); err != nil {
			return VaultEncryptedPayload{}, fmt.Errorf("decode prf params: %w", err)
		}
		encrypted.PrfParams = &params
	}

	return encrypted, nil
}

func validateEncryptedPayload(payload VaultEncryptedPayload) error {
	if strings.TrimSpace(payload.Version) == "" {
		return errors.New("payload version is required")
	}
	if len(payload.Ciphertext) == 0 {
		return errors.New("payload ciphertext is required")
	}
	if len(payload.Nonce) == 0 {
		return errors.New("payload nonce is required")
	}
	return nil
}

func buildPayloadInput(payload VaultEncryptedPayload, userID int64, now time.Time) (vault.PayloadInput, error) {
	input := vault.PayloadInput{
		UserID:     userID,
		Version:    strings.TrimSpace(payload.Version),
		Ciphertext: append([]byte(nil), payload.Ciphertext...),
		Nonce:      append([]byte(nil), payload.Nonce...),
		UpdatedAt:  now.UTC(),
	}

	if payload.AssociatedData != nil {
		input.AssociatedData = append([]byte(nil), *payload.AssociatedData...)
	}

	if payload.PrfParams != nil {
		raw, err := json.Marshal(payload.PrfParams)
		if err != nil {
			return vault.PayloadInput{}, fmt.Errorf("encode prf params: %w", err)
		}
		input.PRFParams = raw
	}

	return input, nil
}

func buildSecretsBundle(bundle VaultSecretsBundle, now time.Time) (vault.Secrets, error) {
	username := strings.TrimSpace(bundle.NotSecret.Username)
	if username == "" {
		return vault.Secrets{}, errors.New("payload username is required")
	}

	threeCommasKey := strings.TrimSpace(bundle.Secrets.THREECOMMASAPIKEY)
	threeCommasSecret := strings.TrimSpace(bundle.Secrets.THREECOMMASPRIVATEKEY)
	if threeCommasKey == "" || threeCommasSecret == "" {
		return vault.Secrets{}, errors.New("missing required secret fields")
	}

	if len(bundle.Secrets.Venues) == 0 {
		return vault.Secrets{}, errors.New("vault secrets require at least one venue")
	}

	normalized := bundle
	normalized.NotSecret.Username = username
	normalized.Secrets.THREECOMMASAPIKEY = threeCommasKey
	normalized.Secrets.THREECOMMASPRIVATEKEY = threeCommasSecret
	normalized.Secrets.Venues = make([]VaultVenueSecret, 0, len(bundle.Secrets.Venues))

	venues := make([]vault.VenueSecret, 0, len(bundle.Secrets.Venues))
	primaryCount := 0

	for _, venue := range bundle.Secrets.Venues {
		id := strings.TrimSpace(venue.Id)
		venueType := strings.TrimSpace(venue.Type)
		wallet := strings.TrimSpace(venue.Wallet)
		privateKey := strings.TrimSpace(venue.PrivateKey)

		if id == "" || venueType == "" || wallet == "" || privateKey == "" {
			return vault.Secrets{}, errors.New("venue definitions must include id, type, wallet, and private key")
		}

		displayName := ""
		if venue.DisplayName != nil {
			displayName = strings.TrimSpace(*venue.DisplayName)
		}

		apiURL := ""
		if venue.ApiUrl != nil {
			apiURL = strings.TrimSpace(*venue.ApiUrl)
		}

		if strings.EqualFold(venueType, "hyperliquid") && apiURL == "" {
			return vault.Secrets{}, errors.New("hyperliquid venues require an api_url")
		}

		var flags map[string]interface{}
		if venue.Flags != nil {
			flags = cloneInterfaceMap(*venue.Flags)
		}

		if venue.IsPrimary {
			primaryCount++
		}

		normalizedVenue := VaultVenueSecret{
			Id:         id,
			Type:       venueType,
			Wallet:     wallet,
			PrivateKey: privateKey,
			IsPrimary:  venue.IsPrimary,
		}

		if displayName != "" {
			dn := displayName
			normalizedVenue.DisplayName = &dn
		}
		if apiURL != "" {
			url := apiURL
			normalizedVenue.ApiUrl = &url
		}
		if len(flags) > 0 {
			normFlags := cloneInterfaceMap(flags)
			normalizedVenue.Flags = &normFlags
		}

		normalized.Secrets.Venues = append(normalized.Secrets.Venues, normalizedVenue)
		venues = append(venues, vault.VenueSecret{
			ID:          id,
			Type:        venueType,
			DisplayName: displayName,
			Wallet:      wallet,
			PrivateKey:  privateKey,
			APIURL:      apiURL,
			Flags:       flags,
			Primary:     venue.IsPrimary,
		})
	}

	if primaryCount == 0 {
		return vault.Secrets{}, errors.New("vault secrets require at least one primary venue")
	}

	raw, err := json.Marshal(normalized)
	if err != nil {
		return vault.Secrets{}, fmt.Errorf("encode secrets bundle: %w", err)
	}

	return vault.Secrets{
		Secrets: vault.Data{
			THREECOMMASAPIKEY:     threeCommasKey,
			THREECOMMASPRIVATEKEY: threeCommasSecret,
			Venues:                venues,
		},
		Raw:        raw,
		ReceivedAt: now.UTC(),
	}, nil
}

func deriveSessionTTL(rememberSeconds *int64) time.Duration {
	ttl := defaultVaultSessionTTL
	if rememberSeconds != nil {
		if *rememberSeconds == 0 {
			return defaultVaultSessionTTL
		}
		if *rememberSeconds > 0 {
			ttl = time.Duration(*rememberSeconds) * time.Second
		}
	}

	if ttl < minVaultSessionTTL {
		ttl = minVaultSessionTTL
	}
	if ttl > maxVaultSessionTTL {
		ttl = maxVaultSessionTTL
	}
	return ttl
}

func (h *ApiHandler) syncVenueMetadata(ctx context.Context, venues []vault.VenueSecret) error {
	if len(venues) == 0 {
		return nil
	}

	if h.store == nil {
		return errors.New("api handler store unavailable for venue sync")
	}

	for _, venue := range venues {
		displayName := strings.TrimSpace(venue.DisplayName)
		if displayName == "" {
			displayName = venue.ID
		}

		flags := cloneInterfaceMap(venue.Flags)
		if venue.APIURL != "" {
			if flags == nil {
				flags = make(map[string]interface{})
			}
			if _, exists := flags["api_url"]; !exists {
				flags["api_url"] = venue.APIURL
			}
		}

		payload := VenueUpsertRequest{
			Type:        strings.TrimSpace(venue.Type),
			DisplayName: displayName,
			Wallet:      strings.TrimSpace(venue.Wallet),
		}

		if len(flags) > 0 {
			payload.Flags = &flags
		}

		if _, err := h.store.UpsertVenue(ctx, strings.TrimSpace(venue.ID), payload); err != nil {
			return fmt.Errorf("upsert venue %s: %w", venue.ID, err)
		}
	}

	return nil
}

func cloneInterfaceMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		if src == nil {
			return nil
		}
		return map[string]interface{}{}
	}

	cloned := make(map[string]interface{}, len(src))
	for key, value := range src {
		cloned[key] = value
	}
	return cloned
}

type sealVault200JSONResponse struct {
	Body   VaultStatus
	Cookie string
}

func (response sealVault200JSONResponse) VisitSealVaultResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	if response.Cookie != "" {
		w.Header().Add("Set-Cookie", response.Cookie)
	}
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response.Body)
}
