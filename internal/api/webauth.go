package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"

	"github.com/terwey/recomma/internal/vault"
)

const (
	defaultWebAuthnSessionTTL = 5 * time.Minute
	sessionTokenLength        = 32
)

// ErrWebAuthnSessionNotFound indicates the referenced session token does not exist.
var ErrWebAuthnSessionNotFound = errors.New("webauthn session not found")

// ErrWebAuthnSessionExpired indicates the server-side session expired before completion.
var ErrWebAuthnSessionExpired = errors.New("webauthn session expired")

// ErrWebAuthnUserNotFound indicates the vault user could not be located.
var ErrWebAuthnUserNotFound = errors.New("webauthn user not found")

// ErrWebAuthnNoCredentials indicates the user has not registered any credentials yet.
var ErrWebAuthnNoCredentials = errors.New("no registered webauthn credentials")

// WebAuthnStore defines the persistence needs for WebAuthn ceremonies.
type WebAuthnStore interface {
	EnsureVaultUser(ctx context.Context, username string) (vault.User, error)
	GetVaultUser(ctx context.Context) (*vault.User, error)
	GetVaultUserByID(ctx context.Context, id int64) (*vault.User, error)
	GetVaultUserByUsername(ctx context.Context, username string) (*vault.User, error)
	ListWebauthnCredentialsByUser(ctx context.Context, userID int64) ([]vault.Credential, error)
	UpsertWebauthnCredential(ctx context.Context, input vault.CredentialInput) error
	DeleteWebauthnCredential(ctx context.Context, credentialID []byte) error
}

// WebAuthnService orchestrates WebAuthn ceremonies backed by storage.
type WebAuthnService struct {
	wa                *webauthnlib.WebAuthn
	store             WebAuthnStore
	logger            *slog.Logger
	registrationCache *webauthnSessionCache
	loginCache        *webauthnSessionCache
	sessionTTL        time.Duration
}

// WebAuthnServiceConfig configures the WebAuthnService.
type WebAuthnServiceConfig struct {
	WebAuthn   *webauthnlib.WebAuthn
	Store      WebAuthnStore
	Logger     *slog.Logger
	SessionTTL time.Duration
}

// NewWebAuthnService constructs the service ensuring sane defaults.
func NewWebAuthnService(cfg WebAuthnServiceConfig) (*WebAuthnService, error) {
	if cfg.WebAuthn == nil {
		return nil, errors.New("webauthn configuration is required")
	}
	if cfg.Store == nil {
		return nil, errors.New("webauthn store is required")
	}

	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = defaultWebAuthnSessionTTL
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &WebAuthnService{
		wa:                cfg.WebAuthn,
		store:             cfg.Store,
		logger:            logger,
		registrationCache: newWebauthnSessionCache(),
		loginCache:        newWebauthnSessionCache(),
		sessionTTL:        ttl,
	}, nil
}

// BeginRegistration initializes a registration ceremony for the provided username.
func (s *WebAuthnService) BeginRegistration(ctx context.Context, username string) (*protocol.CredentialCreation, string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, "", errors.New("username is required")
	}

	vaultUser, err := s.store.EnsureVaultUser(ctx, username)
	if err != nil {
		return nil, "", fmt.Errorf("ensure vault user: %w", err)
	}

	weUser, err := s.buildWebAuthnUser(ctx, vaultUser)
	if err != nil {
		return nil, "", err
	}

	options := []webauthnlib.RegistrationOption{}
	if descriptors := credentialDescriptors(weUser.WebAuthnCredentials()); len(descriptors) > 0 {
		options = append(options, webauthnlib.WithExclusions(descriptors))
	}

	creation, sessionData, err := s.wa.BeginRegistration(weUser, options...)
	if err != nil {
		return nil, "", fmt.Errorf("begin registration: %w", err)
	}

	token, err := generateSessionToken()
	if err != nil {
		return nil, "", fmt.Errorf("generate session token: %w", err)
	}

	s.registrationCache.put(token, vaultUser.ID, *sessionData, s.sessionTTL)

	return creation, token, nil
}

