package middleware

import (
	"net/http"
	"time"

	"janus/internal/protocol"
)

// ClearStreamingWriteDeadline removes the finite server write deadline for
// SSE and WebSocket requests. Their lifetime is controlled by client
// cancellation, StreamTimeout's write interruption, and Limen shutdown.
func ClearStreamingWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if protocol.IsWebSocketRequest(r) || protocol.WantsSSE(r) {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}
