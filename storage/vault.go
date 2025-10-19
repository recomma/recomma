package storage

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "strings"
    "time"

    "github.com/terwey/recomma/internal/vault"
    "github.com/terwey/recomma/storage/sqlcgen"
)

// EnsureVaultUser inserts the user when missing and returns the persisted record.
func (s *Storage) EnsureVaultUser(ctx context.Context, username string) (vault.User, error) {
    if strings.TrimSpace(username) == "" {
        return vault.User{}, errors.New("username cannot be empty")
    }

    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    row, err := s.queries.EnsureVaultUser(ctx, username)
    if err != nil {
        return vault.User{}, err
    }

    return mapVaultUser(row), nil
}

// GetVaultUser returns the first vault user or nil when none exists.
func (s *Storage) GetVaultUser(ctx context.Context) (*vault.User, error) {
    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    row, err := s.queries.GetVaultUser(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    user := mapVaultUser(row)
    return &user, nil
}

// GetVaultUserByID fetches the user with the provided id.
func (s *Storage) GetVaultUserByID(ctx context.Context, id int64) (*vault.User, error) {
    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    row, err := s.queries.GetVaultUserByID(ctx, id)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    user := mapVaultUser(row)
    return &user, nil
}

// GetVaultUserByUsername fetches the user with the provided username.
func (s *Storage) GetVaultUserByUsername(ctx context.Context, username string) (*vault.User, error) {
    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    row, err := s.queries.GetVaultUserByUsername(ctx, username)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    user := mapVaultUser(row)
    return &user, nil
}

// DeleteVaultUserByID removes the user and cascades to the payloads.
func (s *Storage) DeleteVaultUserByID(ctx context.Context, id int64) error {
    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    return s.queries.DeleteVaultUserByID(ctx, id)
}

// UpsertVaultPayload stores the encrypted blob for the user.
func (s *Storage) UpsertVaultPayload(ctx context.Context, input vault.PayloadInput) error {
    if input.UserID == 0 {
        return errors.New("user id is required")
    }
    if strings.TrimSpace(input.Version) == "" {
        return errors.New("payload version is required")
    }
    if len(input.Ciphertext) == 0 {
        return errors.New("ciphertext is required")
    }
    if len(input.Nonce) == 0 {
        return errors.New("nonce is required")
    }

    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    now := input.UpdatedAt
    if now.IsZero() {
        now = time.Now().UTC()
    }

    params := sqlcgen.UpsertVaultPayloadParams{
        UserID:         input.UserID,
        Version:        input.Version,
        Ciphertext:     cloneBytes(input.Ciphertext),
        Nonce:          cloneBytes(input.Nonce),
        AssociatedData: cloneBytes(input.AssociatedData),
        PrfParams:      rawMessageOrNil(input.PRFParams),
        UpdatedAtUtc:   now.UnixMilli(),
    }

    return s.queries.UpsertVaultPayload(ctx, params)
}

// GetVaultPayloadForUser returns the stored payload for the user if present.
func (s *Storage) GetVaultPayloadForUser(ctx context.Context, userID int64) (*vault.Payload, error) {
    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    row, err := s.queries.GetVaultPayloadForUser(ctx, userID)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    payload := vault.Payload{
        ID:             row.ID,
        UserID:         row.UserID,
        Version:        row.Version,
        Ciphertext:     cloneBytes(row.Ciphertext),
        Nonce:          cloneBytes(row.Nonce),
        AssociatedData: cloneBytes(row.AssociatedData),
        PRFParams:      cloneRawMessage(row.PrfParams),
        UpdatedAt:      time.UnixMilli(row.UpdatedAtUtc).UTC(),
    }

    return &payload, nil
}

// DeleteVaultPayloadForUser removes the ciphertext blob for the user.
func (s *Storage) DeleteVaultPayloadForUser(ctx context.Context, userID int64) error {
    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    return s.queries.DeleteVaultPayloadForUser(ctx, userID)
}

// UpsertWebauthnCredential stores or replaces a serialized WebAuthn credential for the given user.
func (s *Storage) UpsertWebauthnCredential(ctx context.Context, input vault.CredentialInput) error {
    if input.UserID == 0 {
        return errors.New("user id is required")
    }
    if len(input.CredentialID) == 0 {
        return errors.New("credential id is required")
    }
    if len(input.Credential) == 0 {
        return errors.New("credential payload is required")
    }

    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    params := sqlcgen.UpsertWebauthnCredentialParams{
        UserID:       input.UserID,
        CredentialID: cloneBytes(input.CredentialID),
        Credential:   rawMessageOrNil(input.Credential),
    }

    return s.queries.UpsertWebauthnCredential(ctx, params)
}

// ListWebauthnCredentialsByUser fetches all stored credentials for the given user.
func (s *Storage) ListWebauthnCredentialsByUser(ctx context.Context, userID int64) ([]vault.Credential, error) {
    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    rows, err := s.queries.ListWebauthnCredentialsByUser(ctx, userID)
    if err != nil {
        return nil, err
    }

    creds := make([]vault.Credential, 0, len(rows))
    for _, row := range rows {
        creds = append(creds, vault.Credential{
            ID:           row.ID,
            UserID:       row.UserID,
            CredentialID: cloneBytes(row.CredentialID),
            Credential:   cloneRawMessage(row.Credential),
            CreatedAt:    time.UnixMilli(row.CreatedAtUtc).UTC(),
            UpdatedAt:    time.UnixMilli(row.UpdatedAtUtc).UTC(),
        })
    }

    return creds, nil
}

// GetWebauthnCredential fetches a credential by its binary credential identifier.
func (s *Storage) GetWebauthnCredential(ctx context.Context, credentialID []byte) (*vault.Credential, error) {
    if len(credentialID) == 0 {
        return nil, errors.New("credential id is required")
    }

    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    row, err := s.queries.GetWebauthnCredentialByID(ctx, credentialID)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    cred := &vault.Credential{
        ID:           row.ID,
        UserID:       row.UserID,
        CredentialID: cloneBytes(row.CredentialID),
        Credential:   cloneRawMessage(row.Credential),
        CreatedAt:    time.UnixMilli(row.CreatedAtUtc).UTC(),
        UpdatedAt:    time.UnixMilli(row.UpdatedAtUtc).UTC(),
    }

    return cred, nil
}

// DeleteWebauthnCredential removes a credential by its identifier.
func (s *Storage) DeleteWebauthnCredential(ctx context.Context, credentialID []byte) error {
    if len(credentialID) == 0 {
        return errors.New("credential id is required")
    }

    ctx = contextOrBackground(ctx)

    s.mu.Lock()
    defer s.mu.Unlock()

    return s.queries.DeleteWebauthnCredentialByID(ctx, credentialID)
}

func mapVaultUser(row sqlcgen.VaultUser) vault.User {
    return vault.User{
        ID:        row.ID,
        Username:  row.Username,
        CreatedAt: time.UnixMilli(row.CreatedAtUtc).UTC(),
    }
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

func cloneRawMessage(in []byte) json.RawMessage {
    if len(in) == 0 {
        if in == nil {
            return nil
        }
        return json.RawMessage{}
    }
    out := make([]byte, len(in))
    copy(out, in)
    return json.RawMessage(out)
}

func rawMessageOrNil(msg json.RawMessage) interface{} {
    if len(msg) == 0 {
        if msg == nil {
            return nil
        }
        return string(msg)
    }
    return string(msg)
}

func contextOrBackground(ctx context.Context) context.Context {
    if ctx != nil {
        return ctx
    }
    return context.Background()
}
