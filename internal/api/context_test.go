package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestContextMiddleware(t *testing.T) {
	t.Run("attaches request to context", func(t *testing.T) {
		middleware := RequestContextMiddleware()
		require.NotNil(t, middleware)

		// Create a mock handler that verifies the request is in context
		var capturedReq *http.Request
		handler := func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
			req, ok := requestFromContext(ctx)
			require.True(t, ok, "request should be in context")
			require.NotNil(t, req, "request should not be nil")
			capturedReq = req
			return nil, nil
		}

		// Wrap handler with middleware
		wrapped := middleware(handler, "test-operation")

		// Create test request
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()

		// Execute
		_, err := wrapped(context.Background(), w, req, nil)
		require.NoError(t, err)

		// Verify the request was captured
		require.Equal(t, req, capturedReq)
	})
}

func TestRequestFromContext(t *testing.T) {
	t.Run("returns request when present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
		ctx := context.WithValue(context.Background(), httpRequestContextKey, req)

		gotReq, ok := requestFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, req, gotReq)
	})

	t.Run("returns false when not present", func(t *testing.T) {
		ctx := context.Background()

		_, ok := requestFromContext(ctx)
		require.False(t, ok)
	})

	t.Run("returns false when wrong type", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), httpRequestContextKey, "not a request")

		_, ok := requestFromContext(ctx)
		require.False(t, ok)
	})
}
