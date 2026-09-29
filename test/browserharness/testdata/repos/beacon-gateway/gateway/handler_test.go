package gateway

import (
	"net/http/httptest"
	"testing"

	"example.com/beacon-gateway/queue"
)

func TestReusesClientRequestID(t *testing.T) {
	p := &queue.Producer{}
	req := httptest.NewRequest("POST", "/send", nil)
	req.Header.Set(RequestIDHeader, "abc")
	rec := httptest.NewRecorder()
	Handler(p)(rec, req)
	if got := rec.Header().Get(RequestIDHeader); got != "abc" {
		t.Fatalf("request id %q", got)
	}
}
