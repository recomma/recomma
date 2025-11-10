package api

import (
	"sync"
	"time"
)

// SystemStatusTracker tracks the current health state of system components
type SystemStatusTracker struct {
	mu sync.RWMutex

	// 3commas API status
	threeCommasAvailable       bool
	threeCommasLastError       *string
	threeCommasLastSuccessCall *time.Time

	// Engine status
	engineRunning   bool
	engineLastSync  *time.Time
	activeDealsFunc func() int // Function to get current active deal count

	// Venue statuses
	venueStatuses map[string]*VenueStatus
}

// VenueStatus tracks the health of a single venue
type VenueStatus struct {
	VenueID   string
	Wallet    string
	Connected bool
	LastError *string
}

// NewSystemStatusTracker creates a new status tracker
func NewSystemStatusTracker() *SystemStatusTracker {
	return &SystemStatusTracker{
		venueStatuses:        make(map[string]*VenueStatus),
		threeCommasAvailable: true, // Assume available until proven otherwise
		engineRunning:        false,
	}
}

// SetActiveDealsFunc sets the function to retrieve active deal count
func (s *SystemStatusTracker) SetActiveDealsFunc(fn func() int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeDealsFunc = fn
}

// SetThreeCommasError records an error from 3commas API
func (s *SystemStatusTracker) SetThreeCommasError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threeCommasAvailable = false
	s.threeCommasLastError = &err
}

// ClearThreeCommasError marks 3commas API as healthy
func (s *SystemStatusTracker) ClearThreeCommasError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threeCommasAvailable = true
	s.threeCommasLastError = nil
	now := time.Now().UTC()
	s.threeCommasLastSuccessCall = &now
}

// SetEngineRunning updates the engine running state
func (s *SystemStatusTracker) SetEngineRunning(running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engineRunning = running
}

// RecordEngineSync records a successful engine sync
func (s *SystemStatusTracker) RecordEngineSync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.engineLastSync = &now
}

// SetVenueStatus updates or creates venue status
func (s *SystemStatusTracker) SetVenueStatus(venueID, wallet string, connected bool, lastError *string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.venueStatuses[venueID] = &VenueStatus{
		VenueID:   venueID,
		Wallet:    wallet,
		Connected: connected,
		LastError: lastError,
	}
}

// SetVenueError records an error for a venue
func (s *SystemStatusTracker) SetVenueError(venueID string, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status, ok := s.venueStatuses[venueID]; ok {
		status.Connected = false
		status.LastError = &err
	}
}

// ClearVenueError marks a venue as healthy
func (s *SystemStatusTracker) ClearVenueError(venueID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status, ok := s.venueStatuses[venueID]; ok {
		status.Connected = true
		status.LastError = nil
	}
}

// Snapshot returns a point-in-time view of system status
func (s *SystemStatusTracker) Snapshot() SystemStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()

	var threeCommasAPI *SystemStatusThreecommasApi
	if s.threeCommasLastError != nil || s.threeCommasLastSuccessCall != nil {
		threeCommasAPI = &SystemStatusThreecommasApi{
			Available:          s.threeCommasAvailable,
			LastError:          s.threeCommasLastError,
			LastSuccessfulCall: s.threeCommasLastSuccessCall,
		}
	}

	var venues *[]SystemStatusHyperliquidVenuesItem
	if len(s.venueStatuses) > 0 {
		venueList := make([]SystemStatusHyperliquidVenuesItem, 0, len(s.venueStatuses))
		for _, v := range s.venueStatuses {
			venueList = append(venueList, SystemStatusHyperliquidVenuesItem{
				VenueId:   v.VenueID,
				Wallet:    v.Wallet,
				Connected: v.Connected,
				LastError: v.LastError,
			})
		}
		venues = &venueList
	}

	activeDeals := 0
	if s.activeDealsFunc != nil {
		activeDeals = s.activeDealsFunc()
	}

	return SystemStatus{
		Timestamp:        now,
		ThreecommasApi:   threeCommasAPI,
		HyperliquidVenues: venues,
		Engine: SystemStatusEngine{
			Running:     s.engineRunning,
			ActiveDeals: activeDeals,
			LastSync:    s.engineLastSync,
		},
	}
}
