package protocol

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsWebSocketRequestRequiresGET(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "http://gateway/socket", nil)
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		if got, want := IsWebSocketRequest(r), method == http.MethodGet; got != want {
			t.Fatalf("method %s: websocket = %v, want %v", method, got, want)
		}
	}
}

func TestWantsSSERequiresGET(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "http://gateway/events", nil)
		r.Header.Set("Accept", "text/event-stream")
		if got, want := WantsSSE(r), method == http.MethodGet; got != want {
			t.Fatalf("method %s: SSE = %v, want %v", method, got, want)
		}
	}
}
