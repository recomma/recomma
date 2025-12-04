package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	threecommasmock "github.com/recomma/3commas-mock/server"
	tcMock "github.com/recomma/3commas-mock/tcmock"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/stretchr/testify/require"
)

// TestE2E_OrderScaling_WithMultiplier tests the full E2E flow:
// 3commas mock (deal with small size) -> app (scales 5x) -> hyperliquid mock
func TestE2E_OrderScaling_WithMultiplier(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(1, "Order Scaling Bot", 12345, true))

	deal := threecommasmock.NewDeal(201, 1, "USDT_BTC", "active")
	addBaseOrderEvent(&deal, "BTC", 50000.0, 0.001)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal))

	require.NoError(t, harness.Store.RecordBot(testCtx, tc.Bot{Id: 1}, time.Now()))

	// Configure order scaler multiplier before startup so initial sync uses it
	multiplier := 5.0
	_, err := harness.Store.UpsertBotOrderScaler(testCtx, 1, &multiplier, nil, "e2e-test")
	require.NoError(t, err)

	// Start application
	harness.Start(testCtx)

	// Trigger deal production (3commas -> app)
	harness.TriggerDealProduction(testCtx)

	// Wait for order in database (which implies it was submitted and scaled)
	harness.WaitForOrderInDatabase(5 * time.Second)

	// Verify scaling via Hyperliquid submission
	size := lastHyperliquidOrderSize(t, harness, 201)
	require.InDelta(t, 0.005, size, 0.0001, "expected Hyperliquid order size to be 0.005")

	// Verify database has the order
	dbOrders, _, err := harness.Store.ListOrders(testCtx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders, "expected at least one order in database")

	order := dbOrders[0]
	require.NotNil(t, order.BotEvent)
	require.Equal(t, "BTC", order.BotEvent.Coin)
	require.Equal(t, tc.BUY, order.BotEvent.Type)
	// Bot events reflect the original 3Commas size.
	require.InDelta(t, 0.001, order.BotEvent.Size, 0.0001, "expected bot event to retain original size")

	t.Log("✅ E2E test passed: 3commas -> app (5x scaling) -> hyperliquid")
}

// TestE2E_OrderScaling_MaxMultiplierClamp tests the full E2E flow with max multiplier clamping:
// 3commas mock (deal) -> app (clamps 20x to 10x max) -> hyperliquid mock
func TestE2E_OrderScaling_MaxMultiplierClamp(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(2, "Max Multiplier Bot", 67890, true))

	deal := threecommasmock.NewDeal(202, 2, "USDT_ETH", "active")
	addBaseOrderEvent(&deal, "ETH", 3000.0, 0.01)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal))

	require.NoError(t, harness.Store.RecordBot(testCtx, tc.Bot{Id: 2}, time.Now()))

	// Set multiplier to 20.0 (should be clamped to 10.0) before startup
	multiplier := 20.0
	_, err := harness.Store.UpsertBotOrderScaler(testCtx, 2, &multiplier, nil, "e2e-test")
	require.NoError(t, err)

	// Start application (max multiplier is 10.0 from config)
	harness.Start(testCtx)

	// Trigger E2E flow
	harness.TriggerDealProduction(testCtx)
	harness.WaitForOrderInDatabase(5 * time.Second)

	// Verify clamped scaling via Hyperliquid submission
	size := lastHyperliquidOrderSize(t, harness, 202)
	require.InDelta(t, 0.1, size, 0.01, "expected Hyperliquid order size to be clamped to 0.1")

	// Verify database has the order
	dbOrders, _, err := harness.Store.ListOrders(testCtx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders, "expected at least one order in database")

	order := dbOrders[0]
	require.NotNil(t, order.BotEvent)
	// Bot events reflect the original size; scaling happens at submission.
	require.InDelta(t, 0.01, order.BotEvent.Size, 0.001, "expected bot event to retain original size")

	t.Log("✅ E2E test passed: 3commas -> app (max clamp) -> hyperliquid")
}

