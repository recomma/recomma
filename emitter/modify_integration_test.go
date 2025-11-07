package emitter

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestHyperLiquidEmitterModifyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	exchange, ts := newMockExchangeWithServer(t, nil)
	store := newModifyTestStore(t)

	emitter := NewHyperLiquidEmitter(exchange, nil, store)

	// Create initial order
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	cloid := oid.Hex()
	originalOrder := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	createWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionCreate, Create: &originalOrder},
		BotEvent: recomma.BotEvent{RowID: 1},
	}

	require.NoError(t, emitter.Emit(ctx, createWork))

	// Verify initial order
	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", storedOrder.Status)

	originalOid := storedOrder.Order.Oid
	originalPrice, _ := strconv.ParseFloat(storedOrder.Order.LimitPx, 64)
	originalSize, _ := strconv.ParseFloat(storedOrder.Order.Sz, 64)
	require.InDelta(t, 50000, originalPrice, 1e-6)
	require.InDelta(t, 1.0, originalSize, 1e-6)

	// Modify order
	modifyReq := hyperliquid.ModifyOrderRequest{
		Oid: originalOid,
		Order: hyperliquid.ModifyOrderRequestOrder{
			Price: 51000,
			Size:  1.5,
		},
	}

	modifyWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionModify, Modify: &modifyReq},
		BotEvent: recomma.BotEvent{RowID: 2},
	}

	require.NoError(t, emitter.Emit(ctx, modifyWork))

	// Verify modification
	modifiedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", modifiedOrder.Status)

	modifiedPrice, _ := strconv.ParseFloat(modifiedOrder.Order.LimitPx, 64)
	modifiedSize, _ := strconv.ParseFloat(modifiedOrder.Order.Sz, 64)
	require.InDelta(t, 51000, modifiedPrice, 1e-6)
	require.InDelta(t, 1.5, modifiedSize, 1e-6)

	// Verify modification was persisted
	action, found, err := store.LoadHyperliquidSubmission(ctx, oid)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, recomma.ActionModify, action.Type)
	require.NotNil(t, action.Modify)
}

func TestHyperLiquidEmitterModifyThenFill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	exchange, ts := newMockExchangeWithServer(t, nil)
	store := newModifyTestStore(t)

	emitter := NewHyperLiquidEmitter(exchange, nil, store)

	oid := orderid.OrderId{BotID: 10, DealID: 20, BotEventID: 30}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         3000,
		Size:          2.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	createWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionCreate, Create: &order},
		BotEvent: recomma.BotEvent{RowID: 1},
	}

	require.NoError(t, emitter.Emit(ctx, createWork))

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	originalOid := storedOrder.Order.Oid

	// Modify to increase size
	modifyReq := hyperliquid.ModifyOrderRequest{
		Oid: originalOid,
		Order: hyperliquid.ModifyOrderRequestOrder{
			Price: 3050,
			Size:  3.0,
		},
	}

	modifyWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionModify, Modify: &modifyReq},
		BotEvent: recomma.BotEvent{RowID: 2},
	}

	require.NoError(t, emitter.Emit(ctx, modifyWork))

	// Fill the modified order
	ts.FillOrder(cloid, 3050)

	filledOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "filled", filledOrder.Status)
}

func TestHyperLiquidEmitterModifyThenCancel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	exchange, ts := newMockExchangeWithServer(t, nil)
	store := newModifyTestStore(t)

	emitter := NewHyperLiquidEmitter(exchange, nil, store)

	oid := orderid.OrderId{BotID: 100, DealID: 200, BotEventID: 300}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "SOL",
		IsBuy:         false,
		Price:         100,
		Size:          10.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	createWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionCreate, Create: &order},
		BotEvent: recomma.BotEvent{RowID: 1},
	}

	require.NoError(t, emitter.Emit(ctx, createWork))

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	originalOid := storedOrder.Order.Oid

	// Modify
	modifyReq := hyperliquid.ModifyOrderRequest{
		Oid: originalOid,
		Order: hyperliquid.ModifyOrderRequestOrder{
			Price: 105,
			Size:  8.0,
		},
	}

	modifyWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionModify, Modify: &modifyReq},
		BotEvent: recomma.BotEvent{RowID: 2},
	}

	require.NoError(t, emitter.Emit(ctx, modifyWork))

	// Cancel
	cancelReq := hyperliquid.CancelOrderRequestByCloid{
		Coin:  order.Coin,
		Cloid: cloid,
	}

	cancelWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionCancel, Cancel: &cancelReq},
		BotEvent: recomma.BotEvent{RowID: 3},
	}

	require.NoError(t, emitter.Emit(ctx, cancelWork))

	canceledOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "canceled", canceledOrder.Status)
}

