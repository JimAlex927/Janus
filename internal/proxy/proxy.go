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
	// Keep HTTP/2 enabled after installing the custom DialContext below. The
	// standard Transport disables automatic HTTP/2 in that case unless this is
	// explicitly requested.
	t.ForceAttemptHTTP2 = true
	//这里两个用于transport创立连接的配置，如果连接已经存在于连接池中，就不会新建tcp或者udp连接。
	//参数1 Timeout: 新建连接的总超时时间
	//参数2 KeepAlive: 不是http的 Keep-Alive。
	//TCP KeepAlive 是操作系统在 TCP 连接上发送探测包，帮助检测死掉的连接。Go 当前文档也明确说明 Dialer.KeepAlive 控制 TCP keep-alive probe 的间隔/行为。
	t.DialContext = (&net.Dialer{Timeout: settings.ConnectTimeout.Duration(), KeepAlive: settings.KeepAlive.Duration()}).DialContext
	//Mandarin: 下面是Transport的配置. Transport是请求已经抵达gateway了。然后反向代理的时候，gateway需要请求service.
	//也就是一个网络传输层.
	//Go 官方把 Transport 定义为 RoundTripper 的实现，并明确说它是 HTTP/HTTPS 请求的一个低层客户端原语，同时负责连接缓存和复用；
	//它应该长期复用，而不是每个请求创建一个:
	//Clients and Transports are safe for concurrent use by multiple goroutines and for efficiency should only be created once and re-used.
	//config - 1
	//tcp连接建立后 如果需要tls 会进行tls的handshake。TLS handshake 最大等待时间。如果https的连接复用，不需要重新tcp connect、tls handshake
	t.TLSHandshakeTimeout = settings.TLSHandshakeTimeout.Duration()
	//config - 2
	//request 完全发送完以后，等待 backend 开始返回 HTTP response headers 最多多久。
	t.ResponseHeaderTimeout = settings.ResponseHeaderTimeout.Duration()
	//Backend 返回的 HTTP response headers 最大允许多大。
	t.MaxResponseHeaderBytes = settings.MaxResponseHeaderBytes
	//下面四个属于连接池参数，最好一起理解。
	//整个 Transport 最多保留多少条空闲连接。多个host，每个host的空闲连接数只和。
	t.MaxIdleConns = settings.MaxIdleConns
	//每一个 backend host 最多保留多少条 idle connection。
	t.MaxIdleConnsPerHost = settings.MaxIdleConnsPerHost
	//一个 host 所有连接总数。 正在建立的 + 正在使用的 + idle 的
	t.MaxConnsPerHost = settings.MaxConnsPerHost
	// 一个 HTTP keep-alive connection 放进连接池后，最多可以闲多久。
	t.IdleConnTimeout = settings.IdleConnTimeout.Duration()
	//Go 官方特别说明：这个选项控制 Transport 是否自动添加 Accept-Encoding: gzip；如果你自己显式设置了 Accept-Encoding，规则又有所不同
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
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				status = http.StatusRequestEntityTooLarge
			}
			var timeout net.Error
			if status == http.StatusBadGateway && (errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())) {
				status = http.StatusGatewayTimeout
			}
			logger.Warn("upstream request failed", zap.Int("status", status), zap.Error(err))
			http.Error(w, http.StatusText(status), status)
		},
	}
}