// TestE2E_MultiDeal_ConcurrentProcessing tests multiple deals flowing through the system:
// 3commas mock (3 bots, 3 deals) -> app -> hyperliquid mock (3 orders)
func TestE2E_MultiDeal_ConcurrentProcessing(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	// Configure 3commas mock with multiple bots and deals
	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(10, "Bot A", 111, true))
	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(20, "Bot B", 222, true))

	deal301 := threecommasmock.NewDeal(301, 10, "USDT_BTC", "active")
	addBaseOrderEvent(&deal301, "BTC", 50000.0, 0.001)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal301))

	deal302 := threecommasmock.NewDeal(302, 10, "USDT_ETH", "active")
	addBaseOrderEvent(&deal302, "ETH", 3000.0, 0.01)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal302))

	deal401 := threecommasmock.NewDeal(401, 20, "USDT_SOL", "active")
	addBaseOrderEvent(&deal401, "SOL", 100.0, 1.0)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal401))

	// Start application
	harness.Start(testCtx)

	// Trigger E2E flow (should process all 3 deals)
	harness.TriggerDealProduction(testCtx)

	// Wait for all deals to be processed
	harness.WaitForDealProcessing(301, 5*time.Second)
	harness.WaitForDealProcessing(302, 5*time.Second)
	harness.WaitForDealProcessing(401, 5*time.Second)

	var dbOrders []api.OrderItem
	require.Eventually(t, func() bool {
		var err error
		dbOrders, _, err = harness.Store.ListOrders(testCtx, api.ListOrdersOptions{})
		if err != nil {
			return false
		}
		return len(dbOrders) == 3
	}, 5*time.Second, 100*time.Millisecond, "expected 3 orders in database")

	// Verify E2E: all 3 orders were submitted
	require.Len(t, dbOrders, 3, "expected 3 orders in database")

	// Verify coins match
	coins := make(map[string]int)
	for _, order := range dbOrders {
		require.NotNil(t, order.BotEvent)
		coins[order.BotEvent.Coin]++
	}
	require.Equal(t, 1, coins["BTC"], "expected 1 BTC order")
	require.Equal(t, 1, coins["ETH"], "expected 1 ETH order")
	require.Equal(t, 1, coins["SOL"], "expected 1 SOL order")

	t.Log("✅ E2E test passed: 3commas (3 deals) -> app -> hyperliquid (3 orders)")
}

