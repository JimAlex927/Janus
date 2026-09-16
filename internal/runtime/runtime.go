// Package runtime owns the stable HTTP dispatcher and the lifetime of the
// currently published route/service generation.
package runtime

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"janus/internal/config"
	"janus/internal/gateway"
	"janus/internal/middleware"
	"janus/internal/proxy"

	"go.uber.org/zap"
)

var (
	ErrClosed               = errors.New("runtime is closed")
	ErrRetiredLimit         = errors.New("runtime retired generation limit reached")
	ErrStartupConfigChanged = errors.New("runtime startup settings cannot change during replacement")
)

const maxRetiredGenerations = 8

// Generation is one immutable route/service handler graph. Runtime calls
// Handler for each acquired request and Close only after that generation has
// no active requests left.
type Generation interface {
	Handler() http.Handler
	Close()
}

// Builder constructs a generation using the process-owned outbound transport.
// The transport must not be closed by the returned generation.
type Builder func(config.Config, http.RoundTripper, *zap.Logger, map[string]*middleware.Limiter) (Generation, error)

// Runtime is a stable handler whose active generation can be replaced without
// changing the handler installed in the protocol Limen.
type Runtime struct {
	mu       sync.Mutex
	updateMu sync.Mutex

	active  *generationRef
	retired map[*generationRef]struct{}
	closed  bool

	version   int
	limens    map[string]config.LimenConfig
	settings  config.Settings
	builder   Builder
	logger    *zap.Logger
	transport *http.Transport
	global    *middleware.Limiter
	services  *serviceLimiterRegistry
	handler   http.Handler
}

type generationRef struct {
	generation Generation
	handler    http.Handler
	commit     func()
	refs       int
	retired    bool
	closed     bool
}

// New creates a runtime with the standard Gateway generation builder and one
// process-owned outbound transport.
func New(c config.Config, logger *zap.Logger) (*Runtime, error) {
	return NewWithBuilder(c, logger, DefaultBuilder)
}

