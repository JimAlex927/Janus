// Package proxy owns outbound HTTP behavior and connection reuse.
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"janus/internal/upstream"
)

// NewTransport is shared across services with identical trust/TLS policy.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil // Backend connections must not inherit a workstation's HTTP_PROXY.
	t.DialContext = (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 5 * time.Second
	t.ResponseHeaderTimeout = 10 * time.Second
	t.MaxResponseHeaderBytes = 64 << 10
	t.MaxIdleConns = 256
	t.MaxIdleConnsPerHost = 32
	t.MaxConnsPerHost = 128
	t.IdleConnTimeout = 90 * time.Second
	t.DisableCompression = true // Leave content negotiation to the client/backend.
	return t
}

func New(pool *upstream.Pool, transport http.RoundTripper, logger *slog.Logger) http.Handler {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			target := pool.Next()
			r.SetURL(&target)
			// Rewrite already removes the standard forwarding headers. Remove
			// alternate identity hints too; v0 trusts only its immediate peer.
			for name := range r.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
					r.Out.Header.Del(name)
				}
			}
			r.Out.Header.Del("X-Real-Ip")
			r.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			status := http.StatusBadGateway
			var timeout net.Error
			if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
				status = http.StatusGatewayTimeout
			}
			logger.Warn("upstream request failed", "status", status, "error", err)
			http.Error(w, http.StatusText(status), status)
		},
	}
}
