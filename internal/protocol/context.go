// Package protocol contains context values shared by inbound protocol adapters
// and the HTTP router without coupling those packages together.
package protocol

import (
	"context"
	"net/http"
	"strings"
)

type limenIDKey struct{}

// WithLimenID returns a request carrying the trusted inbound Limen identity.
// The identity is assigned by the listener, never taken from a client header.
func WithLimenID(r *http.Request, id string) *http.Request {
	if id == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), limenIDKey{}, id))
}

// LimenID returns the trusted inbound Limen identity, if one was assigned.
func LimenID(r *http.Request) string {
	id, _ := r.Context().Value(limenIDKey{}).(string)
	return id
}

// IsWebSocketRequest recognizes the RFC 6455 HTTP/1.1 opening handshake. The
// HTTP/2 extended CONNECT form is intentionally not part of this milestone.
func IsWebSocketRequest(r *http.Request) bool {
	return r != nil && r.ProtoMajor == 1 && hasToken(r.Header.Values("Connection"), "upgrade") &&
		strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

// WantsSSE recognizes the standard EventSource request preference. SSE uses a
// normal HTTP response and therefore remains compatible with HTTP/1 and H2.
func WantsSSE(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, value := range r.Header.Values("Accept") {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]), "text/event-stream") {
				return true
			}
		}
	}
	return false
}

func hasToken(values []string, wanted string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), wanted) {
				return true
			}
		}
	}
	return false
}