// NewWithBuilder is used by tests and future configuration sources to supply
// a generation builder while preserving Runtime's publication and lifetime
// rules.
func NewWithBuilder(c config.Config, logger *zap.Logger, builder Builder) (*Runtime, error) {
	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if builder == nil {
		return nil, errors.New("runtime builder is nil")
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	transport := proxy.NewTransport(c.Settings.Backend)
	services := newServiceLimiterRegistry()
	initial, err := buildGeneration(builder, c, transport, logger, services)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	initial.commit()
	r := &Runtime{
		active:    initial,
		retired:   make(map[*generationRef]struct{}),
		version:   c.Version,
		limens:    cloneLimens(c.LimenBindings()),
		settings:  c.Settings,
		builder:   builder,
		logger:    logger,
		transport: transport,
		global:    middleware.NewLimiter(c.Settings.Request.MaxInFlight),
		services:  services,
	}
	// This chain is process-owned and is deliberately outside the replaceable
	// generation. Its order preserves the existing behavior: observation wraps
	// protocol guards, timeout, and active-generation dispatch.
	r.handler = middleware.Chain(
		http.HandlerFunc(r.dispatch),
		middleware.Observe(logger),
		middleware.RejectUnsupportedProtocols,
		middleware.Admission(r.global),
		middleware.Timeout(c.Settings.Request.MaximumDuration.Duration()),
		middleware.ClearStreamingWriteDeadline,
	)
	return r, nil
}

// DefaultBuilder adapts the Gateway builder to Runtime's generation contract.
func DefaultBuilder(c config.Config, transport http.RoundTripper, logger *zap.Logger, serviceLimiters map[string]*middleware.Limiter) (Generation, error) {
	return gateway.NewWithTransportAndLimiters(c, logger, transport, serviceLimiters)
}

// Handler returns the stable dispatcher to install in Protocol Limen.
func (r *Runtime) Handler() http.Handler { return r.handler }

// ServeHTTP delegates to the stable dispatcher.
func (r *Runtime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

// Replace validates and publishes a new in-memory route/service generation.
// Listener, server, request, backend-transport, and global timeout settings
// are startup-owned in 2B and cannot change through this method.
func (r *Runtime) Replace(c config.Config) error {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Version != r.version || !reflect.DeepEqual(c.LimenBindings(), r.limens) || !reflect.DeepEqual(c.Settings, r.settings) {
		return ErrStartupConfigChanged
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if len(r.retired) >= maxRetiredGenerations {
		r.mu.Unlock()
		return ErrRetiredLimit
	}
	r.mu.Unlock()

	candidate, err := buildGeneration(r.builder, c, r.transport, r.logger, r.services)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		candidate.generation.Close()
		return ErrClosed
	}
	candidate.commit()
	old := r.active
	r.active = candidate
	shouldCloseOld := r.retireLocked(old)
	r.mu.Unlock()
	if shouldCloseOld {
		old.generation.Close()
	}
	return nil
}

func cloneLimens(source map[string]config.LimenConfig) map[string]config.LimenConfig {
	clone := make(map[string]config.LimenConfig, len(source))
	for name, binding := range source {
		binding.Protocols = append([]string(nil), binding.Protocols...)
		if binding.TLS != nil {
			tls := *binding.TLS
			binding.TLS = &tls
		}
		clone[name] = binding
	}
	return clone
}

// Close retires the active generation and closes the process-owned transport.
// An in-flight generation is closed by release after its final request exits.
func (r *Runtime) Close() {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	old := r.active
	r.active = nil
	shouldCloseOld := r.retireLocked(old)
	r.mu.Unlock()
	if shouldCloseOld {
		old.generation.Close()
	}
	r.transport.CloseIdleConnections()
}

func (r *Runtime) dispatch(w http.ResponseWriter, req *http.Request) {
	ref, ok := r.acquire()
	if !ok {
		http.Error(w, "runtime is not serving", http.StatusServiceUnavailable)
		return
	}
	defer r.release(ref)
	ref.handler.ServeHTTP(w, req)
}

func (r *Runtime) acquire() (*generationRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.active == nil {
		return nil, false
	}
	r.active.refs++
	return r.active, true
}

func (r *Runtime) release(ref *generationRef) {
	r.mu.Lock()
	ref.refs--
	shouldClose := ref.retired && ref.refs == 0 && !ref.closed
	if shouldClose {
		ref.closed = true
		delete(r.retired, ref)
	}
	r.mu.Unlock()
	if shouldClose {
		ref.generation.Close()
	}
}

func (r *Runtime) retireLocked(ref *generationRef) bool {
	if ref == nil {
		return false
	}
	ref.retired = true
	if ref.refs != 0 {
		r.retired[ref] = struct{}{}
		return false
	}
	ref.closed = true
	return true
}

func buildGeneration(builder Builder, c config.Config, transport http.RoundTripper, logger *zap.Logger, services *serviceLimiterRegistry) (*generationRef, error) {
	serviceLimiters, commitServices, releaseServices := services.acquire(c)
	candidate, err := builder(c, transport, logger, serviceLimiters)
	if err != nil {
		if candidate != nil {
			candidate.Close()
		}
		releaseServices()
		return nil, err
	}
	if candidate == nil {
		releaseServices()
		return nil, fmt.Errorf("builder returned an invalid generation")
	}
	handler := candidate.Handler()
	if handler == nil {
		candidate.Close()
		releaseServices()
		return nil, fmt.Errorf("builder returned an invalid generation")
	}
	managed := &managedGeneration{Generation: candidate, release: releaseServices}
	return &generationRef{generation: managed, handler: handler, commit: commitServices}, nil
}

type managedGeneration struct {
	Generation
	release func()
	once    sync.Once
}

func (g *managedGeneration) Close() {
	g.once.Do(func() {
		defer g.release()
		g.Generation.Close()
	})
}

type serviceLimiterRegistry struct {
	mu      sync.Mutex
	entries map[string]*serviceLimiterEntry
}

type serviceLimiterEntry struct {
	limiter *middleware.Limiter
	refs    int
}

func newServiceLimiterRegistry() *serviceLimiterRegistry {
	return &serviceLimiterRegistry{entries: make(map[string]*serviceLimiterEntry)}
}

func (r *serviceLimiterRegistry) acquire(c config.Config) (map[string]*middleware.Limiter, func(), func()) {
	r.mu.Lock()
	limiters := make(map[string]*middleware.Limiter)
	names := make([]string, 0)
	limits := make(map[string]int)
	for name, service := range c.Services {
		limit, ok := serviceInFlightLimit(c, service)
		if !ok {
			continue
		}
		entry := r.entries[name]
		if entry == nil {
			entry = &serviceLimiterEntry{limiter: middleware.NewLimiter(limit)}
			r.entries[name] = entry
		}
		entry.refs++
		limiters[name] = entry.limiter
		limits[name] = limit
		names = append(names, name)
	}
	r.mu.Unlock()

	var once sync.Once
	var commitOnce sync.Once
	return limiters, func() {
			commitOnce.Do(func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				for name, limit := range limits {
					if entry := r.entries[name]; entry != nil {
						entry.limiter.SetLimit(limit)
					}
				}
			})
		}, func() {
			once.Do(func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				for _, name := range names {
					entry := r.entries[name]
					if entry == nil {
						continue
					}
					entry.refs--
					if entry.refs == 0 {
						delete(r.entries, name)
					}
				}
			})
		}
}

func serviceInFlightLimit(c config.Config, service config.Service) (int, bool) {
	for _, name := range service.Middlewares {
		if definition := c.Middlewares[name]; definition.InFlight != nil {
			return definition.InFlight.MaxConcurrent, true
		}
	}
	return 0, false
}
