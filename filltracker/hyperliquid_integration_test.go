package filltracker

import (
	"context"
	"crypto/ecdsa"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	mockserver "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

// TestFillTrackerWithHyperliquidStatusUpdates tests that the fill tracker
// correctly processes order status updates from HyperLiquid mock server
func TestFillTrackerWithHyperliquidStatusUpdates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newHyperliquidTestStore(t)
	tracker := New(store, nil)

	const (
		dealID = uint32(1000)
		botID  = uint32(2000)
		coin   = "BTC"
	)

	// Record deal
	recordDeal(t, store, dealID, botID, coin)

	// Create base order
	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseEvent := tc.BotEvent{
		CreatedAt:   time.Now().Add(-5 * time.Minute),
		Action:      tc.BotEventActionExecute,
		Coin:        coin,
		Type:        tc.BUY,
		Status:      tc.Active,
		Price:       50000,
		Size:        1.0,
		OrderType:   tc.MarketOrderDealOrderTypeBase,
		QuoteVolume: 50000,
		IsMarket:    false,
	}

	require.NoError(t, recordEvent(store, baseOid, baseEvent))

	// Create order in HyperLiquid mock
	exchange := newHyperliquidMockExchange(t, ts.URL())
	cloid := baseOid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          coin,
		IsBuy:         true,
		Price:         50000,
		Size:          1.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Get initial status and record it
	initialStatus := makeStatusFromMockOrder(ts, baseOid, coin)
	require.NoError(t, recordStatus(store, baseOid, initialStatus))
	require.NoError(t, tracker.Rebuild(ctx))

	// Simulate fill on HyperLiquid
	ts.FillOrder(cloid, 50000)

	// Get updated status and update tracker
	filledStatus := makeStatusFromMockOrder(ts, baseOid, coin)
	require.NoError(t, recordStatus(store, baseOid, filledStatus))
	require.NoError(t, tracker.UpdateStatus(ctx, baseOid, filledStatus))

	// Verify position
	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 1.0, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, 50000, snapshot.Position.TotalBuyValue, 1e-6)
}

// TestFillTrackerPartialFillsFromHyperliquid tests tracking of partial fills
func TestFillTrackerPartialFillsFromHyperliquid(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newHyperliquidTestStore(t)
	tracker := New(store, nil)

	const (
		dealID = uint32(3000)
		botID  = uint32(4000)
		coin   = "ETH"
	)

	recordDeal(t, store, dealID, botID, coin)

	oid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	event := tc.BotEvent{
		CreatedAt: time.Now().Add(-3 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Active,
		Price:     3000,
		Size:      10.0,
		OrderType: tc.MarketOrderDealOrderTypeBase,
	}

	require.NoError(t, recordEvent(store, oid, event))

	// Create order in mock
	exchange := newHyperliquidMockExchange(t, ts.URL())
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          coin,
		IsBuy:         true,
		Price:         3000,
		Size:          10.0,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, order, nil)
	require.NoError(t, err)

	// Initial status
	initialStatus := makeStatusFromMockOrder(ts, oid, coin)
	require.NoError(t, recordStatus(store, oid, initialStatus))
	require.NoError(t, tracker.Rebuild(ctx))

	// Partial fill (30%)
	ts.FillOrder(cloid, 3000, mockserver.WithFillSize(3.0))

	// Update tracker with partial fill
	partialStatus := makeStatusFromMockOrder(ts, oid, coin)
	require.NoError(t, recordStatus(store, oid, partialStatus))
	require.NoError(t, tracker.UpdateStatus(ctx, oid, partialStatus))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 3.0, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, 9000, snapshot.Position.TotalBuyValue, 1e-6)

	// Complete the fill
	ts.FillOrder(cloid, 3000) // Fill remaining

	filledStatus := makeStatusFromMockOrder(ts, oid, coin)
	require.NoError(t, recordStatus(store, oid, filledStatus))
	require.NoError(t, tracker.UpdateStatus(ctx, oid, filledStatus))

	snapshot, ok = tracker.Snapshot(dealID)
	require.True(t, ok)
	require.InDelta(t, 10.0, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, 30000, snapshot.Position.TotalBuyValue, 1e-6)
}

