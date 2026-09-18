// Package gateway composes validated configuration into a request handler.
package gateway

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"janus/internal/config"
	"janus/internal/discovery"
	"janus/internal/discovery/nacos"
	"janus/internal/forwarding"
	"janus/internal/health"
	"janus/internal/middleware"
	"janus/internal/proxy"
	"janus/internal/router"
	"janus/internal/telemetry"
	"janus/internal/upstream"

	"go.uber.org/zap"
)

type Gateway struct {
	handler         http.Handler
	standalone      http.Handler
	transport       *http.Transport
	ownedTransport  *http.Transport
	checkers        []*health.Checker
	healthByService map[string]*health.Checker
	discovered      map[string]*discovery.Lease
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
	return NewWithDiscovery(c, logger, transport, serviceLimiters, discovery.NewManager(nacos.New))
}

// NewWithDiscovery uses Runtime-owned discovery resources. A generation owns
// leases, not clients: rollback and drain release only that generation's leases.
func NewWithDiscovery(c config.Config, logger *zap.Logger, transport http.RoundTripper, serviceLimiters map[string]*middleware.Limiter, resolver *discovery.Manager) (*Gateway, error) {
	// 把配置填充默认值 同时里面还有个对server的health check填充默认值的操作
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
	healthByService := make(map[string]*health.Checker)
	discovered := make(map[string]*discovery.Lease)
	committed := false
	defer func() {
		if committed {
			return
		}
		for _, lease := range discovered {
			lease.Close()
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
		var pool proxy.TargetSelector
		if service.Nacos != nil {
			if resolver == nil {
				return nil, fmt.Errorf("service %q requires a discovery manager", name)
			}
			lease, err := resolver.Acquire(c.Discovery.Nacos[service.Nacos.Registry], *service.Nacos)
			if err != nil {
				return nil, fmt.Errorf("service %q: %w", name, err)
			}
			discovered[name] = lease
			pool = lease.Pool
		} else {
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
				healthByService[name] = healthChecker
			}
			if healthChecker != nil {
				pool, err = upstream.NewWithHealth(targets, healthChecker.Store())
			} else {
				pool, err = upstream.New(targets)
			}
			if err != nil {
				return nil, err
			}
		}
		//这里启用了proxy机制
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
		actionHandler, serviceName, err := buildRouteAction(r, services)
		if err != nil {
			return nil, err
		}
		routeHandler := middleware.Chain(actionHandler, routeMiddlewares...)
		routeHandler = middleware.RouteMetadata(r.Name, serviceName)(routeHandler)
		routes = append(routes, router.Route{Name: r.Name, Limen: r.Limen, Match: r.Match, Priority: r.Priority, Host: r.Host, PathPrefix: r.PathPrefix, Handler: routeHandler})
	}
	//http.Handler is an interface.
	// Router itself is a loop of match. It contains
	// Every Route has a handler . So if the request has matched a route, the handler corresponding to the route will handler the request.
	// And will only use the shared transport
	routeHandler, err := router.New(routes)
	if err != nil {
		return nil, err
	}
	committed = true
	return &Gateway{
		discovered: discovered,
		handler:    routeHandler,
		standalone: middleware.Chain(
			routeHandler,
			middleware.Observe(logger),
			middleware.RejectUnsupportedProtocols,
			middleware.Timeout(c.Settings.Request.MaximumDuration.Duration()),
			middleware.StreamTimeout(c.Settings.Stream.MaxDuration.Duration(), c.Settings.Stream.IdleTimeout.Duration()),
			middleware.ClearStreamingWriteDeadline,
		),
		transport:       ownedTransport,
		ownedTransport:  ownedTransport,
		checkers:        checkers,
		healthByService: healthByService,
	}, nil
}

func buildRouteAction(route config.Route, services map[string]http.Handler) (http.Handler, string, error) {
	if route.Action == nil || route.Action.Forward != nil {
		serviceName := route.Service
		if route.Action != nil && route.Action.Forward != nil {
			serviceName = route.Action.Forward.Service
		}
		handler := services[serviceName]
		if handler == nil {
			return nil, "", fmt.Errorf("route %q references missing service %q", route.Name, serviceName)
		}
		return handler, serviceName, nil
	}
	if route.Action.Redirect != nil {
		status := route.Action.Redirect.Status
		if status == 0 {
			status = http.StatusTemporaryRedirect
		}
		location := route.Action.Redirect.Location
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, location, status)
		}), "direct", nil
	}
	if route.Action.Respond != nil {
		status := route.Action.Respond.Status
		if status == 0 {
			status = http.StatusOK
		}
		headers := make(http.Header, len(route.Action.Respond.Headers))
		for name, value := range route.Action.Respond.Headers {
			headers.Set(name, value)
		}
		body := []byte(route.Action.Respond.Body)
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for name, values := range headers {
				for _, value := range values {
					w.Header().Add(name, value)
				}
			}
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}), "direct", nil
	}
	return nil, "", fmt.Errorf("route %q has no supported action", route.Name)
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
		case definition.Headers != nil:
			rules := definition.Headers
			result = append(result, middleware.Headers(rules.RequestSet, rules.RequestRemove, rules.ResponseSet, rules.ResponseRemove))
		case definition.StripPrefix != nil:
			result = append(result, middleware.StripPrefix(definition.StripPrefix.Prefix))
		case definition.AddPrefix != nil:
			result = append(result, middleware.AddPrefix(definition.AddPrefix.Prefix))
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

// HealthSnapshot returns only bounded service and target-index state for the
// active generation. It intentionally does not expose upstream URLs.
func (g *Gateway) HealthSnapshot() []telemetry.BackendHealth {
	if g == nil {
		return nil
	}
	result := make([]telemetry.BackendHealth, 0)
	for service, checker := range g.healthByService {
		for _, target := range checker.Store().Snapshot() {
			result = append(result, telemetry.BackendHealth{
				Service: service,
				Target:  strconv.Itoa(target.Index),
				Healthy: target.Healthy,
			})
		}
	}
	return result
}

func (g *Gateway) Close() {
	for _, lease := range g.discovered {
		lease.Close()
	}
	for _, checker := range g.checkers {
		checker.Close()
	}
	if g.ownedTransport != nil {
		g.ownedTransport.CloseIdleConnections()
	}
}

// DiscoverySnapshot is a read-only view for the control plane. It never asks
// Nacos for data and must not be treated as a mutable configuration source.
func (g *Gateway) DiscoverySnapshot() map[string]discovery.Status {
	result := make(map[string]discovery.Status, len(g.discovered))
	for name, lease := range g.discovered {
		result[name] = lease.Status()
	}
	return result
}
