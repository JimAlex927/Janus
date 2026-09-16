// Package gateway composes validated configuration into a request handler.
package gateway

import (
	"net/http"
	"net/url"

	"janus/internal/config"
	"janus/internal/middleware"
	"janus/internal/proxy"
	"janus/internal/router"
	"janus/internal/upstream"

	"go.uber.org/zap"
)

type Gateway struct {
	handler   http.Handler
	transport *http.Transport
}

func New(c config.Config, logger *zap.Logger) (*Gateway, error) {
	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	//
	//NOTE: A shared transport is created for all reverse proxy requests
	//this transport itself is designed to be used in concurrency
	sharedTransportLayer := proxy.NewTransport(c.Settings.Backend)
	services := make(map[string]http.Handler, len(c.Services))
	for name, service := range c.Services {
		targets := make([]*url.URL, 0, len(service.Upstreams))
		//Notice that here is upstreams, consisting of multiple upstream.
		for _, raw := range service.Upstreams {
			u, err := config.ParseUpstream(raw)
			if err != nil {
				return nil, err
			}
			targets = append(targets, u)
		}
		//pool is slice contains all the upstream url corresponding to a service
		// pool 就是一个service的upstream的切片 然后有一个next方法 可以round robin的方式返回下一个url
		// 这样就可以负载均衡
		pool, err := upstream.New(targets)
		if err != nil {
			return nil, err
		}
		services[name] = proxy.New(pool, sharedTransportLayer, logger.With(zap.String("service", name)))
	}
	routes := make([]router.Route, 0, len(c.Routes))
	for _, r := range c.Routes {
		routeMiddlewares := make([]middleware.Middleware, 0, len(r.Middlewares))
		for _, name := range r.Middlewares {
			definition := c.Middlewares[name]
			if definition.Buffer != nil {
				routeMiddlewares = append(routeMiddlewares, middleware.Buffer(definition.Buffer.MaxResponseBodyBytes))
			}
		}
		routeHandler := middleware.Chain(services[r.Service], routeMiddlewares...)
		routes = append(routes, router.Route{Host: r.Host, PathPrefix: r.PathPrefix, Handler: routeHandler})
	}
	//http.Handler is an interface.
	// Router itself is a loop of match. It contains
	// Every Route has a handler . So if the request has matched a route, the handler corresponding to the route will handler the request.
	// And will only use the shared transport
	return &Gateway{
		handler: middleware.Chain(
			router.New(routes),
			middleware.Timeout(c.Settings.Request.MaximumDuration.Duration()),
		),
		transport: sharedTransportLayer,
	}, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Long-lived and tunnel protocols need their own limits and drain lifecycle.
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
		http.Error(w, "protocol upgrades are not supported", http.StatusNotImplemented)
		return
	}
	g.handler.ServeHTTP(w, r)
}

func (g *Gateway) Close() { g.transport.CloseIdleConnections() }

// NewServer is a bounded-duration API profile, not an SSE/WebSocket profile.
// The optional argument keeps callers using the starter defaults source-compatible.
func NewServer(address string, handler http.Handler, values ...config.Settings) *http.Server {
	settings := config.DefaultSettings()
	if len(values) > 0 {
		settings = values[0]
	}
	settings = settings.WithDefaults()
	return &http.Server{
		Addr: address, Handler: handler,
		//客户端最多可以花多久把 HTTP Request Header 发完整。
		ReadHeaderTimeout: settings.Server.ReadHeaderTimeout.Duration(),
		//ReadTimeout 是读取整个 request，包括 body 的最大持续时间。 Go 的实现是在开始读取这个 request 时算。不是开始读取header的时候算。
		ReadTimeout: settings.Request.ReadTimeout.Duration(),
		// WriteTimeout 是 response 写操作的底层 socket deadline；它通常在
		// request header 读取完成后开始计时，也会覆盖 Handler 的处理时间。
		// 它独立于 overall context deadline，配置校验要求它更长，以便超时后
		// 仍有余量写入 504；它不会强制停止任意业务代码。
		WriteTimeout: settings.Server.WriteTimeout.Duration(),
		//HTTP keep-alive 状态下，Server 最多等下一个 request 多久。用于http的 keep-alive的情况。
		//现代http请求一般默认都是keep-alive 这样请求可以复用旧的connection
		IdleTimeout: settings.Server.IdleTimeout.Duration(),
		//Server 允许客户端 HTTP Request Header 有多大
		MaxHeaderBytes: int(settings.Server.MaxHeaderBytes),
	}
}
