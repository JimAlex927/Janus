package middleware

import (
	"net/http"
	"time"

	"janus/internal/protocol"
)

// ClearStreamingWriteDeadline removes the finite server write deadline for
// SSE and WebSocket requests. Their lifetime is controlled by client
// cancellation and Limen shutdown, not by the bounded API timeout.
func ClearStreamingWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if protocol.IsWebSocketRequest(r) || protocol.WantsSSE(r) {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}
