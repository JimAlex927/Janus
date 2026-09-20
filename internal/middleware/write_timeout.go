package middleware

import (
	"net/http"
	"time"
)

// WriteTimeout applies the effective response-write budget for one matched
// route. It overrides the deadline initially installed by http.Server; the
// inner ClearStreamingWriteDeadline middleware removes it again for SSE and
// WebSocket requests, whose lifetime is controlled by StreamTimeout.
func WriteTimeout(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if timeout > 0 {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(timeout))
			}
			next.ServeHTTP(w, r)
		})
	}
}