// TestFillTrackerTakeProfitCancellation tests take profit handling with real HyperLiquid status
func TestFillTrackerTakeProfitCancellation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newHyperliquidTestStore(t)
	tracker := New(store, nil)

	const (
		dealID = uint32(5000)
		botID  = uint32(6000)
		coin   = "SOL"
	)

	recordDeal(t, store, dealID, botID, coin)

	// Create and fill base order
	baseOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 1}
	baseEvent := tc.BotEvent{
		CreatedAt: time.Now().Add(-10 * time.Minute),
		Action:    tc.BotEventActionExecute,
		Coin:      coin,
		Type:      tc.BUY,
		Status:    tc.Filled,
		Price:     100,
		Size:      50,
		OrderType: tc.MarketOrderDealOrderTypeBase,
	}

	require.NoError(t, recordEvent(store, baseOid, baseEvent))

	exchange := newHyperliquidMockExchange(t, ts.URL())
	baseCloid := baseOid.Hex()
	baseOrder := hyperliquid.CreateOrderRequest{
		Coin:          coin,
		IsBuy:         true,
		Price:         100,
		Size:          50,
		ClientOrderID: &baseCloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err := exchange.Order(ctx, baseOrder, nil)
	require.NoError(t, err)
	ts.FillOrder(baseCloid, 100)

	baseStatus := makeStatusFromMockOrder(ts, baseOid, coin)
	require.NoError(t, recordStatus(store, baseOid, baseStatus))

	// Create take profit order
	tpOid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: 2}
	tpEvent := tc.BotEvent{
		CreatedAt: time.Now().Add(-8 * time.Minute),
		Action:    tc.BotEventActionPlace,
		Coin:      coin,
		Type:      tc.SELL,
		Status:    tc.Active,
		Price:     105,
		Size:      50,
		OrderType: tc.MarketOrderDealOrderTypeTakeProfit,
	}

	require.NoError(t, recordEvent(store, tpOid, tpEvent))

	tpCloid := tpOid.Hex()
	tpOrder := hyperliquid.CreateOrderRequest{
		Coin:          coin,
		IsBuy:         false,
		Price:         105,
		Size:          50,
		ClientOrderID: &tpCloid,
		ReduceOnly:    true,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	_, err = exchange.Order(ctx, tpOrder, nil)
	require.NoError(t, err)

	tpStatus := makeStatusFromMockOrder(ts, tpOid, coin)
	require.NoError(t, recordStatus(store, tpOid, tpStatus))

	require.NoError(t, tracker.Rebuild(ctx))

	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)
	require.NotNil(t, snapshot.ActiveTakeProfit)
	require.InDelta(t, 50, snapshot.ActiveTakeProfit.RemainingQty, 1e-6)

	// Cancel the take profit order
	_, err = exchange.CancelByCloid(ctx, coin, tpCloid)
	require.NoError(t, err)

	canceledStatus := makeStatusFromMockOrder(ts, tpOid, coin)
	require.NoError(t, recordStatus(store, tpOid, canceledStatus))
	require.NoError(t, tracker.UpdateStatus(ctx, tpOid, canceledStatus))

	snapshot, ok = tracker.Snapshot(dealID)
	require.True(t, ok)
	require.Nil(t, snapshot.ActiveTakeProfit, "take profit should be nil after cancellation")
}

