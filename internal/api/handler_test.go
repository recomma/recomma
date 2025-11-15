package api

import (
	"context"
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

// stubHandlerStore implements Store interface for testing handlers
type stubHandlerStore struct {
	bots              []BotItem
	deals             []tc.Deal
	orders            []OrderItem
	venues            []VenueRecord
	venueAssignments  map[string][]VenueAssignmentRecord
	botVenues         map[int64][]BotVenueAssignmentRecord
	orderScalers      []OrderScalerConfigItem
	defaultScaler     OrderScalerState
	botScalerOverride map[uint32]*OrderScalerOverride
}

func newStubHandlerStore() *stubHandlerStore {
	now := time.Now().UTC()
	return &stubHandlerStore{
		bots:              make([]BotItem, 0),
		deals:             make([]tc.Deal, 0),
		orders:            make([]OrderItem, 0),
		venues:            make([]VenueRecord, 0),
		venueAssignments:  make(map[string][]VenueAssignmentRecord),
		botVenues:         make(map[int64][]BotVenueAssignmentRecord),
		orderScalers:      make([]OrderScalerConfigItem, 0),
		defaultScaler:     OrderScalerState{Multiplier: 1.0, UpdatedAt: now, UpdatedBy: "test"},
		botScalerOverride: make(map[uint32]*OrderScalerOverride),
	}
}

func (s *stubHandlerStore) ListBots(ctx context.Context, opts ListBotsOptions) ([]BotItem, *string, error) {
	return s.bots, nil, nil
}

func (s *stubHandlerStore) ListDeals(ctx context.Context, opts ListDealsOptions) ([]tc.Deal, *string, error) {
	return s.deals, nil, nil
}

func (s *stubHandlerStore) ListOrders(ctx context.Context, opts ListOrdersOptions) ([]OrderItem, *string, error) {
	return s.orders, nil, nil
}

func (s *stubHandlerStore) ListOrderScalers(ctx context.Context, opts ListOrderScalersOptions) ([]OrderScalerConfigItem, *string, error) {
	return s.orderScalers, nil, nil
}

func (s *stubHandlerStore) LoadHyperliquidSubmission(ctx context.Context, ident recomma.OrderIdentifier) (recomma.Action, bool, error) {
	return recomma.Action{}, false, nil
}

func (s *stubHandlerStore) LoadHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	return nil, false, nil
}

func (s *stubHandlerStore) ListSubmissionIdentifiersForOrder(ctx context.Context, oid orderid.OrderId) ([]recomma.OrderIdentifier, error) {
	return nil, nil
}

func (s *stubHandlerStore) ListVenues(ctx context.Context) ([]VenueRecord, error) {
	return s.venues, nil
}

func (s *stubHandlerStore) UpsertVenue(ctx context.Context, venueID string, req VenueUpsertRequest) (VenueRecord, error) {
	venue := VenueRecord{
		VenueId:     venueID,
		Type:        req.Type,
		Wallet:      req.Wallet,
		DisplayName: req.DisplayName,
		Flags:       req.Flags,
	}
	s.venues = append(s.venues, venue)
	return venue, nil
}

func (s *stubHandlerStore) DeleteVenue(ctx context.Context, venueID string) error {
	for i, v := range s.venues {
		if v.VenueId == venueID {
			s.venues = append(s.venues[:i], s.venues[i+1:]...)
			break
		}
	}
	return nil
}

func (s *stubHandlerStore) ListVenueAssignments(ctx context.Context, venueID string) ([]VenueAssignmentRecord, error) {
	return s.venueAssignments[venueID], nil
}

func (s *stubHandlerStore) UpsertVenueAssignment(ctx context.Context, venueID string, botID int64, isPrimary bool) (VenueAssignmentRecord, error) {
	now := time.Now().UTC()
	assignment := VenueAssignmentRecord{
		VenueId:    venueID,
		BotId:      botID,
		IsPrimary:  isPrimary,
		AssignedAt: now,
	}
	s.venueAssignments[venueID] = append(s.venueAssignments[venueID], assignment)
	return assignment, nil
}

func (s *stubHandlerStore) DeleteVenueAssignment(ctx context.Context, venueID string, botID int64) error {
	assignments := s.venueAssignments[venueID]
	for i, a := range assignments {
		if a.BotId == botID {
			s.venueAssignments[venueID] = append(assignments[:i], assignments[i+1:]...)
			break
		}
	}
	return nil
}

func (s *stubHandlerStore) ListBotVenues(ctx context.Context, botID int64) ([]BotVenueAssignmentRecord, error) {
	return s.botVenues[botID], nil
}

