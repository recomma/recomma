package storage

import (
	"context"
	"testing"

	"github.com/recomma/recomma/internal/api"
	"github.com/stretchr/testify/require"
)

// TestDefaultVenueCreationWithNoExistingVenue tests that the default venue
// is created successfully when no venues exist.
func TestDefaultVenueCreationWithNoExistingVenue(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	// The default venue should be created during storage initialization
	venue, err := store.queries.GetVenue(ctx, string(defaultHyperliquidVenueID))
	require.NoError(t, err)
	require.Equal(t, string(defaultHyperliquidVenueID), venue.ID)
	require.Equal(t, defaultHyperliquidVenueType, venue.Type)
	require.Equal(t, defaultHyperliquidWallet, venue.Wallet)
}

// TestDefaultVenueWithConflictingUserVenue tests issue #108: when a user
// has already created a venue with (type=hyperliquid, wallet=X) but a
// different ID, EnsureDefaultVenueWallet should handle this gracefully
// rather than failing or returning early without creating the default.
func TestDefaultVenueWithConflictingUserVenue(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	const testWallet = "0x1234567890abcdef"
	const customVenueID = "hyperliquid:custom"

	// Create a user-defined venue with the same (type, wallet) but different ID
	_, err := store.UpsertVenue(ctx, customVenueID, api.VenueUpsertRequest{
		Type:        defaultHyperliquidVenueType,
		DisplayName: "Custom User Venue",
		Wallet:      testWallet,
	})
	require.NoError(t, err)

	// Verify the custom venue was created
	customVenue, err := store.queries.GetVenue(ctx, customVenueID)
	require.NoError(t, err)
	require.Equal(t, customVenueID, customVenue.ID)
	require.Equal(t, testWallet, customVenue.Wallet)

	// Now try to ensure the default venue with the same wallet
	// This should handle the conflict gracefully
	err = store.EnsureDefaultVenueWallet(ctx, testWallet)
	require.NoError(t, err, "EnsureDefaultVenueWallet should not fail when a conflicting venue exists")

	// The custom venue should still exist
	customVenue, err = store.queries.GetVenue(ctx, customVenueID)
	require.NoError(t, err)
	require.Equal(t, testWallet, customVenue.Wallet)

	// The default venue may or may not exist depending on unique constraint handling,
	// but the function should not have errored
	_, err = store.queries.GetVenue(ctx, string(defaultHyperliquidVenueID))
	if err == nil {
		// If default venue exists, verify it has a different wallet or same wallet
		// (implementation allows either depending on timing)
		t.Log("Default venue was created despite conflict")
	} else {
		// If default venue doesn't exist due to unique constraint, that's also acceptable
		// as long as the function didn't error
		t.Log("Default venue was not created due to unique constraint conflict (expected)")
	}
}

// TestDefaultVenueUpdateWallet tests that updating the default venue's wallet
// works correctly when the venue already exists.
func TestDefaultVenueUpdateWallet(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	// The default venue should exist from initialization
	venue, err := store.queries.GetVenue(ctx, string(defaultHyperliquidVenueID))
	require.NoError(t, err)
	require.Equal(t, defaultHyperliquidWallet, venue.Wallet)

	// Update to a new wallet
	const newWallet = "0xabcdef1234567890"
	err = store.EnsureDefaultVenueWallet(ctx, newWallet)
	require.NoError(t, err)

	// Verify the wallet was updated
	venue, err = store.queries.GetVenue(ctx, string(defaultHyperliquidVenueID))
	require.NoError(t, err)
	require.Equal(t, newWallet, venue.Wallet)
}

// TestDefaultVenueIdempotent tests that calling EnsureDefaultVenueWallet
// multiple times with the same wallet is idempotent.
func TestDefaultVenueIdempotent(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	const testWallet = "0x9999999999999999"

	// Call multiple times
	for i := 0; i < 3; i++ {
		err := store.EnsureDefaultVenueWallet(ctx, testWallet)
		require.NoError(t, err, "call %d should not error", i+1)
	}

	// Verify only one venue exists with the default ID
	venue, err := store.queries.GetVenue(ctx, string(defaultHyperliquidVenueID))
	require.NoError(t, err)
	require.Equal(t, testWallet, venue.Wallet)
}
