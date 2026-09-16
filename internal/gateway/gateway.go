// Package gateway composes validated configuration into a request handler.
package gateway

import (
	"fmt"
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
	handler        http.Handler
	standalone     http.Handler
	transport      *http.Transport
	ownedTransport *http.Transport
}

func New(c config.Config, logger *zap.Logger) (*Gateway, error) {
	return NewWithTransport(c, logger, nil)
}

// NewWithTransport builds a route/service handler graph using a caller-owned
// outbound transport. A nil transport creates an owned transport for the
// standalone Gateway compatibility path; runtime generations inject the
// process-owned transport instead.
func NewWithTransport(c config.Config, logger *zap.Logger, transport http.RoundTripper) (*Gateway, error) {
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
	var ownedTransport *http.Transport
	if transport == nil {
		ownedTransport = proxy.NewTransport(c.Settings.Backend)
		transport = ownedTransport
	}
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
		serviceHandler := proxy.New(pool, transport, logger.With(zap.String("service", name)))
		serviceMiddlewares, err := buildMiddlewares(c, service.Middlewares)
		if err != nil {
			return nil, err
		}
		services[name] = middleware.Chain(serviceHandler, serviceMiddlewares...)
	}
	routes := make([]router.Route, 0, len(c.Routes))
	for _, r := range c.Routes {
		routeMiddlewares, err := buildMiddlewares(c, r.Middlewares)
		if err != nil {
			return nil, err
		}
		routeHandler := middleware.Chain(services[r.Service], routeMiddlewares...)
		routes = append(routes, router.Route{Limen: r.Limen, Protocols: r.Protocols, Host: r.Host, PathPrefix: r.PathPrefix, Handler: routeHandler})
	}
	//http.Handler is an interface.
	// Router itself is a loop of match. It contains
	// Every Route has a handler . So if the request has matched a route, the handler corresponding to the route will handler the request.
	// And will only use the shared transport
	routeHandler := router.New(routes)
	return &Gateway{
		handler: routeHandler,
		standalone: middleware.Chain(
			routeHandler,
			middleware.RejectUnsupportedProtocols,
			middleware.Timeout(c.Settings.Request.MaximumDuration.Duration()),
			middleware.ClearStreamingWriteDeadline,
		),
		transport:      ownedTransport,
		ownedTransport: ownedTransport,
	}, nil
}

func buildMiddlewares(c config.Config, names []string) ([]middleware.Middleware, error) {
	result := make([]middleware.Middleware, 0, len(names))
	for _, name := range names {
		definition := c.Middlewares[name]
		switch {
		case definition.Buffer != nil:
			result = append(result, middleware.Buffer(definition.Buffer.MaxResponseBodyBytes))
		case definition.BodyLimit != nil:
			result = append(result, middleware.BodyLimit(definition.BodyLimit.MaxBytes))
		default:
			return nil, fmt.Errorf("middleware %q has no supported policy", name)
		}
	}
	return result, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.standalone.ServeHTTP(w, r)
}

// Handler returns the route/service graph for embedding in a stable runtime
// dispatcher. It intentionally excludes process-global middleware.
func (g *Gateway) Handler() http.Handler { return g.handler }

func (g *Gateway) Close() {
	if g.ownedTransport != nil {
		g.ownedTransport.CloseIdleConnections()
	}
}
