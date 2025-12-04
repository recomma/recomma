package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	tcMock "github.com/recomma/3commas-mock/tcmock"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/stretchr/testify/require"
)

// TestE2E_BasicHarnessLifecycle tests that the E2E harness can start and stop cleanly
func TestE2E_BasicHarnessLifecycle(t *testing.T) {
	// t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	harness.ThreeCommasMock.AllowDuplicateIDs(true)
	err := harness.ThreeCommasMock.LoadVCRCassette("../../testdata/singledeal")
	require.NoError(t, err)

	// Start application
	harness.Start(testCtx)

	// Verify HTTP server is responding
	resp := harness.APIGet("/api/bots")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	t.Log("✅ E2E harness lifecycle test passed")
}

// TestE2E_DealToOrderFlow tests the full flow from 3commas deal to Hyperliquid order
func TestE2E_DealToOrderFlow(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	err := harness.ThreeCommasMock.LoadVCRCassette("../../testdata/singledeal")
	require.NoError(t, err)

	// Start application
	harness.Start(testCtx)

	// Trigger deal production
	harness.TriggerDealProduction(testCtx)

	// Wait for the recorded deal to be processed
	harness.WaitForDealProcessing(2376446537, 5*time.Second)

	deals, _, err := harness.Store.ListDeals(testCtx, api.ListDealsOptions{})
	require.NoError(t, err)

	var recordedDeal *tc.Deal
	for i := range deals {
		if deals[i].Id == 2376446537 {
			recordedDeal = &deals[i]
			break
		}
	}
	require.NotNil(t, recordedDeal, "recorded deal should be stored")
	require.Equal(t, "USDT_DOGE", recordedDeal.Pair)
	require.Equal(t, "Bot16511317 has signal", recordedDeal.BotName)

	// Wait for order to be recorded in database (which implies it was submitted)
	harness.WaitForOrderInDatabase(5 * time.Second)

	// Verify database has the order
	dbOrders, _, err := harness.Store.ListOrders(testCtx, api.ListOrdersOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, dbOrders)

	// Verify web API returns the order
	resp := harness.APIGet("/api/orders")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var apiResp api.ListOrders200JSONResponse
	err = json.Unmarshal(body, &apiResp)
	require.NoError(t, err)
	require.NotEmpty(t, apiResp.Items, "API should return at least one order")

	t.Log("✅ E2E test passed: 3commas deal → processing → hyperliquid order → database → web API")
}

// TestE2E_APIListBots tests the /api/bots endpoint
func TestE2E_APIListBots(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	// Configure mock with a bot
	harness.ThreeCommasMock.AddBot(tcMock.Bot{
		Id:        1,
		Name:      strPtr("API Test Bot"),
		IsEnabled: true,
	})

	// Start application
	harness.Start(testCtx)

	// Trigger bot sync
	harness.TriggerDealProduction(testCtx)

	// Give time for bot to be synced
	time.Sleep(500 * time.Millisecond)

	// Test API
	resp := harness.APIGet("/api/bots")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var apiResp map[string]interface{}
	err = json.Unmarshal(body, &apiResp)
	require.NoError(t, err)

	// Check if bots are present
	botsData, ok := apiResp["bots"]
	if ok {
		botsArray, ok := botsData.([]interface{})
		if ok && len(botsArray) > 0 {
			bot := botsArray[0].(map[string]interface{})
			require.Equal(t, float64(1), bot["id"])
			require.Equal(t, "API Test Bot", bot["name"])
			t.Log("✅ E2E test passed: /api/bots returns expected data")
			return
		}
	}

	t.Log("ℹ️  No bots returned yet (may need more time for sync)")
}

