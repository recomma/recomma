package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	hyperliquid "github.com/sonirico/go-hyperliquid"

	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
)

type cancelHandlerStore struct {
	*stubHandlerStore
	listIdentifiers func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error)
	loadSubmission  func(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error)
	loadStatus      func(context.Context, recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error)
}

func newCancelHandlerStore() *cancelHandlerStore {
	return &cancelHandlerStore{
		stubHandlerStore: newStubHandlerStore(),
	}
}

func (s *cancelHandlerStore) ListSubmissionIdentifiersForOrder(ctx context.Context, oid orderid.OrderId) ([]recomma.OrderIdentifier, error) {
	if s.listIdentifiers != nil {
		return s.listIdentifiers(ctx, oid)
	}
	return nil, nil
}

func (s *cancelHandlerStore) LoadHyperliquidSubmission(ctx context.Context, ident recomma.OrderIdentifier) (recomma.Action, bool, error) {
	if s.loadSubmission != nil {
		return s.loadSubmission(ctx, ident)
	}
	return recomma.Action{}, false, nil
}

func (s *cancelHandlerStore) LoadHyperliquidStatus(ctx context.Context, ident recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
	if s.loadStatus != nil {
		return s.loadStatus(ctx, ident)
	}
	return nil, false, nil
}

type stubEmitter struct {
	err   error
	works []recomma.OrderWork
}

func (e *stubEmitter) Emit(ctx context.Context, work recomma.OrderWork) error {
	if e.err != nil {
		return e.err
	}
	e.works = append(e.works, work)
	return nil
}

func newCancelHandler(t *testing.T, store Store, emitter recomma.Emitter) (*ApiHandler, context.Context) {
	t.Helper()

	stream := NewStreamController()
	controller := vault.NewController(vault.StateUnsealed, vault.WithInitialUser(&vault.User{ID: 1, Username: "tester"}))

	opts := []HandlerOption{WithVaultController(controller)}
	if emitter != nil {
		opts = append(opts, WithOrderEmitter(emitter))
	}

	handler := NewHandler(store, stream, opts...)

	expiry := time.Now().Add(time.Hour)
	token, err := handler.session.Issue(expiry)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/orders/oid/cancel", nil)
	req.AddCookie(&http.Cookie{Name: "recomma_session", Value: token})
	ctx := context.WithValue(context.Background(), httpRequestContextKey, req)
	return handler, ctx
}

