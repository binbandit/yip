// Package gateway accepts Beacon requests.
package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"example.com/beacon-gateway/queue"
)

// RequestIDHeader lets clients supply an idempotency key.
const RequestIDHeader = "X-Request-Id"

// Handler assigns a request ID (reusing the client's when present) and
// enqueues the request.
func Handler(p *queue.Producer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newID()
		}
		if err := p.Publish(queue.Message{RequestID: id, Path: r.URL.Path}); err != nil {
			http.Error(w, "queue unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set(RequestIDHeader, id)
		w.WriteHeader(http.StatusAccepted)
	}
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