// FinishRegistration verifies the client's response and stores the credential.
func (s *WebAuthnService) FinishRegistration(ctx context.Context, token string, parsed *protocol.ParsedCredentialCreationData) (*webauthnlib.Credential, error) {
	entry, ok := s.registrationCache.pop(token)
	if !ok {
		return nil, ErrWebAuthnSessionNotFound
	}
	if entry.expired() {
		return nil, ErrWebAuthnSessionExpired
	}
	if parsed == nil {
		return nil, errors.New("parsed credential cannot be nil")
	}

	weUser, err := s.buildWebAuthnUserByID(ctx, entry.userID)
	if err != nil {
		return nil, err
	}

	credential, err := s.wa.CreateCredential(weUser, entry.data, parsed)
	if err != nil {
		return nil, fmt.Errorf("finish registration: %w", err)
	}

	payload, err := json.Marshal(credential)
	if err != nil {
		return nil, fmt.Errorf("marshal credential: %w", err)
	}

	save := vault.CredentialInput{
		UserID:       entry.userID,
		CredentialID: credential.ID,
		Credential:   payload,
	}

	if err := s.store.UpsertWebauthnCredential(ctx, save); err != nil {
		return nil, fmt.Errorf("store credential: %w", err)
	}

	return credential, nil
}

// BeginLogin begins an assertion ceremony for the given username.
func (s *WebAuthnService) BeginLogin(ctx context.Context, username string) (*protocol.CredentialAssertion, string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, "", errors.New("username is required")
	}

	vaultUser, err := s.store.GetVaultUserByUsername(ctx, username)
	if err != nil {
		return nil, "", fmt.Errorf("get vault user: %w", err)
	}
	if vaultUser == nil {
		return nil, "", ErrWebAuthnUserNotFound
	}

	weUser, err := s.buildWebAuthnUser(ctx, *vaultUser)
	if err != nil {
		return nil, "", err
	}

	if len(weUser.credentials) == 0 {
		return nil, "", ErrWebAuthnNoCredentials
	}

	assertion, sessionData, err := s.wa.BeginLogin(weUser)
	if err != nil {
		return nil, "", fmt.Errorf("begin login: %w", err)
	}

	token, err := generateSessionToken()
	if err != nil {
		return nil, "", fmt.Errorf("generate session token: %w", err)
	}

	s.loginCache.put(token, weUser.userID, *sessionData, s.sessionTTL)

	return assertion, token, nil
}

// FinishLogin finalizes the assertion ceremony.
func (s *WebAuthnService) FinishLogin(ctx context.Context, token string, parsed *protocol.ParsedCredentialAssertionData) (*webauthnlib.Credential, error) {
	entry, ok := s.loginCache.pop(token)
	if !ok {
		return nil, ErrWebAuthnSessionNotFound
	}
	if entry.expired() {
		return nil, ErrWebAuthnSessionExpired
	}
	if parsed == nil {
		return nil, errors.New("parsed credential cannot be nil")
	}

	weUser, err := s.buildWebAuthnUserByID(ctx, entry.userID)
	if err != nil {
		return nil, err
	}

	credential, err := s.wa.ValidateLogin(weUser, entry.data, parsed)
	if err != nil {
		return nil, fmt.Errorf("finish login: %w", err)
	}

	payload, err := json.Marshal(credential)
	if err != nil {
		return nil, fmt.Errorf("marshal credential: %w", err)
	}

	save := vault.CredentialInput{
		UserID:       entry.userID,
		CredentialID: credential.ID,
		Credential:   payload,
	}

	if err := s.store.UpsertWebauthnCredential(ctx, save); err != nil {
		return nil, fmt.Errorf("update credential: %w", err)
	}

	return credential, nil
}

func (s *WebAuthnService) buildWebAuthnUser(ctx context.Context, user vault.User) (*webAuthnUser, error) {
	creds, err := s.store.ListWebauthnCredentialsByUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}

	converted := make([]webauthnlib.Credential, 0, len(creds))
	for _, cred := range creds {
		var decoded webauthnlib.Credential
		if err := json.Unmarshal(cred.Credential, &decoded); err != nil {
			return nil, fmt.Errorf("decode credential id %x: %w", cred.CredentialID, err)
		}
		converted = append(converted, decoded)
	}

	return &webAuthnUser{
		userID:      user.ID,
		id:          userHandleFromID(user.ID),
		username:    user.Username,
		displayName: user.Username,
		credentials: converted,
	}, nil
}

func (s *WebAuthnService) buildWebAuthnUserByID(ctx context.Context, userID int64) (*webAuthnUser, error) {
	user, err := s.store.GetVaultUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get vault user: %w", err)
	}
	if user == nil {
		return nil, ErrWebAuthnUserNotFound
	}
	return s.buildWebAuthnUser(ctx, *user)
}

