package api

import (
	"context"
	"net/http"

	strictnethttp "github.com/oapi-codegen/runtime/strictmiddleware/nethttp"
)

type requestContextKey struct{}

var httpRequestContextKey = requestContextKey{}

// RequestContextMiddleware attaches *http.Request to the context for downstream handlers.
func RequestContextMiddleware() StrictMiddlewareFunc {
	return func(next strictnethttp.StrictHTTPHandlerFunc, operationID string) strictnethttp.StrictHTTPHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
			ctx = context.WithValue(ctx, httpRequestContextKey, r)
			return next(ctx, w, r, request)
		}
	}
}

func requestFromContext(ctx context.Context) (*http.Request, bool) {
	req, ok := ctx.Value(httpRequestContextKey).(*http.Request)
	return req, ok
}
