package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewSystemStatusTracker(t *testing.T) {
	tracker := NewSystemStatusTracker()
	require.NotNil(t, tracker)
	require.NotNil(t, tracker.venueStatuses)
	require.True(t, tracker.threeCommasAvailable, "should start as available")
	require.False(t, tracker.engineRunning, "should start as not running")
}

func TestSystemStatusTracker_SetActiveDealsFunc(t *testing.T) {
	tracker := NewSystemStatusTracker()

	// Set function
	tracker.SetActiveDealsFunc(func() int {
		return 42
	})

	// Verify it's called in snapshot
	snapshot := tracker.Snapshot()
	require.Equal(t, 42, snapshot.Engine.ActiveDeals)
}

func TestSystemStatusTracker_ThreeCommasError(t *testing.T) {
	tracker := NewSystemStatusTracker()

	t.Run("SetThreeCommasError", func(t *testing.T) {
		errMsg := "connection timeout"
		tracker.SetThreeCommasError(errMsg)

		snapshot := tracker.Snapshot()
		require.NotNil(t, snapshot.ThreecommasApi)
		require.NotNil(t, snapshot.ThreecommasApi.Available)
		require.False(t, *snapshot.ThreecommasApi.Available)
		require.NotNil(t, snapshot.ThreecommasApi.LastError)
		require.Equal(t, errMsg, *snapshot.ThreecommasApi.LastError)
	})

	t.Run("ClearThreeCommasError", func(t *testing.T) {
		tracker.SetThreeCommasError("some error")
		tracker.ClearThreeCommasError()

		snapshot := tracker.Snapshot()
		require.NotNil(t, snapshot.ThreecommasApi)
		require.NotNil(t, snapshot.ThreecommasApi.Available)
		require.True(t, *snapshot.ThreecommasApi.Available)
		require.Nil(t, snapshot.ThreecommasApi.LastError)
		require.NotNil(t, snapshot.ThreecommasApi.LastSuccessfulCall)
		require.WithinDuration(t, time.Now().UTC(), *snapshot.ThreecommasApi.LastSuccessfulCall, 2*time.Second)
	})
}

func TestSystemStatusTracker_Engine(t *testing.T) {
	tracker := NewSystemStatusTracker()

	t.Run("SetEngineRunning", func(t *testing.T) {
		tracker.SetEngineRunning(true)

		snapshot := tracker.Snapshot()
		require.True(t, snapshot.Engine.Running)
	})

	t.Run("RecordEngineSync", func(t *testing.T) {
		tracker.RecordEngineSync()

		snapshot := tracker.Snapshot()
		require.NotNil(t, snapshot.Engine.LastSync)
		require.WithinDuration(t, time.Now().UTC(), *snapshot.Engine.LastSync, 2*time.Second)
	})
}

