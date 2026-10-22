package cycle

import "net/http"

// HandleRoutes registers every route of the service.
func HandleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/orders", handleOrders)
	mux.HandleFunc("/healthz", handleHealth)
}

// handleOrders serves the order listing.
func handleOrders(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("orders"))
}

// handleHealth reports liveness.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

// Middleware wraps a handler with one extra layer.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
