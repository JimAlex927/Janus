// Package gateway composes validated configuration into a request handler.
package gateway

import (
	"fmt"
	"net/http"
	"net/url"

	"janus/internal/config"
	"janus/internal/forwarding"
	"janus/internal/health"
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
	checkers       []*health.Checker
}

func New(c config.Config, logger *zap.Logger) (*Gateway, error) {
	return NewWithTransport(c, logger, nil)
}

// NewWithTransport builds a route/service handler graph using a caller-owned
// outbound transport. A nil transport creates an owned transport for the
// standalone Gateway compatibility path; runtime generations inject the
// process-owned transport instead.
func NewWithTransport(c config.Config, logger *zap.Logger, transport http.RoundTripper) (*Gateway, error) {
	return NewWithTransportAndLimiters(c, logger, transport, nil)
}

// NewWithTransportAndLimiters builds a generation with runtime-owned service
// limiters. A nil map keeps the standalone Gateway path source-compatible by
// creating generation-local service limiters.
func NewWithTransportAndLimiters(c config.Config, logger *zap.Logger, transport http.RoundTripper, serviceLimiters map[string]*middleware.Limiter) (*Gateway, error) {
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
	checkers := make([]*health.Checker, 0)
	committed := false
	defer func() {
		if committed {
			return
		}
		for _, checker := range checkers {
			checker.Close()
		}
		if ownedTransport != nil {
			ownedTransport.CloseIdleConnections()
		}
	}()
	services := make(map[string]http.Handler, len(c.Services))
	forwardingPolicies := forwarding.NewPolicies()
	for name, binding := range c.LimenBindings() {
		policy, err := forwarding.NewPolicy(binding.TrustedProxies)
		if err != nil {
			return nil, fmt.Errorf("limen %q trusted proxy policy: %w", name, err)
		}
		forwardingPolicies.Set(name, policy)
	}
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
		var healthChecker *health.Checker
		var pool *upstream.Pool
		var err error
		if service.HealthCheck != nil {
			check := service.HealthCheck.WithDefaults()
			healthTargets := make([]url.URL, len(targets))
			for i, target := range targets {
				healthTargets[i] = *target
			}
			healthChecker, err = health.New(healthTargets, health.Settings{
				Path:               check.Path,
				Interval:           check.Interval.Duration(),
				Timeout:            check.Timeout.Duration(),
				Jitter:             check.Jitter.Duration(),
				UnhealthyThreshold: check.UnhealthyThreshold,
				HealthyThreshold:   check.HealthyThreshold,
				ExpectedStatus:     check.ExpectedStatus,
			}, transport, logger.With(zap.String("service", name)))
			if err != nil {
				return nil, fmt.Errorf("service %q health check: %w", name, err)
			}
			checkers = append(checkers, healthChecker)
		}
		if healthChecker != nil {
			pool, err = upstream.NewWithHealth(targets, healthChecker.Store())
		} else {
			pool, err = upstream.New(targets)
		}
		if err != nil {
			return nil, err
		}
		serviceHandler := proxy.NewWithForwarding(pool, transport, logger.With(zap.String("service", name)), forwardingPolicies)
		serviceLimiter := serviceLimiters[name]
		if serviceLimiter == nil {
			if limit, ok := configuredServiceLimit(c, service); ok {
				serviceLimiter = middleware.NewLimiter(limit)
			}
		}
		serviceMiddlewares, err := buildMiddlewares(c, service.Middlewares, serviceLimiter)
		if err != nil {
			return nil, err
		}
		services[name] = middleware.Chain(serviceHandler, serviceMiddlewares...)
	}
	routes := make([]router.Route, 0, len(c.Routes))
	for _, r := range c.Routes {
		routeMiddlewares, err := buildMiddlewares(c, r.Middlewares, nil)
		if err != nil {
			return nil, err
		}
		routeHandler := middleware.Chain(services[r.Service], routeMiddlewares...)
		routeHandler = middleware.RouteMetadata(r.Name, r.Service)(routeHandler)
		routes = append(routes, router.Route{Name: r.Name, Limen: r.Limen, Protocols: r.Protocols, Host: r.Host, PathPrefix: r.PathPrefix, Handler: routeHandler})
	}
	//http.Handler is an interface.
	// Router itself is a loop of match. It contains
	// Every Route has a handler . So if the request has matched a route, the handler corresponding to the route will handler the request.
	// And will only use the shared transport
	routeHandler := router.New(routes)
	committed = true
	return &Gateway{
		handler: routeHandler,
		standalone: middleware.Chain(
			routeHandler,
			middleware.Observe(logger),
			middleware.RejectUnsupportedProtocols,
			middleware.Timeout(c.Settings.Request.MaximumDuration.Duration()),
			middleware.ClearStreamingWriteDeadline,
		),
		transport:      ownedTransport,
		ownedTransport: ownedTransport,
		checkers:       checkers,
	}, nil
}

func configuredServiceLimit(c config.Config, service config.Service) (int, bool) {
	for _, name := range service.Middlewares {
		if definition := c.Middlewares[name]; definition.InFlight != nil {
			return definition.InFlight.MaxConcurrent, true
		}
	}
	return 0, false
}

func buildMiddlewares(c config.Config, names []string, serviceLimiter *middleware.Limiter) ([]middleware.Middleware, error) {
	result := make([]middleware.Middleware, 0, len(names))
	for _, name := range names {
		definition := c.Middlewares[name]
		switch {
		case definition.Buffer != nil:
			result = append(result, middleware.Buffer(definition.Buffer.MaxResponseBodyBytes))
		case definition.BodyLimit != nil:
			result = append(result, middleware.BodyLimit(definition.BodyLimit.MaxBytes))
		case definition.InFlight != nil:
			if serviceLimiter == nil {
				return nil, fmt.Errorf("middleware %q requires a service limiter", name)
			}
			result = append(result, middleware.Admission(serviceLimiter))
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
	for _, checker := range g.checkers {
		checker.Close()
	}
	if g.ownedTransport != nil {
		g.ownedTransport.CloseIdleConnections()
	}
}
