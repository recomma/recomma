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

type orderScalerStubStore struct {
	defaultState OrderScalerState
	overrides    map[uint32]*OrderScalerOverride
	stream       *StreamController
}

func newOrderScalerStubStore(stream *StreamController) *orderScalerStubStore {
	now := time.Now().UTC()
	return &orderScalerStubStore{
		defaultState: OrderScalerState{Multiplier: 1.0, UpdatedAt: now, UpdatedBy: "bootstrap"},
		overrides:    make(map[uint32]*OrderScalerOverride),
		stream:       stream,
	}
}

func (s *orderScalerStubStore) ListBots(context.Context, ListBotsOptions) ([]BotItem, *string, error) {
	return nil, nil, nil
}

func (s *orderScalerStubStore) ListDeals(context.Context, ListDealsOptions) ([]tc.Deal, *string, error) {
	return nil, nil, nil
}

func (s *orderScalerStubStore) ListOrders(context.Context, ListOrdersOptions) ([]OrderItem, *string, error) {
	return nil, nil, nil
}

func (s *orderScalerStubStore) ListOrderScalers(context.Context, ListOrderScalersOptions) ([]OrderScalerConfigItem, *string, error) {
	return nil, nil, nil
}

func (s *orderScalerStubStore) LoadHyperliquidSubmission(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error) {
	return recomma.Action{}, false, nil
}

func (s *orderScalerStubStore) LoadHyperliquidStatus(context.Context, recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	return nil, false, nil
}

func (s *orderScalerStubStore) ListSubmissionIdentifiersForOrder(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
	return nil, nil
}

func (s *orderScalerStubStore) ListVenues(context.Context) ([]VenueRecord, error) {
	return nil, nil
}

func (s *orderScalerStubStore) UpsertVenue(context.Context, string, VenueUpsertRequest) (VenueRecord, error) {
	return VenueRecord{}, nil
}

func (s *orderScalerStubStore) DeleteVenue(context.Context, string) error {
	return nil
}

func (s *orderScalerStubStore) ListVenueAssignments(context.Context, string) ([]VenueAssignmentRecord, error) {
	return nil, nil
}

func (s *orderScalerStubStore) UpsertVenueAssignment(context.Context, string, int64, bool) (VenueAssignmentRecord, error) {
	return VenueAssignmentRecord{}, nil
}

func (s *orderScalerStubStore) DeleteVenueAssignment(context.Context, string, int64) error {
	return nil
}

func (s *orderScalerStubStore) ListBotVenues(context.Context, int64) ([]BotVenueAssignmentRecord, error) {
	return nil, nil
}

func (s *orderScalerStubStore) GetDefaultOrderScaler(context.Context) (OrderScalerState, error) {
	return s.defaultState, nil
}

func (s *orderScalerStubStore) UpsertDefaultOrderScaler(ctx context.Context, multiplier float64, updatedBy string, notes *string) (OrderScalerState, error) {
	s.defaultState.Multiplier = multiplier
	s.defaultState.UpdatedBy = updatedBy
	s.defaultState.UpdatedAt = time.Now().UTC()
	s.defaultState.Notes = notes
	s.publishEvent(orderid.OrderId{}, updatedBy)
	return s.defaultState, nil
}

func (s *orderScalerStubStore) GetBotOrderScalerOverride(ctx context.Context, botID uint32) (*OrderScalerOverride, bool, error) {
	override, ok := s.overrides[botID]
	if !ok {
		return nil, false, nil
	}
	cloned := *override
	return &cloned, true, nil
}

func (s *orderScalerStubStore) UpsertBotOrderScalerOverride(ctx context.Context, botID uint32, multiplier *float64, notes *string, updatedBy string) (OrderScalerOverride, error) {
	now := time.Now().UTC()
	override := OrderScalerOverride{
		BotId:         int64(botID),
		Multiplier:    cloneFloat(multiplier),
		Notes:         cloneString(notes),
		EffectiveFrom: now,
		UpdatedAt:     now,
		UpdatedBy:     updatedBy,
	}
	s.overrides[botID] = &override
	s.publishEvent(orderid.OrderId{BotID: botID}, updatedBy)
	return override, nil
}

func (s *orderScalerStubStore) DeleteBotOrderScalerOverride(ctx context.Context, botID uint32, updatedBy string) error {
	delete(s.overrides, botID)
	s.publishEvent(orderid.OrderId{BotID: botID}, updatedBy)
	return nil
}

