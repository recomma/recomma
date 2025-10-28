package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/recomma/recomma/internal/vault"
)

func TestVaultUserLifecycle(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	// No user present at bootstrap.
	user, err := store.GetVaultUser(ctx)
	require.NoError(t, err)
	require.Nil(t, user)

	// Creating a user persists it and returns metadata.
	created, err := store.EnsureVaultUser(ctx, "alice")
	require.NoError(t, err)
	require.NotZero(t, created.ID)
	require.Equal(t, "alice", created.Username)
	require.False(t, created.CreatedAt.IsZero())

	// Subsequent ensure returns the same row.
	again, err := store.EnsureVaultUser(ctx, "alice")
	require.NoError(t, err)
	require.Equal(t, created.ID, again.ID)

	// Lookup helpers surface the same record.
	fetched, err := store.GetVaultUser(ctx)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	require.Equal(t, created.ID, fetched.ID)

	byID, err := store.GetVaultUserByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, byID)
	require.Equal(t, created.ID, byID.ID)

	byName, err := store.GetVaultUserByUsername(ctx, "alice")
	require.NoError(t, err)
	require.NotNil(t, byName)
	require.Equal(t, created.ID, byName.ID)

	// Deleting the user removes it.
	require.NoError(t, store.DeleteVaultUserByID(ctx, created.ID))

	none, err := store.GetVaultUser(ctx)
	require.NoError(t, err)
	require.Nil(t, none)

	noneByID, err := store.GetVaultUserByID(ctx, created.ID)
	require.NoError(t, err)
	require.Nil(t, noneByID)

	noneByName, err := store.GetVaultUserByUsername(ctx, "alice")
	require.NoError(t, err)
	require.Nil(t, noneByName)
}

func TestVaultPayloadLifecycle(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	created, err := store.EnsureVaultUser(ctx, "alice")
	require.NoError(t, err)

	payload, err := store.GetVaultPayloadForUser(ctx, created.ID)
	require.NoError(t, err)
	require.Nil(t, payload)

	fixed := time.Date(2025, time.January, 1, 10, 0, 0, 0, time.UTC)
	rawParams := json.RawMessage(`{"k":"v"}`)

	input := vault.PayloadInput{
		UserID:         created.ID,
		Version:        "v1",
		Ciphertext:     []byte{1, 2, 3},
		Nonce:          []byte{4, 5, 6},
		AssociatedData: []byte{7, 8},
		PRFParams:      rawParams,
		UpdatedAt:      fixed,
	}

	require.NoError(t, store.UpsertVaultPayload(ctx, input))

	stored, err := store.GetVaultPayloadForUser(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, "v1", stored.Version)
	require.Equal(t, []byte{1, 2, 3}, stored.Ciphertext)
	require.Equal(t, []byte{4, 5, 6}, stored.Nonce)
	require.Equal(t, []byte{7, 8}, stored.AssociatedData)
	require.Equal(t, json.RawMessage(`{"k":"v"}`), stored.PRFParams)
	require.True(t, stored.UpdatedAt.Equal(fixed))

	// Ensure returned slices are defensive copies.
	stored.Ciphertext[0] = 99
	stored.Nonce[0] = 98
	stored.AssociatedData[0] = 97
	stored.PRFParams[0] = 'x'

	refetched, err := store.GetVaultPayloadForUser(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, []byte{1, 2, 3}, refetched.Ciphertext)
	require.Equal(t, []byte{4, 5, 6}, refetched.Nonce)
	require.Equal(t, []byte{7, 8}, refetched.AssociatedData)
	require.Equal(t, json.RawMessage(`{"k":"v"}`), refetched.PRFParams)

	// Update the payload and ensure changes persist.
	updated := input
	updated.Version = "v2"
	updated.Ciphertext = []byte{9, 9}
	updated.Nonce = []byte{8, 8}
	updated.AssociatedData = nil
	updated.PRFParams = nil
	updated.UpdatedAt = fixed.Add(time.Hour)

	require.NoError(t, store.UpsertVaultPayload(ctx, updated))

	afterUpdate, err := store.GetVaultPayloadForUser(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "v2", afterUpdate.Version)
	require.Equal(t, []byte{9, 9}, afterUpdate.Ciphertext)
	require.Equal(t, []byte{8, 8}, afterUpdate.Nonce)
	require.Nil(t, afterUpdate.AssociatedData)
	require.Nil(t, afterUpdate.PRFParams)
	require.True(t, afterUpdate.UpdatedAt.Equal(fixed.Add(time.Hour)))

	// Deletion removes the payload.
	require.NoError(t, store.DeleteVaultPayloadForUser(ctx, created.ID))

	none, err := store.GetVaultPayloadForUser(ctx, created.ID)
	require.NoError(t, err)
	require.Nil(t, none)
}

