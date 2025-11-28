package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/internal/api"
	"github.com/stretchr/testify/require"
)

// TestE2E_VenueCRUDAndAssignments exercises the venue management handlers end-to-end.
func TestE2E_VenueCRUDAndAssignments(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	harness := NewE2ETestHarness(t, ctx)
	defer harness.Shutdown()

	const (
		botID   int64 = 55101
		venueID       = "hyperliquid:e2e"
	)

	require.NoError(t, harness.Store.RecordBot(ctx, tc.Bot{Id: int(botID)}, time.Now()))

	harness.Start(ctx)

	venuePath := fmt.Sprintf("/api/venues/%s", url.PathEscape(venueID))
	venueBody := api.UpsertVenueJSONRequestBody{
		Type:        "hyperliquid",
		Wallet:      "0xe2e",
		DisplayName: "E2E Venue",
	}

	resp := harness.APIRequest(http.MethodPut, venuePath, mustJSONReader(t, venueBody))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created api.UpsertVenue200JSONResponse
	decodeResponse(t, resp.Body, &created)
	require.Equal(t, venueID, created.VenueId)
	require.Equal(t, venueBody.Wallet, created.Wallet)
	require.Equal(t, venueBody.DisplayName, created.DisplayName)
	require.Equal(t, venueBody.Type, created.Type)

	resp = harness.APIGet("/api/venues")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var venues api.ListVenues200JSONResponse
	decodeResponse(t, resp.Body, &venues)
	found := false
	for _, v := range venues.Items {
		if v.VenueId == venueID {
			found = true
			require.Equal(t, venueBody.DisplayName, v.DisplayName)
			require.Equal(t, venueBody.Type, v.Type)
			break
		}
	}
	require.True(t, found, "expected venue to appear in listing")

	assignPath := fmt.Sprintf("/api/venues/%s/assignments/%d", url.PathEscape(venueID), botID)
	assignBody := api.UpsertVenueAssignmentJSONRequestBody{IsPrimary: true}

	resp = harness.APIRequest(http.MethodPut, assignPath, mustJSONReader(t, assignBody))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var assignment api.UpsertVenueAssignment200JSONResponse
	decodeResponse(t, resp.Body, &assignment)
	require.Equal(t, botID, assignment.BotId)
	require.True(t, assignment.IsPrimary)

	resp = harness.APIGet(fmt.Sprintf("/api/venues/%s/assignments", url.PathEscape(venueID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var assignmentList api.ListVenueAssignments200JSONResponse
	decodeResponse(t, resp.Body, &assignmentList)
	require.Len(t, assignmentList.Items, 1)
	require.Equal(t, botID, assignmentList.Items[0].BotId)
	require.True(t, assignmentList.Items[0].IsPrimary)

	resp = harness.APIGet(fmt.Sprintf("/api/bots/%d/venues", botID))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var botVenues api.ListBotVenues200JSONResponse
	decodeResponse(t, resp.Body, &botVenues)
	require.NotEmpty(t, botVenues.Items, "expected bot venues to include default assignment")
	foundPrimary := false
	for _, venue := range botVenues.Items {
		if venue.VenueId == venueID {
			foundPrimary = true
			require.True(t, venue.IsPrimary)
			break
		}
	}
	require.True(t, foundPrimary, "expected bot venues to include new assignment")

	resp = harness.APIRequest(http.MethodPut, fmt.Sprintf("/api/venues/%s/assignments/%d", url.PathEscape(venueID), 0), mustJSONReader(t, assignBody))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	resp = harness.APIRequest(http.MethodDelete, assignPath, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	resp = harness.APIGet(fmt.Sprintf("/api/venues/%s/assignments", url.PathEscape(venueID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	decodeResponse(t, resp.Body, &assignmentList)
	require.Empty(t, assignmentList.Items)

	resp = harness.APIRequest(http.MethodDelete, venuePath, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	resp = harness.APIRequest(http.MethodDelete, venuePath, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	resp = harness.APIGet(fmt.Sprintf("/api/venues/%s/assignments", url.PathEscape(venueID)))
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}
