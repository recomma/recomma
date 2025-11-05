package server

import (
	"log"
	"net/http"
)

// Run starts the HTTP server
func Run(addr string) error {
	handler := NewHandler()

	mux := http.NewServeMux()
	mux.HandleFunc("/exchange", handler.HandleExchange)
	mux.HandleFunc("/info", handler.HandleInfo)
	mux.HandleFunc("/health", handler.HandleHealth)

	// Log all requests
	loggedMux := loggingMiddleware(mux)

	log.Printf("Mock Hyperliquid API server listening on %s", addr)
	log.Printf("Endpoints:")
	log.Printf("  POST   %s/exchange", addr)
	log.Printf("  POST   %s/info", addr)
	log.Printf("  GET    %s/health", addr)

	return http.ListenAndServe(addr, loggedMux)
}

// loggingMiddleware logs all incoming requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
