// Package protocol contains context values shared by inbound protocol adapters
// and the HTTP router without coupling those packages together.
package protocol

import (
	"context"
	"net/http"
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