// TestFillTrackerMultipleOrdersFromHyperliquid tests tracking multiple concurrent orders
func TestFillTrackerMultipleOrdersFromHyperliquid(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	store := newHyperliquidTestStore(t)
	tracker := New(store, nil)

	const (
		dealID = uint32(7000)
		botID  = uint32(8000)
		coin   = "ARB"
	)

	recordDeal(t, store, dealID, botID, coin)
	exchange := newHyperliquidMockExchange(t, ts.URL())

	// Create base order and 2 safety orders
	orders := []struct {
		eventID   uint32
		price     float64
		size      float64
		orderType tc.MarketOrderDealOrderType
	}{
		{1, 1.5, 100, tc.MarketOrderDealOrderTypeBase},
		{2, 1.4, 200, tc.MarketOrderDealOrderTypeSafetyOrder},
		{3, 1.3, 300, tc.MarketOrderDealOrderTypeSafetyOrder},
	}

	for _, o := range orders {
		oid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: o.eventID}
		event := tc.BotEvent{
			CreatedAt: time.Now().Add(-time.Duration(len(orders)-int(o.eventID)) * time.Minute),
			Action:    tc.BotEventActionPlace,
			Coin:      coin,
			Type:      tc.BUY,
			Status:    tc.Active,
			Price:     o.price,
			Size:      o.size,
			OrderType: o.orderType,
		}

		require.NoError(t, recordEvent(store, oid, event))

		cloid := oid.Hex()
		order := hyperliquid.CreateOrderRequest{
			Coin:          coin,
			IsBuy:         true,
			Price:         o.price,
			Size:          o.size,
			ClientOrderID: &cloid,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		}

		_, err := exchange.Order(ctx, order, nil)
		require.NoError(t, err)

		status := makeStatusFromMockOrder(ts, oid, coin)
		require.NoError(t, recordStatus(store, oid, status))
	}

	require.NoError(t, tracker.Rebuild(ctx))

	// Fill all orders
	for _, o := range orders {
		oid := orderid.OrderId{BotID: botID, DealID: dealID, BotEventID: o.eventID}
		cloid := oid.Hex()
		ts.FillOrder(cloid, o.price)

		filledStatus := makeStatusFromMockOrder(ts, oid, coin)
		require.NoError(t, recordStatus(store, oid, filledStatus))
		require.NoError(t, tracker.UpdateStatus(ctx, oid, filledStatus))
	}

	// Verify final position
	snapshot, ok := tracker.Snapshot(dealID)
	require.True(t, ok)

	expectedQty := 100.0 + 200.0 + 300.0
	expectedValue := (1.5 * 100) + (1.4 * 200) + (1.3 * 300)
	expectedAvg := expectedValue / expectedQty

	require.InDelta(t, expectedQty, snapshot.Position.TotalBuyQty, 1e-6)
	require.InDelta(t, expectedValue, snapshot.Position.TotalBuyValue, 1e-6)
	require.InDelta(t, expectedAvg, snapshot.Position.AverageEntry, 1e-6)
	require.True(t, snapshot.AllBuysFilled)
}

// Helper functions

func newHyperliquidTestStore(t *testing.T) *storage.Storage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hyperliquid_tracker.db")
	store, err := storage.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newHyperliquidMockExchange(t *testing.T, baseURL string) *hyperliquid.Exchange {
	t.Helper()

	ctx := context.Background()

	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	require.True(t, ok, "expected ECDSA public key")

	walletAddr := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(
		ctx,
		privateKey,
		baseURL,
		nil,
		"",
		walletAddr,
		nil,
	)

	return exchange
}

func makeStatusFromMockOrder(ts *mockserver.TestServer, oid orderid.OrderId, coin string) hyperliquid.WsOrder {
	cloid := oid.Hex()
	storedOrder, exists := ts.GetOrder(cloid)
	if !exists {
		panic("order not found in mock server")
	}

	// Parse order details from mock server response
	sz, _ := strconv.ParseFloat(storedOrder.Order.Sz, 64)
	limitPx, _ := strconv.ParseFloat(storedOrder.Order.LimitPx, 64)

	var status hyperliquid.OrderStatusValue
	switch storedOrder.Status {
	case "open":
		status = hyperliquid.OrderStatusValueOpen
	case "filled":
		status = hyperliquid.OrderStatusValueFilled
	case "canceled":
		status = hyperliquid.OrderStatusValueCanceled
	default:
		status = hyperliquid.OrderStatusValueOpen
	}

	// Calculate remaining size based on status
	remainingSz := sz
	if status == hyperliquid.OrderStatusValueFilled {
		remainingSz = 0
	}

	return hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      coin,
			Side:      storedOrder.Order.Side,
			LimitPx:   storedOrder.Order.LimitPx,
			Sz:        formatFloat(remainingSz),
			Oid:       storedOrder.Order.Oid,
			Timestamp: time.Now().UnixMilli(),
			OrigSz:    storedOrder.Order.Sz,
			Cloid:     &cloid,
		},
		Status:          status,
		StatusTimestamp: time.Now().UnixMilli(),
	}
}
