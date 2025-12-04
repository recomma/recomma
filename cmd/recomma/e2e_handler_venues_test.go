package main

import (
	"context"
	"fmt"
	"io"
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

// TestE2E_VenueHandlers_Errors exercises validation and error branches for venue APIs.
func TestE2E_VenueHandlers_Errors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	harness := NewE2ETestHarness(t, ctx)
	defer harness.Shutdown()

	const botID int64 = 55202
	require.NoError(t, harness.Store.RecordBot(ctx, tc.Bot{Id: int(botID)}, time.Now()))

	harness.Start(ctx)

	assignBody := api.UpsertVenueAssignmentJSONRequestBody{IsPrimary: true}
	existingVenue := url.PathEscape(harness.VenueID)
	defaultVenue := url.PathEscape("hyperliquid:default")
	unassignedVenue := url.PathEscape("hyperliquid:errors")

	_, err := harness.Store.UpsertVenue(ctx, "hyperliquid:errors", api.VenueUpsertRequest{
		Type:        "hyperliquid",
		DisplayName: "Error Coverage",
		Wallet:      "0xdead",
	})
	require.NoError(t, err)

	testCases := []struct {
		name       string
		method     string
		path       string
		body       interface{}
		wantStatus int
	}{
		{
			name:       "upsert venue missing body and id",
			method:     http.MethodPut,
			path:       fmt.Sprintf("/api/venues/%s", url.PathEscape(" ")),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "upsert venue invalid payload",
			method:     http.MethodPut,
			path:       fmt.Sprintf("/api/venues/%s", url.PathEscape("hyperliquid:invalid")),
			body:       api.UpsertVenueJSONRequestBody{Type: "hyperliquid"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "delete default venue forbidden",
			method:     http.MethodDelete,
			path:       fmt.Sprintf("/api/venues/%s", defaultVenue),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "delete missing venue",
			method:     http.MethodDelete,
			path:       fmt.Sprintf("/api/venues/%s", url.PathEscape("ghost")),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "list assignments for missing venue",
			method:     http.MethodGet,
			path:       fmt.Sprintf("/api/venues/%s/assignments", url.PathEscape("ghost")),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "list assignments with blank id",
			method:     http.MethodGet,
			path:       fmt.Sprintf("/api/venues/%s/assignments", url.PathEscape(" ")),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "upsert assignment invalid bot",
			method:     http.MethodPut,
			path:       fmt.Sprintf("/api/venues/%s/assignments/%d", existingVenue, 0),
			body:       assignBody,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "upsert assignment missing venue",
			method:     http.MethodPut,
			path:       fmt.Sprintf("/api/venues/%s/assignments/%d", url.PathEscape("ghost"), botID),
			body:       assignBody,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "delete assignment missing record",
			method:     http.MethodDelete,
			path:       fmt.Sprintf("/api/venues/%s/assignments/%d", unassignedVenue, botID),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "delete assignment invalid bot",
			method:     http.MethodDelete,
			path:       fmt.Sprintf("/api/venues/%s/assignments/%d", existingVenue, 0),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "list bot venues invalid bot id",
			method:     http.MethodGet,
			path:       "/api/bots/0/venues",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != nil {
				body = mustJSONReader(t, tc.body)
			}
			resp := harness.APIRequest(tc.method, tc.path, body)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

// TestE2E_VenueHandlers_StoreFailures forces storage errors to exercise 500 paths.
func TestE2E_VenueHandlers_StoreFailures(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	harness := NewE2ETestHarness(t, ctx)
	defer harness.Shutdown()

	const botID int64 = 66303
	require.NoError(t, harness.Store.RecordBot(ctx, tc.Bot{Id: int(botID)}, time.Now()))

	harness.Start(ctx)

	// Close the underlying storage so subsequent API calls surface 500 errors.
	require.NoError(t, harness.Store.Close())

	failVenue := url.PathEscape("hyperliquid:fail")
	assignBody := api.UpsertVenueAssignmentJSONRequestBody{IsPrimary: true}
	venueBody := api.UpsertVenueJSONRequestBody{
		Type:        "hyperliquid",
		DisplayName: "Failover Venue",
		Wallet:      "0xfail",
	}

	cases := []struct {
		name       string
		method     string
		path       string
		body       interface{}
		wantStatus int
	}{
		{
			name:       "list venues fails",
			method:     http.MethodGet,
			path:       "/api/venues",
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "upsert venue fails",
			method:     http.MethodPut,
			path:       fmt.Sprintf("/api/venues/%s", failVenue),
			body:       venueBody,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "delete venue fails",
			method:     http.MethodDelete,
			path:       fmt.Sprintf("/api/venues/%s", failVenue),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "list venue assignments fails",
			method:     http.MethodGet,
			path:       fmt.Sprintf("/api/venues/%s/assignments", failVenue),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "upsert venue assignment fails",
			method:     http.MethodPut,
			path:       fmt.Sprintf("/api/venues/%s/assignments/%d", failVenue, botID),
			body:       assignBody,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "delete venue assignment fails",
			method:     http.MethodDelete,
			path:       fmt.Sprintf("/api/venues/%s/assignments/%d", failVenue, botID),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "list bot venues fails",
			method:     http.MethodGet,
			path:       fmt.Sprintf("/api/bots/%d/venues", botID),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != nil {
				body = mustJSONReader(t, tc.body)
			}
			resp := harness.APIRequest(tc.method, tc.path, body)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			resp.Body.Close()
		})
	}
}
