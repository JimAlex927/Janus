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
type Builder func(config.Config, http.RoundTripper, *zap.Logger) (Generation, error)

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
	handler   http.Handler
}

type generationRef struct {
	generation Generation
	handler    http.Handler
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
	initial, err := buildGeneration(builder, c, transport, logger)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	r := &Runtime{
		active:    initial,
		retired:   make(map[*generationRef]struct{}),
		version:   c.Version,
		limens:    cloneLimens(c.LimenBindings()),
		settings:  c.Settings,
		builder:   builder,
		logger:    logger,
		transport: transport,
	}
	// This chain is process-owned and is deliberately outside the replaceable
	// generation. Its order preserves the existing behavior: protocol guards
	// run before the overall timeout, then the active generation is acquired.
	r.handler = middleware.Chain(
		http.HandlerFunc(r.dispatch),
		middleware.RejectUnsupportedProtocols,
		middleware.Timeout(c.Settings.Request.MaximumDuration.Duration()),
	)
	return r, nil
}

// DefaultBuilder adapts the Gateway builder to Runtime's generation contract.
func DefaultBuilder(c config.Config, transport http.RoundTripper, logger *zap.Logger) (Generation, error) {
	return gateway.NewWithTransport(c, logger, transport)
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

	candidate, err := buildGeneration(r.builder, c, r.transport, r.logger)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		candidate.generation.Close()
		return ErrClosed
	}
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

func buildGeneration(builder Builder, c config.Config, transport http.RoundTripper, logger *zap.Logger) (*generationRef, error) {
	candidate, err := builder(c, transport, logger)
	if err != nil {
		if candidate != nil {
			candidate.Close()
		}
		return nil, err
	}
	if candidate == nil {
		if candidate != nil {
			candidate.Close()
		}
		return nil, fmt.Errorf("builder returned an invalid generation")
	}
	handler := candidate.Handler()
	if handler == nil {
		candidate.Close()
		return nil, fmt.Errorf("builder returned an invalid generation")
	}
	return &generationRef{generation: candidate, handler: handler}, nil
}