type webAuthnUser struct {
	userID      int64
	id          []byte
	username    string
	displayName string
	credentials []webauthnlib.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte {
	return cloneBytes(u.id)
}

func (u *webAuthnUser) WebAuthnName() string {
	return u.username
}

func (u *webAuthnUser) WebAuthnDisplayName() string {
	return u.displayName
}

func (u *webAuthnUser) WebAuthnCredentials() []webauthnlib.Credential {
	creds := make([]webauthnlib.Credential, len(u.credentials))
	copy(creds, u.credentials)
	return creds
}

type webauthnSessionCache struct {
	mu      sync.RWMutex
	entries map[string]sessionEntry
}

type sessionEntry struct {
	userID int64
	data   webauthnlib.SessionData
	expiry time.Time
}

func (s sessionEntry) expired() bool {
	if s.expiry.IsZero() {
		return false
	}
	return time.Now().After(s.expiry)
}

func newWebauthnSessionCache() *webauthnSessionCache {
	return &webauthnSessionCache{entries: make(map[string]sessionEntry)}
}

func (c *webauthnSessionCache) put(token string, userID int64, data webauthnlib.SessionData, fallbackTTL time.Duration) {
	expiry := data.Expires
	if expiry.IsZero() {
		expiry = time.Now().Add(fallbackTTL)
	}
	c.mu.Lock()
	c.entries[token] = sessionEntry{userID: userID, data: data, expiry: expiry}
	c.mu.Unlock()
}

func (c *webauthnSessionCache) pop(token string) (sessionEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[token]
	if ok {
		delete(c.entries, token)
	}
	return entry, ok
}

func generateSessionToken() (string, error) {
	buf := make([]byte, sessionTokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func userHandleFromID(id int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(id))
	return b[:]
}

func credentialDescriptors(creds []webauthnlib.Credential) []protocol.CredentialDescriptor {
	descriptors := make([]protocol.CredentialDescriptor, len(creds))
	for i, cred := range creds {
		descriptors[i] = cred.Descriptor()
	}
	return descriptors
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		if in == nil {
			return nil
		}
		return []byte{}
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// BeginWebauthnRegistration handles `/webauthn/registration/begin`.
func (h *ApiHandler) BeginWebauthnRegistration(ctx context.Context, req BeginWebauthnRegistrationRequestObject) (BeginWebauthnRegistrationResponseObject, error) {
	if h.webauthn == nil {
		h.logger.ErrorContext(ctx, "webauthn registration begin without service")
		return BeginWebauthnRegistration500Response{}, nil
	}
	if req.Body == nil {
		return BeginWebauthnRegistration400Response{}, nil
	}

	username := strings.TrimSpace(req.Body.Username)
	if username == "" {
		return BeginWebauthnRegistration400Response{}, nil
	}

	creation, token, err := h.webauthn.BeginRegistration(ctx, username)
	if err != nil {
		h.logger.ErrorContext(ctx, "begin webauthn registration", slog.String("username", username), slog.String("error", err.Error()))
		return BeginWebauthnRegistration500Response{}, nil
	}

	creationMap, err := marshalToMap(creation.Response)
	if err != nil {
		h.logger.ErrorContext(ctx, "marshal webauthn creation options", slog.String("error", err.Error()))
		return BeginWebauthnRegistration500Response{}, nil
	}

	resp := BeginWebauthnRegistration200JSONResponse{
		SessionToken:    token,
		CreationOptions: creationMap,
	}
	return resp, nil
}

// FinishWebauthnRegistration handles `/webauthn/registration/finish`.
func (h *ApiHandler) FinishWebauthnRegistration(ctx context.Context, req FinishWebauthnRegistrationRequestObject) (FinishWebauthnRegistrationResponseObject, error) {
	if h.webauthn == nil {
		h.logger.ErrorContext(ctx, "webauthn registration finish without service")
		return FinishWebauthnRegistration500Response{}, nil
	}
	if req.Body == nil {
		return FinishWebauthnRegistration400Response{}, nil
	}

	token := strings.TrimSpace(req.Body.SessionToken)
	if token == "" {
		return FinishWebauthnRegistration400Response{}, nil
	}
	if len(req.Body.ClientResponse) == 0 {
		return FinishWebauthnRegistration400Response{}, nil
	}

	raw, err := json.Marshal(req.Body.ClientResponse)
	if err != nil {
		h.logger.ErrorContext(ctx, "encode webauthn registration response", slog.String("error", err.Error()))
		return FinishWebauthnRegistration400Response{}, nil
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(raw)
	if err != nil {
		return FinishWebauthnRegistration400Response{}, nil
	}

	if _, err := h.webauthn.FinishRegistration(ctx, token, parsed); err != nil {
		switch {
		case errors.Is(err, ErrWebAuthnSessionNotFound), errors.Is(err, ErrWebAuthnSessionExpired):
			return FinishWebauthnRegistration404Response{}, nil
		default:
			h.logger.ErrorContext(ctx, "finish webauthn registration", slog.String("error", err.Error()))
			return FinishWebauthnRegistration500Response{}, nil
		}
	}

	return FinishWebauthnRegistration200JSONResponse{Status: "registered"}, nil
}

// BeginWebauthnLogin handles `/webauthn/login/begin`.
func (h *ApiHandler) BeginWebauthnLogin(ctx context.Context, req BeginWebauthnLoginRequestObject) (BeginWebauthnLoginResponseObject, error) {
	if h.webauthn == nil {
		h.logger.ErrorContext(ctx, "webauthn login begin without service")
		return BeginWebauthnLogin500Response{}, nil
	}
	if req.Body == nil {
		return BeginWebauthnLogin400Response{}, nil
	}

	username := strings.TrimSpace(req.Body.Username)
	if username == "" {
		return BeginWebauthnLogin400Response{}, nil
	}

	assertion, token, err := h.webauthn.BeginLogin(ctx, username)
	if err != nil {
		switch {
		case errors.Is(err, ErrWebAuthnUserNotFound), errors.Is(err, ErrWebAuthnNoCredentials):
			return BeginWebauthnLogin404Response{}, nil
		default:
			h.logger.ErrorContext(ctx, "begin webauthn login", slog.String("username", username), slog.String("error", err.Error()))
			return BeginWebauthnLogin500Response{}, nil
		}
	}

	assertionMap, err := marshalToMap(assertion.Response)
	if err != nil {
		h.logger.ErrorContext(ctx, "marshal webauthn assertion options", slog.String("error", err.Error()))
		return BeginWebauthnLogin500Response{}, nil
	}

	resp := BeginWebauthnLogin200JSONResponse{
		SessionToken:     token,
		AssertionOptions: assertionMap,
	}
	return resp, nil
}

// FinishWebauthnLogin handles `/webauthn/login/finish`.
func (h *ApiHandler) FinishWebauthnLogin(ctx context.Context, req FinishWebauthnLoginRequestObject) (FinishWebauthnLoginResponseObject, error) {
	if h.webauthn == nil {
		h.logger.ErrorContext(ctx, "webauthn login finish without service")
		return FinishWebauthnLogin500Response{}, nil
	}
	if req.Body == nil {
		return FinishWebauthnLogin400Response{}, nil
	}

	token := strings.TrimSpace(req.Body.SessionToken)
	if token == "" {
		return FinishWebauthnLogin400Response{}, nil
	}
	if len(req.Body.ClientResponse) == 0 {
		return FinishWebauthnLogin400Response{}, nil
	}

	raw, err := json.Marshal(req.Body.ClientResponse)
	if err != nil {
		h.logger.ErrorContext(ctx, "encode webauthn assertion response", slog.String("error", err.Error()))
		return FinishWebauthnLogin400Response{}, nil
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(raw)
	if err != nil {
		return FinishWebauthnLogin400Response{}, nil
	}

	if _, err := h.webauthn.FinishLogin(ctx, token, parsed); err != nil {
		switch {
		case errors.Is(err, ErrWebAuthnSessionNotFound), errors.Is(err, ErrWebAuthnSessionExpired):
			return FinishWebauthnLogin404Response{}, nil
		default:
			h.logger.ErrorContext(ctx, "finish webauthn login", slog.String("error", err.Error()))
			return FinishWebauthnLogin500Response{}, nil
		}
	}

	var setCookie string
	if h.session != nil {
		expiry := h.now().Add(defaultVaultSessionTTL).UTC()
		if token, issueErr := h.session.Issue(expiry); issueErr == nil {
			setCookie = (&http.Cookie{
				Name:     vaultSessionCookieName,
				Value:    token,
				Path:     "/",
				Expires:  expiry,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			}).String()
		} else {
			h.logger.ErrorContext(ctx, "issue provisional session token", slog.String("error", issueErr.Error()))
		}
	} else {
		h.logger.WarnContext(ctx, "webAuthn login finished without session manager")
	}

	response := FinishWebauthnLogin200JSONResponse{
		Body:    WebAuthnLoginFinishResponse{Status: "authenticated"},
		Headers: FinishWebauthnLogin200ResponseHeaders{SetCookie: setCookie},
	}
	return response, nil
}