func (s *orderScalerStubStore) ResolveEffectiveOrderScalerConfig(ctx context.Context, oid orderid.OrderId) (EffectiveOrderScaler, error) {
	override, ok := s.overrides[oid.BotID]
	oidCopy := oid
	effective := EffectiveOrderScaler{
		Default:    s.defaultState,
		OrderId:    oidCopy.Hex(),
		Multiplier: s.defaultState.Multiplier,
		Source:     Default,
	}
	if ok && override != nil {
		cloned := *override
		effective.Override = &cloned
		if override.Multiplier != nil {
			effective.Multiplier = *override.Multiplier
			effective.Source = BotOverride
		}
	}
	return effective, nil
}

func (s *orderScalerStubStore) publishEvent(oid orderid.OrderId, actor string) {
	if s.stream == nil {
		return
	}
	effective, _ := s.ResolveEffectiveOrderScalerConfig(context.Background(), oid)
	ident := recomma.NewOrderIdentifier("", "", oid)
	identCopy := ident
	event := StreamEvent{
		Type:         OrderScalerConfigEntry,
		OrderID:      oid,
		Identifier:   &identCopy,
		ObservedAt:   time.Now().UTC(),
		ScalerConfig: &effective,
		Actor:        &actor,
	}
	s.stream.Publish(event)
}

func cloneFloat(src *float64) *float64 {
	if src == nil {
		return nil
	}
	val := *src
	return &val
}

func cloneString(src *string) *string {
	if src == nil {
		return nil
	}
	val := *src
	return &val
}