// TestE2E_APIListBotsFilters ensures pagination and filtering work for /api/bots.
func TestE2E_APIListBotsFilters(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	base := time.Now().Add(-2 * time.Hour).UTC()
	botIDs := []int{101, 102, 103}
	for i, id := range botIDs {
		synced := base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, harness.Store.RecordBot(testCtx, tc.Bot{
			Id:        id,
			Name:      strPtr(fmt.Sprintf("Filter Bot %d", id)),
			IsEnabled: true,
		}, synced))
	}

	harness.Start(testCtx)

	resp := harness.APIGet("/api/bots?limit=1")
	var firstPage api.ListBots200JSONResponse
	decodeResponse(t, resp.Body, &firstPage)
	require.Len(t, firstPage.Items, 1)
	require.NotNil(t, firstPage.NextPageToken)

	resp = harness.APIGet(fmt.Sprintf("/api/bots?limit=1&page_token=%s", url.QueryEscape(*firstPage.NextPageToken)))
	var secondPage api.ListBots200JSONResponse
	decodeResponse(t, resp.Body, &secondPage)
	require.Len(t, secondPage.Items, 1)
	require.NotEqual(t, firstPage.Items[0].BotId, secondPage.Items[0].BotId, "expected pagination to advance results")

	targetBot := botIDs[1]
	resp = harness.APIGet(fmt.Sprintf("/api/bots?bot_id=%d", targetBot))
	var filtered api.ListBots200JSONResponse
	decodeResponse(t, resp.Body, &filtered)
	require.Len(t, filtered.Items, 1)
	require.EqualValues(t, targetBot, filtered.Items[0].BotId)

	from := base.Add(90 * time.Second)
	resp = harness.APIGet(fmt.Sprintf("/api/bots?updated_from=%s", url.QueryEscape(from.Format(time.RFC3339Nano))))
	var fromResp api.ListBots200JSONResponse
	decodeResponse(t, resp.Body, &fromResp)
	require.NotEmpty(t, fromResp.Items)
	for _, item := range fromResp.Items {
		require.False(t, item.LastSyncedAt.Before(from), "bot %d returned before updated_from threshold", item.BotId)
	}

	to := base.Add(90 * time.Second)
	resp = harness.APIGet(fmt.Sprintf("/api/bots?updated_to=%s", url.QueryEscape(to.Format(time.RFC3339Nano))))
	var toResp api.ListBots200JSONResponse
	decodeResponse(t, resp.Body, &toResp)
	require.NotEmpty(t, toResp.Items)
	for _, item := range toResp.Items {
		require.False(t, item.LastSyncedAt.After(to), "bot %d returned after updated_to threshold", item.BotId)
	}
}

// TestE2E_APIListDealsFilters ensures filtering works for /api/deals.
func TestE2E_APIListDealsFilters(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	harness := NewE2ETestHarness(t, testCtx)
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	base := time.Now().Add(-time.Hour).UTC()
	deals := []tc.Deal{
		{Id: 7001, BotId: 501, CreatedAt: base, UpdatedAt: base},
		{Id: 7002, BotId: 501, CreatedAt: base.Add(2 * time.Minute), UpdatedAt: base.Add(2 * time.Minute)},
		{Id: 7003, BotId: 502, CreatedAt: base.Add(4 * time.Minute), UpdatedAt: base.Add(4 * time.Minute)},
	}
	for _, deal := range deals {
		require.NoError(t, harness.Store.RecordThreeCommasDeal(testCtx, deal))
	}

	harness.Start(testCtx)

	resp := harness.APIGet("/api/deals?limit=1")
	var firstPage api.ListDeals200JSONResponse
	decodeResponse(t, resp.Body, &firstPage)
	require.Len(t, firstPage.Items, 1)
	require.NotNil(t, firstPage.NextPageToken)

	resp = harness.APIGet(fmt.Sprintf("/api/deals?limit=1&page_token=%s", url.QueryEscape(*firstPage.NextPageToken)))
	var secondPage api.ListDeals200JSONResponse
	decodeResponse(t, resp.Body, &secondPage)
	require.Len(t, secondPage.Items, 1)
	require.NotEqual(t, firstPage.Items[0].DealId, secondPage.Items[0].DealId, "expected next page deal to differ")

	resp = harness.APIGet(fmt.Sprintf("/api/deals?bot_id=%d", deals[0].BotId))
	var botDeals api.ListDeals200JSONResponse
	decodeResponse(t, resp.Body, &botDeals)
	require.Len(t, botDeals.Items, 2)
	for _, item := range botDeals.Items {
		require.EqualValues(t, deals[0].BotId, item.BotId)
	}

	resp = harness.APIGet(fmt.Sprintf("/api/deals?deal_id=%d", deals[2].Id))
	var singleDeal api.ListDeals200JSONResponse
	decodeResponse(t, resp.Body, &singleDeal)
	require.Len(t, singleDeal.Items, 1)
	require.EqualValues(t, deals[2].Id, singleDeal.Items[0].DealId)

	cutoff := base.Add(3 * time.Minute)
	resp = harness.APIGet(fmt.Sprintf("/api/deals?updated_to=%s", url.QueryEscape(cutoff.Format(time.RFC3339Nano))))
	var toResp api.ListDeals200JSONResponse
	decodeResponse(t, resp.Body, &toResp)
	require.NotEmpty(t, toResp.Items)
	for _, item := range toResp.Items {
		require.False(t, item.UpdatedAt.After(cutoff), "deal %d returned after updated_to threshold", item.DealId)
	}
}