func TestSystemStatusTracker_VenueStatus(t *testing.T) {
	tracker := NewSystemStatusTracker()

	t.Run("SetVenueStatus", func(t *testing.T) {
		errMsg := "websocket disconnected"
		tracker.SetVenueStatus("hyperliquid", "0x1234", false, &errMsg)

		snapshot := tracker.Snapshot()
		require.NotNil(t, snapshot.HyperliquidVenues)
		require.Len(t, *snapshot.HyperliquidVenues, 1)

		venue := (*snapshot.HyperliquidVenues)[0]
		require.Equal(t, "hyperliquid", venue.VenueId)
		require.Equal(t, "0x1234", venue.Wallet)
		require.False(t, venue.Connected)
		require.NotNil(t, venue.LastError)
		require.Equal(t, errMsg, *venue.LastError)
	})

	t.Run("SetVenueError", func(t *testing.T) {
		// First set a venue as connected
		tracker.SetVenueStatus("hyperliquid", "0x5678", true, nil)

		// Then set an error
		errMsg := "rate limited"
		tracker.SetVenueError("hyperliquid", errMsg)

		snapshot := tracker.Snapshot()
		require.NotNil(t, snapshot.HyperliquidVenues)

		var venue *struct {
			Connected bool    `json:"connected"`
			LastError *string `json:"last_error"`
			VenueId   string  `json:"venue_id"`
			Wallet    string  `json:"wallet"`
		}
		for i := range *snapshot.HyperliquidVenues {
			if (*snapshot.HyperliquidVenues)[i].VenueId == "hyperliquid" {
				venue = &(*snapshot.HyperliquidVenues)[i]
				break
			}
		}

		require.NotNil(t, venue)
		require.False(t, venue.Connected)
		require.NotNil(t, venue.LastError)
		require.Equal(t, errMsg, *venue.LastError)
	})

	t.Run("ClearVenueError", func(t *testing.T) {
		// Set a venue with error
		errMsg := "connection failed"
		tracker.SetVenueStatus("hyperliquid2", "0x9abc", false, &errMsg)

		// Clear the error
		tracker.ClearVenueError("hyperliquid2")

		snapshot := tracker.Snapshot()
		require.NotNil(t, snapshot.HyperliquidVenues)

		var venue *struct {
			Connected bool    `json:"connected"`
			LastError *string `json:"last_error"`
			VenueId   string  `json:"venue_id"`
			Wallet    string  `json:"wallet"`
		}
		for i := range *snapshot.HyperliquidVenues {
			if (*snapshot.HyperliquidVenues)[i].VenueId == "hyperliquid2" {
				venue = &(*snapshot.HyperliquidVenues)[i]
				break
			}
		}

		require.NotNil(t, venue)
		require.True(t, venue.Connected)
		require.Nil(t, venue.LastError)
	})

	t.Run("SetVenueError on non-existent venue", func(t *testing.T) {
		// Should not panic or error
		tracker.SetVenueError("nonexistent", "some error")
		// Just verify it doesn't crash
	})

	t.Run("ClearVenueError on non-existent venue", func(t *testing.T) {
		// Should not panic or error
		tracker.ClearVenueError("nonexistent")
		// Just verify it doesn't crash
	})
}

func TestSystemStatusTracker_Snapshot(t *testing.T) {
	tracker := NewSystemStatusTracker()

	t.Run("complete snapshot", func(t *testing.T) {
		// Set up all components
		tracker.SetActiveDealsFunc(func() int { return 5 })
		tracker.SetEngineRunning(true)
		tracker.RecordEngineSync()
		tracker.SetThreeCommasError("test error")
		tracker.ClearThreeCommasError()

		errMsg := "test venue error"
		tracker.SetVenueStatus("hl1", "wallet1", true, nil)
		tracker.SetVenueStatus("hl2", "wallet2", false, &errMsg)

		snapshot := tracker.Snapshot()

		// Verify timestamp
		require.WithinDuration(t, time.Now().UTC(), snapshot.Timestamp, 2*time.Second)

		// Verify engine
		require.True(t, snapshot.Engine.Running)
		require.Equal(t, 5, snapshot.Engine.ActiveDeals)
		require.NotNil(t, snapshot.Engine.LastSync)

		// Verify 3commas
		require.NotNil(t, snapshot.ThreecommasApi)
		require.NotNil(t, snapshot.ThreecommasApi.Available)
		require.True(t, *snapshot.ThreecommasApi.Available)

		// Verify venues
		require.NotNil(t, snapshot.HyperliquidVenues)
		require.Len(t, *snapshot.HyperliquidVenues, 2)
	})

	t.Run("empty snapshot", func(t *testing.T) {
		emptyTracker := NewSystemStatusTracker()
		snapshot := emptyTracker.Snapshot()

		require.WithinDuration(t, time.Now().UTC(), snapshot.Timestamp, 2*time.Second)
		require.False(t, snapshot.Engine.Running)
		require.Equal(t, 0, snapshot.Engine.ActiveDeals)
		require.Nil(t, snapshot.Engine.LastSync)
	})
}