func (s *stubHandlerStore) GetDefaultOrderScaler(ctx context.Context) (OrderScalerState, error) {
	return s.defaultScaler, nil
}

func (s *stubHandlerStore) UpsertDefaultOrderScaler(ctx context.Context, multiplier float64, updatedBy string, notes *string) (OrderScalerState, error) {
	s.defaultScaler.Multiplier = multiplier
	s.defaultScaler.UpdatedBy = updatedBy
	s.defaultScaler.UpdatedAt = time.Now().UTC()
	s.defaultScaler.Notes = notes
	return s.defaultScaler, nil
}

func (s *stubHandlerStore) GetBotOrderScalerOverride(ctx context.Context, botID uint32) (*OrderScalerOverride, bool, error) {
	override, ok := s.botScalerOverride[botID]
	if !ok {
		return nil, false, nil
	}
	return override, true, nil
}

func (s *stubHandlerStore) UpsertBotOrderScalerOverride(ctx context.Context, botID uint32, multiplier *float64, notes *string, updatedBy string) (OrderScalerOverride, error) {
	now := time.Now().UTC()
	override := OrderScalerOverride{
		BotId:         int64(botID),
		Multiplier:    multiplier,
		Notes:         notes,
		EffectiveFrom: now,
		UpdatedAt:     now,
		UpdatedBy:     updatedBy,
	}
	s.botScalerOverride[botID] = &override
	return override, nil
}

func (s *stubHandlerStore) DeleteBotOrderScalerOverride(ctx context.Context, botID uint32, updatedBy string) error {
	delete(s.botScalerOverride, botID)
	return nil
}

func (s *stubHandlerStore) ResolveEffectiveOrderScalerConfig(ctx context.Context, oid orderid.OrderId) (EffectiveOrderScaler, error) {
	effective := EffectiveOrderScaler{
		Default:    s.defaultScaler,
		OrderId:    oid.Hex(),
		Multiplier: s.defaultScaler.Multiplier,
		Source:     Default,
	}
	if override, ok := s.botScalerOverride[oid.BotID]; ok {
		effective.Override = override
		if override.Multiplier != nil {
			effective.Multiplier = *override.Multiplier
			effective.Source = BotOverride
		}
	}
	return effective, nil
}

// Helper to create a test handler with authenticated context
func newTestHandler(t *testing.T) (*ApiHandler, *stubHandlerStore, context.Context) {
	store := newStubHandlerStore()
	stream := NewStreamController()
	controller := vault.NewController(vault.StateUnsealed, vault.WithInitialUser(&vault.User{ID: 1, Username: "tester"}))

	handler := NewHandler(store, stream, WithVaultController(controller))

	// Create session token
	expiry := time.Now().Add(time.Hour)
	token, err := handler.session.Issue(expiry)
	require.NoError(t, err)

	// Create authenticated context
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "recomma_session", Value: token})
	ctx := context.WithValue(context.Background(), httpRequestContextKey, req)

	return handler, store, ctx
}

