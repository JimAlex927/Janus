// Package gateway composes validated configuration into a request handler.
package gateway

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"janus/internal/config"
	"janus/internal/proxy"
	"janus/internal/router"
	"janus/internal/upstream"

	"go.uber.org/zap"
)

type Gateway struct {
	handler        http.Handler
	transport      *http.Transport
	overallTimeout time.Duration
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
		routes = append(routes, router.Route{Host: r.Host, PathPrefix: r.PathPrefix, Handler: services[r.Service]})
	}
	//http.Handler is an interface.
	// Router itself is a loop of match. It contains
	// Every Route has a handler . So if the request has matched a route, the handler corresponding to the route will handler the request.
	// And will only use the shared transport
	return &Gateway{
		handler:        router.New(routes),
		transport:      sharedTransportLayer,
		overallTimeout: c.Settings.Request.MaximumDuration.Duration(),
	}, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Long-lived and tunnel protocols need their own limits and drain lifecycle.
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
		http.Error(w, "protocol upgrades are not supported", http.StatusNotImplemented)
		return
	}
	if g.overallTimeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), g.overallTimeout)
		defer cancel()
		r = r.WithContext(ctx)
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
		ReadHeaderTimeout: settings.Server.ReadHeaderTimeout.Duration(),
		ReadTimeout:       settings.Request.ReadTimeout.Duration(),
		WriteTimeout:      settings.Request.MaximumDuration.Duration(),
		IdleTimeout:       settings.Server.IdleTimeout.Duration(),
		MaxHeaderBytes:    int(settings.Server.MaxHeaderBytes),
	}
}
