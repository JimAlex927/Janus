package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"janus/internal/protocol"
	"janus/internal/telemetry"
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
			if r.Body != nil && r.Body != http.NoBody {
				// A derived request context does not necessarily interrupt the
				// underlying body reader. This matters for HTTP/3, where a QUIC
				// stream body can otherwise wait indefinitely for the next DATA
				// frame. Closing the body on cancellation releases that stream and
				// also bounds slow uploads for any body with the same contract.
				stopBody := context.AfterFunc(ctx, func() { _ = r.Body.Close() })
				defer stopBody()
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				telemetry.MarkError(r.Context(), "timeout")
			} else if errors.Is(ctx.Err(), context.Canceled) {
				telemetry.MarkError(r.Context(), "client_canceled")
			}
		})
	}
}
