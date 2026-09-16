// Package proxy owns outbound HTTP behavior and connection reuse.
package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"

	"janus/internal/config"
	"janus/internal/upstream"

	"go.uber.org/zap"
)

// NewTransport is shared across services with identical trust/TLS policy.
// The optional argument keeps callers using the starter defaults source-compatible.
func NewTransport(values ...config.BackendSettings) *http.Transport {
	settings := config.DefaultSettings().Backend
	if len(values) > 0 {
		settings = values[0]
	}
	settings = settings.WithDefaults()
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil // Backend connections must not inherit a workstation's HTTP_PROXY.
	t.DialContext = (&net.Dialer{Timeout: settings.ConnectTimeout.Duration(), KeepAlive: settings.KeepAlive.Duration()}).DialContext
	t.TLSHandshakeTimeout = settings.TLSHandshakeTimeout.Duration()
	t.ResponseHeaderTimeout = settings.ResponseHeaderTimeout.Duration()
	t.MaxResponseHeaderBytes = settings.MaxResponseHeaderBytes
	t.MaxIdleConns = settings.MaxIdleConns
	t.MaxIdleConnsPerHost = settings.MaxIdleConnsPerHost
	t.MaxConnsPerHost = settings.MaxConnsPerHost
	t.IdleConnTimeout = settings.IdleConnTimeout.Duration()
	t.DisableCompression = settings.DisableCompression != nil && *settings.DisableCompression
	return t
}

func New(pool *upstream.Pool, transport http.RoundTripper, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			//pool再次出现 是之前每个service对应的下游切片 这里是每个service返回一个reverse proxy
			target := pool.Next()
			r.SetURL(&target)
			// Rewrite already removes the standard forwarding headers. Remove
			// alternate identity hints too; v0 trusts only its immediate peer.
			// Note: Delete the specified header for those will be regenerated.
			for name := range r.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
					r.Out.Header.Del(name)
				}
			}
			r.Out.Header.Del("X-Real-Ip")
			//Note: Set new Header
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
			logger.Warn("upstream request failed", zap.Int("status", status), zap.Error(err))
			http.Error(w, http.StatusText(status), status)
		},
	}
}