func TestCancelOrderByOrderId_NoEmitter(t *testing.T) {
	store := newCancelHandlerStore()
	handler, ctx := newCancelHandler(t, store, nil)

	resp, err := handler.CancelOrderByOrderId(ctx, CancelOrderByOrderIdRequestObject{OrderId: "0x123"})
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId500Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_InvalidOrderId(t *testing.T) {
	store := newCancelHandlerStore()
	emitter := &stubEmitter{}
	handler, ctx := newCancelHandler(t, store, emitter)

	resp, err := handler.CancelOrderByOrderId(ctx, CancelOrderByOrderIdRequestObject{OrderId: "not-hex"})
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId400Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_ListIdentifiersError(t *testing.T) {
	store := newCancelHandlerStore()
	store.listIdentifiers = func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
		return nil, errors.New("boom")
	}
	emitter := &stubEmitter{}
	handler, ctx := newCancelHandler(t, store, emitter)

	req := cancelRequestObject(orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3})
	resp, err := handler.CancelOrderByOrderId(ctx, req)
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId500Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_NoIdentifiersFound(t *testing.T) {
	store := newCancelHandlerStore()
	store.listIdentifiers = func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
		return nil, nil
	}
	emitter := &stubEmitter{}
	handler, ctx := newCancelHandler(t, store, emitter)

	req := cancelRequestObject(orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3})
	resp, err := handler.CancelOrderByOrderId(ctx, req)
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId404Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_LoadSubmissionError(t *testing.T) {
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := recomma.NewOrderIdentifier("hyperliquid:test", "0xabc", oid)

	store := newCancelHandlerStore()
	store.listIdentifiers = func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
		return []recomma.OrderIdentifier{ident}, nil
	}
	store.loadSubmission = func(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error) {
		return recomma.Action{}, false, errors.New("load fail")
	}

	emitter := &stubEmitter{}
	handler, ctx := newCancelHandler(t, store, emitter)
	resp, err := handler.CancelOrderByOrderId(ctx, cancelRequestObject(oid))
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId500Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_SubmissionNotFound(t *testing.T) {
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := recomma.NewOrderIdentifier("hyperliquid:test", "0xabc", oid)

	store := newCancelHandlerStore()
	store.listIdentifiers = func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
		return []recomma.OrderIdentifier{ident}, nil
	}
	store.loadSubmission = func(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error) {
		return recomma.Action{}, false, nil
	}

	emitter := &stubEmitter{}
	handler, ctx := newCancelHandler(t, store, emitter)
	resp, err := handler.CancelOrderByOrderId(ctx, cancelRequestObject(oid))
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId404Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_MissingCoin(t *testing.T) {
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := recomma.NewOrderIdentifier("hyperliquid:test", "0xabc", oid)

	store := newCancelHandlerStore()
	store.listIdentifiers = func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
		return []recomma.OrderIdentifier{ident}, nil
	}
	store.loadSubmission = func(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error) {
		return recomma.Action{Type: recomma.ActionCreate}, true, nil
	}

	emitter := &stubEmitter{}
	handler, ctx := newCancelHandler(t, store, emitter)
	resp, err := handler.CancelOrderByOrderId(ctx, cancelRequestObject(oid))
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId404Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_OrderNotCancelable(t *testing.T) {
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := recomma.NewOrderIdentifier("hyperliquid:test", "0xabc", oid)

	store := newCancelHandlerStore()
	store.listIdentifiers = func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
		return []recomma.OrderIdentifier{ident}, nil
	}
	store.loadSubmission = func(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error) {
		return recomma.Action{
			Type: recomma.ActionCreate,
			Create: hyperliquid.CreateOrderRequest{
				Coin: "DOGE",
			},
		}, true, nil
	}
	store.loadStatus = func(context.Context, recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
		return &hyperliquid.WsOrder{
			Order:  hyperliquid.WsBasicOrder{Coin: "DOGE"},
			Status: hyperliquid.OrderStatusValueFilled,
		}, true, nil
	}

	emitter := &stubEmitter{}
	handler, ctx := newCancelHandler(t, store, emitter)
	resp, err := handler.CancelOrderByOrderId(ctx, cancelRequestObject(oid))
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId409Response)
	require.True(t, ok)
}

func TestCancelOrderByOrderId_EmitterFailure(t *testing.T) {
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := recomma.NewOrderIdentifier("hyperliquid:test", "0xabc", oid)

	store := newCancelHandlerStore()
	store.listIdentifiers = func(context.Context, orderid.OrderId) ([]recomma.OrderIdentifier, error) {
		return []recomma.OrderIdentifier{ident}, nil
	}
	store.loadSubmission = func(context.Context, recomma.OrderIdentifier) (recomma.Action, bool, error) {
		return recomma.Action{
			Type: recomma.ActionCreate,
			Create: hyperliquid.CreateOrderRequest{
				Coin: "DOGE",
			},
		}, true, nil
	}
	store.loadStatus = func(context.Context, recomma.OrderIdentifier) (*hyperliquid.WsOrder, bool, error) {
		return &hyperliquid.WsOrder{
			Order:  hyperliquid.WsBasicOrder{Coin: "DOGE"},
			Status: hyperliquid.OrderStatusValueOpen,
		}, true, nil
	}

	emitter := &stubEmitter{err: errors.New("emit failed")}
	handler, ctx := newCancelHandler(t, store, emitter)
	resp, err := handler.CancelOrderByOrderId(ctx, cancelRequestObject(oid))
	require.NoError(t, err)
	_, ok := resp.(CancelOrderByOrderId500Response)
	require.True(t, ok)
}

func cancelRequestObject(oid orderid.OrderId) CancelOrderByOrderIdRequestObject {
	return CancelOrderByOrderIdRequestObject{
		OrderId: oid.Hex(),
		Params:  CancelOrderByOrderIdParams{},
	}
}
