package middleware

import (
	"context"
	"net/http"
	"time"

	"janus/internal/protocol"
)

// Timeout applies an overall handler budget. It cancels outbound work through
// the request context, but does not try to forcibly stop arbitrary handler code
// or buffer a response. The downstream handler remains responsible for mapping
// context errors to an HTTP response while the response is still writable.
func Timeout(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if timeout <= 0 || protocol.IsWebSocketRequest(r) || protocol.WantsSSE(r) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