// TestE2E_MultiVenueReplay ensures that assigning a new venue after an order exists
// triggers a replay on the missing venue, resulting in submissions on both venues.
func TestE2E_MultiVenueReplay(t *testing.T) {
	t.Parallel()

	const (
		botID            = 910
		dealID           = 91001
		secondaryVenueID = "hyperliquid:secondary"
	)

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	harness := NewE2ETestHarness(t, testCtx,
		WithAdditionalHyperliquidVenue(secondaryVenueID, "Secondary Hyperliquid"),
		WithStorageLogger(),
	)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(botID, "Replay Bot", 4242, true))

	deal := threecommasmock.NewDeal(dealID, botID, "USDT_BTC", "active")
	addBaseOrderEvent(&deal, "BTC", 50000.0, 0.001)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal))

	// Ensure the bot initially targets only the primary venue.
	require.NoError(t, harness.Store.UpsertBotVenueAssignment(testCtx, uint32(botID), recomma.VenueID(harness.VenueID), true))

	harness.Start(testCtx)
	harness.TriggerDealProduction(testCtx)
	harness.WaitForDealProcessing(uint32(dealID), 5*time.Second)
	harness.WaitForOrderInDatabase(5 * time.Second)

	dbOrders, _, err := harness.Store.ListOrders(testCtx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders)
	oid := dbOrders[0].OrderId

	var initialIdents []recomma.OrderIdentifier
	require.Eventually(t, func() bool {
		var err error
		initialIdents, err = harness.Store.ListSubmissionIdentifiersForOrder(testCtx, oid)
		if err != nil {
			return false
		}
		for _, ident := range initialIdents {
			if ident.Venue() == secondaryVenueID {
				return false
			}
		}
		return len(initialIdents) > 0
	}, 5*time.Second, 100*time.Millisecond, "expected submission before multi-venue replay")

	// Assign the secondary venue after the initial submission exists.
	require.NoError(t, harness.Store.UpsertBotVenueAssignment(testCtx, uint32(botID), recomma.VenueID(secondaryVenueID), false))

	// Append a new event to trigger a modify, which should replay to the new venue.
	modifiedMsg := baseOrderEventMessage("BTC", 50500.0, 0.0012)
	require.NoError(t, harness.ThreeCommasMock.AddBotEventToDeal(dealID, modifiedMsg))

	harness.TriggerDealProduction(testCtx)

	require.Eventually(t, func() bool {
		idents, err := harness.Store.ListSubmissionIdentifiersForOrder(testCtx, oid)
		if err != nil {
			return false
		}
		if len(idents) < 2 {
			return false
		}
		for _, ident := range idents {
			if ident.Venue() == secondaryVenueID {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "expected replay to submit on secondary venue")

	idents, err := harness.Store.ListSubmissionIdentifiersForOrder(testCtx, oid)
	require.NoError(t, err)
	require.NotEmpty(t, idents, "expected submissions recorded for both venues")

	seenVenues := make(map[string]struct{})
	for _, ident := range idents {
		seenVenues[ident.Venue()] = struct{}{}
	}
	require.Contains(t, seenVenues, secondaryVenueID, "secondary venue submission missing")
	require.Contains(t, seenVenues, harness.VenueID, "primary venue submission missing")

	t.Log("✅ E2E test passed: missing venue assignment triggers replay submission")
}

// TestE2E_TakeProfitStack tests the full E2E flow with stacked take-profit orders:
// 3commas mock (1 base + 3 TPs) -> app -> hyperliquid mock (base order + TPs)
func TestE2E_TakeProfitStack(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	harness.ThreeCommasMock.AddBot(threecommasmock.NewBot(60, "TP Stack Bot", 333, true))

	deal := threecommasmock.NewDeal(801, 60, "USDT_BTC", "active")
	addBaseOrderEvent(&deal, "BTC", 50000.0, 0.003)
	addTakeProfitEvent(&deal, "BTC", 51000.0, 51.0, 0.001)
	addTakeProfitEvent(&deal, "BTC", 52000.0, 52.0, 0.001)
	addTakeProfitEvent(&deal, "BTC", 53000.0, 53.0, 0.001)
	require.NoError(t, harness.ThreeCommasMock.AddDeal(deal))

	// Start application
	harness.Start(testCtx)

	// Trigger E2E flow
	harness.TriggerDealProduction(testCtx)

	// Wait for orders to be in database
	time.Sleep(3 * time.Second)

	// Verify E2E: base order submitted (TPs managed separately by fill tracker)
	dbOrders, _, err := harness.Store.ListOrders(testCtx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(dbOrders), 1, "expected at least the base order in database")

	// Count buy vs sell orders
	var buyOrders, sellOrders int
	for _, order := range dbOrders {
		require.NotNil(t, order.BotEvent)
		if order.BotEvent.Type == tc.BUY {
			buyOrders++
		} else {
			sellOrders++
		}
	}
	require.Equal(t, 1, buyOrders, "expected 1 buy order (base)")

	t.Log("✅ E2E test passed: 3commas (TP stack) -> app -> hyperliquid")
}

func addBaseOrderEvent(deal *tcMock.Deal, coin string, price, size float64) {
	threecommasmock.AddBotEvent(deal, baseOrderEventMessage(coin, price, size))
}

func baseOrderEventMessage(coin string, price, size float64) string {
	quoteVolume := price * size
	return fmt.Sprintf(
		"Placing base order. Price: %.5f USDT Size: %.5f USDT (%.5f %s)",
		price, quoteVolume, size, coin,
	)
}

func addTakeProfitEvent(deal *tcMock.Deal, coin string, price, quoteVolume, baseSize float64) {
	threecommasmock.AddBotEvent(deal, fmt.Sprintf(
		"Placing TakeProfit trade.  Price: %.5f USDT Size: %.5f USDT (%.5f %s), the price should rise for 2.00%% to close the trade",
		price, quoteVolume, baseSize, coin,
	))
}

func fetchScaledOrders(t *testing.T, store *storage.Storage, ctx context.Context, dealID uint32) []storage.ScaledOrderAudit {
	t.Helper()

	audits, err := store.ListScaledOrdersByDeal(ctx, dealID)
	require.NoError(t, err)
	require.NotEmpty(t, audits, "expected scaled order audit entries")
	return audits
}

func lastHyperliquidOrderSize(t *testing.T, harness *E2ETestHarness, dealID uint32) float64 {
	t.Helper()

	reqs := harness.HyperliquidMock.GetExchangeRequests()
	require.NotEmpty(t, reqs, "expected Hyperliquid requests")

	for i := len(reqs) - 1; i >= 0; i-- {
		orders := extractOrdersFromAction(reqs[i].Action)
		for _, order := range orders {
			cloid := firstString(order, "cloid", "c")
			if cloid == "" {
				continue
			}
			oid, err := orderid.FromHexString(cloid)
			if err != nil {
				continue
			}
			if oid.DealID != dealID {
				continue
			}
			if sz, ok := extractOrderSize(order); ok {
				return sz
			}
		}
	}

	t.Fatalf("no Hyperliquid order found for deal %d", dealID)
	return 0
}

func extractOrdersFromAction(action interface{}) []map[string]interface{} {
	var orders []map[string]interface{}

	actionMap, ok := action.(map[string]interface{})
	if !ok {
		return orders
	}

	if rawOrders, ok := actionMap["orders"]; ok {
		switch list := rawOrders.(type) {
		case []interface{}:
			for _, entry := range list {
				if om, ok := entry.(map[string]interface{}); ok {
					orders = append(orders, om)
				}
			}
			return orders
		}
	}

	orders = append(orders, actionMap)
	return orders
}

func firstString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := values[key]; ok {
			switch typed := v.(type) {
			case string:
				return typed
			case json.Number:
				return typed.String()
			}
		}
	}
	return ""
}

func extractOrderSize(order map[string]interface{}) (float64, bool) {
	if sz, ok := order["sz"]; ok {
		if parsed, ok := parseNumeric(sz); ok {
			return parsed, true
		}
	}
	if sz, ok := order["s"]; ok {
		if parsed, ok := parseNumeric(sz); ok {
			return parsed, true
		}
	}
	if szStr := firstString(order, "size"); szStr != "" {
		if parsed, err := strconv.ParseFloat(szStr, 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func parseNumeric(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		if parsed, err := v.Float64(); err == nil {
			return parsed, true
		}
	case string:
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}