func TestListBots_WithData(t *testing.T) {
	handler, store, ctx := newTestHandler(t)

	// Add test bots to store
	now := time.Now().UTC()
	store.bots = []BotItem{
		{Bot: tc.Bot{Id: 1, Name: strPtr("Test Bot 1"), IsEnabled: true}, LastSyncedAt: now},
		{Bot: tc.Bot{Id: 2, Name: strPtr("Test Bot 2"), IsEnabled: false}, LastSyncedAt: now},
	}

	resp, err := handler.ListBots(ctx, ListBotsRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(ListBots200JSONResponse)
	require.True(t, ok, "expected 200 response")
	require.Len(t, okResp.Items, 2)
	require.EqualValues(t, 1, okResp.Items[0].BotId)
	require.True(t, okResp.Items[0].Payload.IsEnabled)
}

func TestListDeals_WithData(t *testing.T) {
	handler, store, ctx := newTestHandler(t)

	// Add test deals to store
	store.deals = []tc.Deal{
		{Id: 101, BotId: 1, Pair: "USDT_BTC"},
		{Id: 102, BotId: 1, Pair: "USDT_ETH"},
	}

	resp, err := handler.ListDeals(ctx, ListDealsRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(ListDeals200JSONResponse)
	require.True(t, ok, "expected 200 response")
	require.Len(t, okResp.Items, 2)
	require.EqualValues(t, 101, okResp.Items[0].DealId)
	require.EqualValues(t, 101, okResp.Items[0].Payload.Id)
	require.Equal(t, "USDT_BTC", okResp.Items[0].Payload.Pair)
}

func TestListOrders_WithData(t *testing.T) {
	handler, store, ctx := newTestHandler(t)

	// Add test orders to store
	now := time.Now().UTC()
	oid1 := orderid.OrderId{DealID: 101, BotID: 1, BotEventID: 1}
	oid2 := orderid.OrderId{DealID: 102, BotID: 1, BotEventID: 2}

	store.orders = []OrderItem{
		{
			OrderId:    oid1,
			ObservedAt: now,
			BotEvent: &tc.BotEvent{
				Coin: "BTC",
				Type: tc.BUY,
				Size: 0.001,
			},
		},
		{
			OrderId:    oid2,
			ObservedAt: now,
			BotEvent: &tc.BotEvent{
				Coin: "ETH",
				Type: tc.SELL,
				Size: 0.1,
			},
		},
	}

	resp, err := handler.ListOrders(ctx, ListOrdersRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(ListOrders200JSONResponse)
	require.True(t, ok, "expected 200 response")
	require.Len(t, okResp.Items, 2)
}

func TestListVenues_WithData(t *testing.T) {
	handler, store, ctx := newTestHandler(t)

	// Add test venues
	store.venues = []VenueRecord{
		{VenueId: "hl1", Type: "hyperliquid", Wallet: "0x1234", DisplayName: "HL 1"},
		{VenueId: "hl2", Type: "hyperliquid", Wallet: "0x5678", DisplayName: "HL 2"},
	}

	resp, err := handler.ListVenues(ctx, ListVenuesRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(ListVenues200JSONResponse)
	require.True(t, ok, "expected 200 response")
	require.Len(t, okResp.Items, 2)
	require.Equal(t, "hl1", okResp.Items[0].VenueId)
	require.Equal(t, "hyperliquid", okResp.Items[0].Type)
}

func TestUpsertVenue_Create(t *testing.T) {
	handler, store, ctx := newTestHandler(t)

	body := UpsertVenueJSONRequestBody{
		Type:        "hyperliquid",
		Wallet:      "0xabcd",
		DisplayName: "Test Venue",
	}

	resp, err := handler.UpsertVenue(ctx, UpsertVenueRequestObject{
		VenueId: "test-venue",
		Body:    &body,
	})
	require.NoError(t, err)

	okResp, ok := resp.(UpsertVenue200JSONResponse)
	require.True(t, ok, "expected 200 response")
	require.Equal(t, "test-venue", okResp.VenueId)
	require.Equal(t, "hyperliquid", okResp.Type)
	require.Equal(t, "0xabcd", okResp.Wallet)

	// Verify stored
	require.Len(t, store.venues, 1)
	require.Equal(t, "test-venue", store.venues[0].VenueId)
}

func TestDeleteVenue(t *testing.T) {
	handler, store, ctx := newTestHandler(t)

	// Add venue to delete
	store.venues = []VenueRecord{
		{VenueId: "to-delete", Type: "hyperliquid", Wallet: "0x1111", DisplayName: "Delete Me"},
	}

	resp, err := handler.DeleteVenue(ctx, DeleteVenueRequestObject{VenueId: "to-delete"})
	require.NoError(t, err)

	_, ok := resp.(DeleteVenue204Response)
	require.True(t, ok, "expected 204 response")

	// Verify deleted
	require.Empty(t, store.venues)
}

func TestListOrderScalers_WithData(t *testing.T) {
	handler, store, ctx := newTestHandler(t)

	// Add test scaler configs
	now := time.Now().UTC()
	oid1 := orderid.OrderId{BotID: 1, DealID: 101, BotEventID: 1}
	oid2 := orderid.OrderId{BotID: 2, DealID: 102, BotEventID: 1}

	store.orderScalers = []OrderScalerConfigItem{
		{
			OrderId:    oid1,
			ObservedAt: now,
			Actor:      "user1",
			Config: EffectiveOrderScaler{
				OrderId:    oid1.Hex(),
				Multiplier: 1.5,
				Source:     Default,
				Default:    OrderScalerState{Multiplier: 1.5, UpdatedAt: now, UpdatedBy: "user1"},
			},
		},
		{
			OrderId:    oid2,
			ObservedAt: now,
			Actor:      "user2",
			Config: EffectiveOrderScaler{
				OrderId:    oid2.Hex(),
				Multiplier: 2.0,
				Source:     Default,
				Default:    OrderScalerState{Multiplier: 2.0, UpdatedAt: now, UpdatedBy: "user2"},
			},
		},
	}

	resp, err := handler.ListOrderScalers(ctx, ListOrderScalersRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(ListOrderScalers200JSONResponse)
	require.True(t, ok, "expected 200 response")
	require.Len(t, okResp.Items, 2)
}
