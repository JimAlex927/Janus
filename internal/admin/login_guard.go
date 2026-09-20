package admin

import (
	"net/http"
	"time"

	"janus/internal/middleware"
)

// Login has a process/handler-local global budget as well as a bounded peer
// budget. Peer identity comes from RemoteAddr, never forwarded client headers.
// Behind a proxy the peer budget is deliberately shared by that proxy.
func protectLogin(next http.Handler) http.Handler {
	peer, _ := middleware.RateLimit(middleware.RateLimitOptions{Average: 5, Period: time.Minute, Burst: 10, MaxKeys: 1024})
	global, _ := middleware.RateLimit(middleware.RateLimitOptions{Average: 60, Period: time.Minute, Burst: 20, MaxKeys: 1, ClientIP: func(*http.Request) string { return "global" }})
	slots := make(chan struct{}, 4)
	return global(peer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "login busy", http.StatusTooManyRequests)
			return
		}
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
		defer controller.SetReadDeadline(time.Time{})
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		next.ServeHTTP(w, r)
	})))
}

func (h *Handler) secureSessionCookie(r *http.Request) bool {
	return r.TLS != nil || h.current != nil && h.current().Settings.Admin.CookieSecure
}
