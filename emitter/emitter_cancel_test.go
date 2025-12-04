package emitter

import (
	"context"
	"errors"
	"testing"

	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestHyperLiquidEmitterTreatsAlreadyClosedCancelAsSatisfied(t *testing.T) {
	t.Parallel()

	exchange := &stubHyperliquidExchange{
		cancelErr: errors.New("Order was never placed, already canceled, or filled. asset=173"),
	}
	store := newTestStorage(t)
	emitter := NewHyperLiquidEmitter(exchange, "hyperliquid:test", nil, store, stubConstraints{})

	orderID := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := recomma.NewOrderIdentifier("hyperliquid:test", "0xwallet", orderID)

	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    orderID,
		Action: recomma.Action{
			Type: recomma.ActionCancel,
			Cancel: hyperliquid.CancelOrderRequestByCloid{
				Coin:  "DOGE",
				Cloid: orderID.Hex(),
			},
			Reason: "position closed; cancel stale take profit",
		},
	}

	err := emitter.Emit(context.Background(), work)
	require.ErrorIs(t, err, recomma.ErrOrderAlreadySatisfied, "cancel should be treated as satisfied when Hyperliquid reports it already closed")
}

type stubHyperliquidExchange struct {
	cancelErr error
}

func (s *stubHyperliquidExchange) Order(context.Context, hyperliquid.CreateOrderRequest, *hyperliquid.BuilderInfo) (hyperliquid.OrderStatus, error) {
	return hyperliquid.OrderStatus{}, errors.New("not implemented")
}

func (s *stubHyperliquidExchange) CancelByCloid(context.Context, string, string) (*hyperliquid.APIResponse[hyperliquid.CancelOrderResponse], error) {
	return nil, s.cancelErr
}

func (s *stubHyperliquidExchange) ModifyOrder(context.Context, hyperliquid.ModifyOrderRequest) (hyperliquid.OrderStatus, error) {
	return hyperliquid.OrderStatus{}, errors.New("not implemented")
}

type stubConstraints struct{}

func (stubConstraints) Resolve(context.Context, string) (hl.CoinConstraints, error) {
	return hl.CoinConstraints{}, nil
}

func newTestStorage(t *testing.T) *storage.Storage {
	t.Helper()
	store, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}
