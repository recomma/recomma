package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	hlmock "github.com/recomma/hyperliquid-mock/server"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/stretchr/testify/require"
)

// TestE2E_ManualCancelFlow verifies the API → emitter → storage path for manual cancels.
func TestE2E_ManualCancelFlow(t *testing.T) {
	t.Parallel()

	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	harness := NewE2ETestHarness(t, testCtx, WithStorageLogger())
	defer func() {
		cancel()
		harness.Shutdown()
	}()

	require.NoError(t, harness.ThreeCommasMock.LoadVCRCassette("../../testdata/singledeal"))

	harness.Start(testCtx)
	harness.TriggerDealProduction(testCtx)
	harness.WaitForDealProcessing(2376446537, 5*time.Second)
	harness.WaitForOrderInDatabase(5 * time.Second)
	harness.WaitForOrderQueueIdle(5 * time.Second)

	order := fetchFirstOrderRecord(t, harness)
	require.NotEmpty(t, order.OrderId, "order must expose an OrderId")

	oid, err := orderid.FromHexString(order.OrderId)
	require.NoError(t, err)

	// Track the existing submission identifier so storage checks can be scoped properly.
	var ident recomma.OrderIdentifier
	require.Eventually(t, func() bool {
		idents, err := harness.Store.ListSubmissionIdentifiersForOrder(testCtx, *oid)
		if err != nil || len(idents) == 0 {
			return false
		}
		ident = idents[0]
		return true
	}, 5*time.Second, 100*time.Millisecond, "expected at least one submission identifier")

	initialCancelCount := countHyperliquidCancelActions(harness.HyperliquidMock.GetExchangeRequests())
	orderPath := fmt.Sprintf("/api/orders/%s/cancel", order.OrderId)

	// First issue a dry-run request to exercise validation branches.
	dryRunPayload := api.CancelOrderByOrderIdRequest{
		DryRun: boolPtr(true),
		Reason: strPtr("preflight validation"),
	}
	dryRunResp := harness.APIPost(orderPath, mustJSONReader(t, dryRunPayload))
	require.Equal(t, http.StatusAccepted, dryRunResp.StatusCode)

	var dryRunBody api.CancelOrderByOrderIdResponse
	decodeResponse(t, dryRunResp.Body, &dryRunBody)
	require.Equal(t, "validated", dryRunBody.Status)
	require.NotNil(t, dryRunBody.Cancel, "dry-run response should include cancel payload")
	require.Equal(t, order.OrderId, dryRunBody.Cancel.Cloid)
	require.NotNil(t, dryRunBody.Message)
	require.Contains(t, strings.ToLower(*dryRunBody.Message), "dry-run")

	// Dry-run must not reach Hyperliquid.
	require.Equal(t, initialCancelCount, countHyperliquidCancelActions(harness.HyperliquidMock.GetExchangeRequests()))

	// Dispatch the real cancel with an operator reason.
	reason := "operator stop requested"
	cancelResp := harness.APIPost(orderPath, mustJSONReader(t, api.CancelOrderByOrderIdRequest{
		Reason: &reason,
	}))
	require.Equal(t, http.StatusAccepted, cancelResp.StatusCode)

	var queuedBody api.CancelOrderByOrderIdResponse
	decodeResponse(t, cancelResp.Body, &queuedBody)
	require.Equal(t, "queued", queuedBody.Status)
	require.NotNil(t, queuedBody.Cancel, "queued response should include cancel payload")
	require.Equal(t, order.OrderId, queuedBody.Cancel.Cloid)
	require.Equal(t, strings.ToUpper(order.ThreeCommas.Event.Coin), queuedBody.Cancel.Coin)
	require.NotNil(t, queuedBody.Message)
	require.Equal(t, reason, *queuedBody.Message)

	// Hyperliquid mock should eventually record a cancelByCloid action.
	require.Eventually(t, func() bool {
		return countHyperliquidCancelActions(harness.HyperliquidMock.GetExchangeRequests()) > initialCancelCount
	}, 5*time.Second, 100*time.Millisecond, "expected cancel request sent to Hyperliquid")

	// Storage should persist the cancel as the latest submission for the identifier.
	require.Eventually(t, func() bool {
		action, found, err := harness.Store.LoadHyperliquidSubmission(testCtx, ident)
		if err != nil || !found {
			return false
		}
		return action.Type == recomma.ActionCancel &&
			strings.EqualFold(action.Cancel.Cloid, order.OrderId) &&
			strings.EqualFold(action.Cancel.Coin, queuedBody.Cancel.Coin)
	}, 5*time.Second, 100*time.Millisecond, "expected cancel recorded in storage")

	t.Log("✅ E2E test passed: API cancel request -> emitter -> Hyperliquid -> storage")
}

func fetchFirstOrderRecord(t *testing.T, harness *E2ETestHarness) api.OrderRecord {
	t.Helper()

	resp := harness.APIGet("/api/orders")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var list api.ListOrders200JSONResponse
	require.NoError(t, json.Unmarshal(body, &list))
	require.NotEmpty(t, list.Items, "expected at least one order from API")

	for _, item := range list.Items {
		if item.Hyperliquid != nil && item.Hyperliquid.LatestSubmission != nil {
			return item
		}
	}

	t.Fatalf("no orders have a Hyperliquid.LatestSubmission in API response")
	return api.OrderRecord{}
}

func countHyperliquidCancelActions(reqs []hlmock.ExchangeRequest) int {
	count := 0
	for _, req := range reqs {
		if isCancelAction(req.Action) {
			count++
		}
	}
	return count
}

func isCancelAction(action interface{}) bool {
	actionMap, ok := action.(map[string]interface{})
	if !ok {
		return false
	}
	if rawType, ok := actionMap["type"].(string); ok {
		switch strings.ToLower(rawType) {
		case "cancel", "cancelbycloid":
			return true
		}
	}
	_, hasCancels := actionMap["cancels"]
	return hasCancels
}

func mustJSONReader(t *testing.T, payload interface{}) io.Reader {
	t.Helper()

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return bytes.NewReader(data)
}

func decodeResponse(t *testing.T, body io.ReadCloser, target interface{}) {
	t.Helper()
	defer body.Close()

	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, target))
}

func boolPtr(v bool) *bool {
	return &v
}