func TestGetOrderScalerConfigRequiresSession(t *testing.T) {
	handler, _, cleanup := newOrderScalerTestHarness(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/order-scaler", nil)
	ctx = context.WithValue(ctx, httpRequestContextKey, req)

	resp, err := handler.GetOrderScalerConfig(ctx, GetOrderScalerConfigRequestObject{})
	require.NoError(t, err)
	_, unauthorized := resp.(GetOrderScalerConfig401Response)
	require.True(t, unauthorized, "expected 401 response")
}

func TestUpdateOrderScalerConfigValidation(t *testing.T) {
	handler, ctx, cleanup := newOrderScalerTestHarness(t)
	t.Cleanup(cleanup)

	zeroBody := UpdateOrderScalerConfigJSONRequestBody{Multiplier: 0}
	resp, err := handler.UpdateOrderScalerConfig(ctx, UpdateOrderScalerConfigRequestObject{Body: &zeroBody})
	require.NoError(t, err)
	_, badRequest := resp.(UpdateOrderScalerConfig400Response)
	require.True(t, badRequest)

	overBody := UpdateOrderScalerConfigJSONRequestBody{Multiplier: 3}
	resp, err = handler.UpdateOrderScalerConfig(ctx, UpdateOrderScalerConfigRequestObject{Body: &overBody})
	require.NoError(t, err)
	_, badRequest = resp.(UpdateOrderScalerConfig400Response)
	require.True(t, badRequest)
}

func TestBotOrderScalerOverrideLifecycle(t *testing.T) {
	handler, stream, ctx, cleanup := newOrderScalerHarnessWithStream(t)
	t.Cleanup(cleanup)

	streamCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events, err := stream.Subscribe(streamCtx, StreamFilter{})
	require.NoError(t, err)

	body := UpdateOrderScalerConfigJSONRequestBody{Multiplier: 1.5}
	resp, err := handler.UpdateOrderScalerConfig(ctx, UpdateOrderScalerConfigRequestObject{Body: &body})
	require.NoError(t, err)
	okResp, ok := resp.(UpdateOrderScalerConfig200JSONResponse)
	require.True(t, ok)
	require.InDelta(t, 1.5, okResp.Effective.Value, 1e-9)

	select {
	case evt := <-events:
		require.Equal(t, OrderScalerConfigEntry, evt.Type)
		require.NotNil(t, evt.ScalerConfig)
		require.InDelta(t, 1.5, evt.ScalerConfig.Multiplier, 1e-9)
	case <-time.After(2 * time.Second):
		t.Fatal("expected scaler config event")
	}

	overrideBody := UpsertBotOrderScalerConfigJSONRequestBody{Multiplier: 0.5}
	putResp, err := handler.UpsertBotOrderScalerConfig(ctx, UpsertBotOrderScalerConfigRequestObject{BotId: 42, Body: &overrideBody})
	require.NoError(t, err)
	putOK, ok := putResp.(UpsertBotOrderScalerConfig200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, putOK.Override)
	require.InDelta(t, 0.5, putOK.Effective.Value, 1e-9)
	require.Equal(t, BotOverride, putOK.Effective.Source)

	getResp, err := handler.GetBotOrderScalerConfig(ctx, GetBotOrderScalerConfigRequestObject{BotId: 42})
	require.NoError(t, err)
	getOK, ok := getResp.(GetBotOrderScalerConfig200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, getOK.Override)
	require.InDelta(t, 0.5, getOK.Effective.Value, 1e-9)

	delResp, err := handler.DeleteBotOrderScalerConfig(ctx, DeleteBotOrderScalerConfigRequestObject{BotId: 42})
	require.NoError(t, err)
	delOK, ok := delResp.(DeleteBotOrderScalerConfig200JSONResponse)
	require.True(t, ok)
	require.Nil(t, delOK.Override)
	require.InDelta(t, 1.5, delOK.Effective.Value, 1e-9)
	require.Equal(t, Default, delOK.Effective.Source)
}

func TestGetOrderScalerConfigClampsEffectiveMultiplier(t *testing.T) {
	handler, ctx, cleanup := newOrderScalerTestHarness(t)
	t.Cleanup(cleanup)

	store, ok := handler.store.(*orderScalerStubStore)
	require.True(t, ok)

	store.defaultState.Multiplier = 3.5

	resp, err := handler.GetOrderScalerConfig(ctx, GetOrderScalerConfigRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(GetOrderScalerConfig200JSONResponse)
	require.True(t, ok)
	require.InDelta(t, 2.0, okResp.Effective.Value, 1e-9)
	require.InDelta(t, 3.5, okResp.Default.Multiplier, 1e-9)
}

func TestGetBotOrderScalerConfigClampsEffectiveMultiplier(t *testing.T) {
	handler, ctx, cleanup := newOrderScalerTestHarness(t)
	t.Cleanup(cleanup)

	store, ok := handler.store.(*orderScalerStubStore)
	require.True(t, ok)

	now := time.Now().UTC()
	multiplier := 4.5
	store.overrides[42] = &OrderScalerOverride{
		BotId:         42,
		Multiplier:    &multiplier,
		EffectiveFrom: now,
		UpdatedAt:     now,
		UpdatedBy:     "tester",
	}

	resp, err := handler.GetBotOrderScalerConfig(ctx, GetBotOrderScalerConfigRequestObject{BotId: 42})
	require.NoError(t, err)

	okResp, ok := resp.(GetBotOrderScalerConfig200JSONResponse)
	require.True(t, ok)
	require.InDelta(t, 2.0, okResp.Effective.Value, 1e-9)
	require.NotNil(t, okResp.Override)
	require.NotNil(t, okResp.Override.Multiplier)
	require.InDelta(t, multiplier, *okResp.Override.Multiplier, 1e-9)
}

func TestMakeOrderLogEntryClampsEffectiveMultiplier(t *testing.T) {
	handler, _, cleanup := newOrderScalerTestHarness(t)
	t.Cleanup(cleanup)

	oid := orderid.OrderId{BotID: 7}
	metaHex := oid.Hex()
	effective := EffectiveOrderScaler{
		OrderId:    metaHex,
		Multiplier: 3.25,
		Source:     Default,
		Default:    OrderScalerState{Multiplier: 3.25},
	}

	entry, ok := handler.makeOrderLogEntry(context.Background(), oid, time.Now().UTC(), OrderScalerConfigEntry, nil, nil, nil, &effective, nil, nil, nil, nil, nil)
	require.True(t, ok)

	logEntry, err := entry.AsOrderScalerConfigLogEntry()
	require.NoError(t, err)
	require.InDelta(t, 2.0, logEntry.Config.Multiplier, 1e-9)
}

func newOrderScalerTestHarness(t *testing.T) (*ApiHandler, context.Context, func()) {
	stream := NewStreamController()
	store := newOrderScalerStubStore(stream)
	controller := vault.NewController(vault.StateUnsealed, vault.WithInitialUser(&vault.User{ID: 1, Username: "tester"}))
	handler := NewHandler(store, stream,
		WithVaultController(controller),
		WithOrderScalerMaxMultiplier(2.0),
	)

	expiry := time.Now().Add(time.Hour)
	token, err := handler.session.Issue(expiry)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/order-scaler", nil)
	req.AddCookie(&http.Cookie{Name: "recomma_session", Value: token})
	ctx := context.WithValue(context.Background(), httpRequestContextKey, req)

	cleanup := func() {}
	return handler, ctx, cleanup
}

func newOrderScalerHarnessWithStream(t *testing.T) (*ApiHandler, *StreamController, context.Context, func()) {
	stream := NewStreamController()
	store := newOrderScalerStubStore(stream)
	controller := vault.NewController(vault.StateUnsealed, vault.WithInitialUser(&vault.User{ID: 1, Username: "tester"}))
	handler := NewHandler(store, stream,
		WithVaultController(controller),
		WithOrderScalerMaxMultiplier(2.0),
	)

	expiry := time.Now().Add(time.Hour)
	token, err := handler.session.Issue(expiry)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/order-scaler", nil)
	req.AddCookie(&http.Cookie{Name: "recomma_session", Value: token})
	ctx := context.WithValue(context.Background(), httpRequestContextKey, req)

	cleanup := func() {}
	return handler, stream, ctx, cleanup
}