func TestHyperLiquidEmitterMultipleModifications(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	exchange, ts := newMockExchangeWithServer(t, nil)
	store := newModifyTestStore(t)

	emitter := NewHyperLiquidEmitter(exchange, nil, store)

	oid := orderid.OrderId{BotID: 500, DealID: 600, BotEventID: 700}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ARB",
		IsBuy:         true,
		Price:         1.5,
		Size:          100.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	createWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionCreate, Create: &order},
		BotEvent: recomma.BotEvent{RowID: 1},
	}

	require.NoError(t, emitter.Emit(ctx, createWork))

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	currentOid := storedOrder.Order.Oid

	// First modification
	modifyReq1 := hyperliquid.ModifyOrderRequest{
		Oid: currentOid,
		Order: hyperliquid.ModifyOrderRequestOrder{
			Price: 1.45,
			Size:  150.0,
		},
	}

	modifyWork1 := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionModify, Modify: &modifyReq1},
		BotEvent: recomma.BotEvent{RowID: 2},
	}

	require.NoError(t, emitter.Emit(ctx, modifyWork1))

	// Get updated OID after first modification
	storedOrder, exists = ts.GetOrder(cloid)
	require.True(t, exists)
	currentOid = storedOrder.Order.Oid

	// Second modification
	modifyReq2 := hyperliquid.ModifyOrderRequest{
		Oid: currentOid,
		Order: hyperliquid.ModifyOrderRequestOrder{
			Price: 1.40,
			Size:  200.0,
		},
	}

	modifyWork2 := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionModify, Modify: &modifyReq2},
		BotEvent: recomma.BotEvent{RowID: 3},
	}

	require.NoError(t, emitter.Emit(ctx, modifyWork2))

	// Verify final state
	finalOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", finalOrder.Status)

	finalPrice, _ := strconv.ParseFloat(finalOrder.Order.LimitPx, 64)
	finalSize, _ := strconv.ParseFloat(finalOrder.Order.Sz, 64)
	require.InDelta(t, 1.40, finalPrice, 1e-6)
	require.InDelta(t, 200.0, finalSize, 1e-6)
}

func TestHyperLiquidEmitterModifyReduceOnlyOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	exchange, ts := newMockExchangeWithServer(t, nil)
	store := newModifyTestStore(t)

	emitter := NewHyperLiquidEmitter(exchange, nil, store)

	// Create a reduce-only order (e.g., take profit)
	oid := orderid.OrderId{BotID: 1000, DealID: 2000, BotEventID: 3000}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         false,
		Price:         55000,
		Size:          0.5,
		ClientOrderID: &cloid,
		ReduceOnly:    true,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	createWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionCreate, Create: &order},
		BotEvent: recomma.BotEvent{RowID: 1},
	}

	require.NoError(t, emitter.Emit(ctx, createWork))

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	originalOid := storedOrder.Order.Oid

	// Modify the reduce-only order
	modifyReq := hyperliquid.ModifyOrderRequest{
		Oid: originalOid,
		Order: hyperliquid.ModifyOrderRequestOrder{
			Price: 56000,
			Size:  0.75,
		},
	}

	modifyWork := recomma.OrderWork{
		OrderId:  oid,
		Action:   recomma.Action{Type: recomma.ActionModify, Modify: &modifyReq},
		BotEvent: recomma.BotEvent{RowID: 2},
	}

	require.NoError(t, emitter.Emit(ctx, modifyWork))

	modifiedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", modifiedOrder.Status)

	modifiedPrice, _ := strconv.ParseFloat(modifiedOrder.Order.LimitPx, 64)
	modifiedSize, _ := strconv.ParseFloat(modifiedOrder.Order.Sz, 64)
	require.InDelta(t, 56000, modifiedPrice, 1e-6)
	require.InDelta(t, 0.75, modifiedSize, 1e-6)
}

func newModifyTestStore(t *testing.T) *storage.Storage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "modify.db")
	store, err := storage.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}