func TestVaultPayloadInputValidation(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	// Missing user id.
	err := store.UpsertVaultPayload(ctx, vault.PayloadInput{})
	require.Error(t, err)

	// Missing version.
	err = store.UpsertVaultPayload(ctx, vault.PayloadInput{UserID: 1})
	require.Error(t, err)

	// Missing ciphertext.
	err = store.UpsertVaultPayload(ctx, vault.PayloadInput{UserID: 1, Version: "v"})
	require.Error(t, err)

	// Missing nonce.
	err = store.UpsertVaultPayload(ctx, vault.PayloadInput{UserID: 1, Version: "v", Ciphertext: []byte{1}})
	require.Error(t, err)

	// EnsureVaultUser rejects blank usernames.
	_, err = store.EnsureVaultUser(ctx, "   ")
	require.Error(t, err)
}

func TestWebauthnCredentialLifecycle(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	created, err := store.EnsureVaultUser(ctx, "alice")
	require.NoError(t, err)

	creds, err := store.ListWebauthnCredentialsByUser(ctx, created.ID)
	require.NoError(t, err)
	require.Empty(t, creds)

	credentialID := []byte{0x01, 0x02, 0x03}
	raw := json.RawMessage(`{"id":"cred1","foo":123}`)

	input := vault.CredentialInput{
		UserID:       created.ID,
		CredentialID: credentialID,
		Credential:   raw,
	}

	require.NoError(t, store.UpsertWebauthnCredential(ctx, input))

	fetched, err := store.GetWebauthnCredential(ctx, credentialID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	require.Equal(t, created.ID, fetched.UserID)
	require.Equal(t, credentialID, fetched.CredentialID)
	require.Equal(t, json.RawMessage(`{"id":"cred1","foo":123}`), fetched.Credential)

	creds, err = store.ListWebauthnCredentialsByUser(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, creds, 1)

	// Ensure defensive copies are returned.
	fetched.CredentialID[0] = 0xFF
	fetched.Credential[0] = 'X'

	refetched, err := store.GetWebauthnCredential(ctx, credentialID)
	require.NoError(t, err)
	require.Equal(t, []byte{0x01, 0x02, 0x03}, refetched.CredentialID)
	require.Equal(t, json.RawMessage(`{"id":"cred1","foo":123}`), refetched.Credential)

	updated := vault.CredentialInput{
		UserID:       created.ID,
		CredentialID: credentialID,
		Credential:   json.RawMessage(`{"id":"cred1","foo":456}`),
	}

	require.NoError(t, store.UpsertWebauthnCredential(ctx, updated))

	afterUpdate, err := store.GetWebauthnCredential(ctx, credentialID)
	require.NoError(t, err)
	require.Equal(t, json.RawMessage(`{"id":"cred1","foo":456}`), afterUpdate.Credential)

	require.NoError(t, store.DeleteWebauthnCredential(ctx, credentialID))

	none, err := store.GetWebauthnCredential(ctx, credentialID)
	require.NoError(t, err)
	require.Nil(t, none)

	creds, err = store.ListWebauthnCredentialsByUser(ctx, created.ID)
	require.NoError(t, err)
	require.Empty(t, creds)
}

func TestWebauthnCredentialValidation(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	err := store.UpsertWebauthnCredential(ctx, vault.CredentialInput{})
	require.Error(t, err)

	err = store.UpsertWebauthnCredential(ctx, vault.CredentialInput{UserID: 1})
	require.Error(t, err)

	err = store.UpsertWebauthnCredential(ctx, vault.CredentialInput{UserID: 1, CredentialID: []byte{1}})
	require.Error(t, err)

	_, err = store.GetWebauthnCredential(ctx, nil)
	require.Error(t, err)

	err = store.DeleteWebauthnCredential(ctx, nil)
	require.Error(t, err)
}
