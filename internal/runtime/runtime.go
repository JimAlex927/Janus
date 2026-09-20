// Package runtime owns the stable HTTP dispatcher and the lifetime of the
// currently published route/service generation.
package runtime

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"janus/internal/config"
	"janus/internal/discovery"
	"janus/internal/discovery/nacos"
	"janus/internal/gateway"
	"janus/internal/middleware"
	"janus/internal/proxy"
	"janus/internal/telemetry"

	"go.uber.org/zap"
)

var (
	ErrClosed               = errors.New("runtime is closed")
	ErrRetiredLimit         = errors.New("runtime retired generation limit reached")
	ErrStartupConfigChanged = errors.New("runtime startup settings cannot change during replacement")
	ErrRevisionConflict     = errors.New("runtime configuration revision conflict")
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

	version                int
	limens                 map[string]config.LimenConfig
	settings               config.Settings
	generationBuilder      Builder
	logger                 *zap.Logger
	transport              *http.Transport
	global                 *middleware.Limiter
	serviceLimiterRegistry *serviceLimiterRegistry
	discoveryManager       *discovery.Manager
	metrics                *telemetry.Metrics
	handler                http.Handler
	config                 config.Config
	revision               atomic.Uint64
	eventsMu               sync.Mutex
	events                 map[chan Event]struct{}
}

// Event describes a published runtime change. Subscribers receive a bounded,
// best-effort stream for the private admin console; request handling never
// waits for an administrator to read an event.
type Event struct {
	Type     string    `json:"type"`
	Revision uint64    `json:"revision"`
	At       time.Time `json:"at"`
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
	return NewWithDiscovery(c, logger, discovery.NewManager(nacos.New))
}

// NewWithDiscovery keeps client/subscription reuse outside immutable handler
// generations and provides an injectable boundary for deterministic tests.
func NewWithDiscovery(c config.Config, logger *zap.Logger, manager *discovery.Manager) (*Runtime, error) {
	r, err := NewWithBuilder(c, logger, func(c config.Config, transport http.RoundTripper, logger *zap.Logger, limits map[string]*middleware.Limiter) (Generation, error) {
		generation, err := gateway.NewWithDiscovery(c, logger, transport, limits, manager)
		if err != nil {
			return nil, err
		}
		return generation, nil
	})
	if err != nil {
		return nil, err
	}
	r.discoveryManager = manager
	return r, nil
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
	// One process-owned transport is reused by every published generation.
	transport := proxy.NewTransport(c.Settings.Backend)
	// Metrics are process-owned so observations remain continuous across
	// generation replacement and can be scraped from the admin listener.
	metrics := telemetry.NewMetrics()
	// Service limiters live outside generations so an in-flight count survives a
	// route reload. The service middleware receives the shared limiter and
	// performs the per-request acquire/release operation.
	serviceLimiterRegistry := newServiceLimiterRegistry(metrics)
	// Build the first immutable route/service generation.
	initial, err := buildGeneration(builder, c, transport, logger, serviceLimiterRegistry)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	// Publication is transactional: only a successfully built generation may
	// commit its service limiter limits.
	initial.commit()
	r := &Runtime{
		// Runtime is created once; reload replaces generations inside it.
		active:            initial,
		retired:           make(map[*generationRef]struct{}),
		version:           c.Version,
		limens:            cloneLimens(c.LimenBindings()),
		settings:          c.Settings,
		generationBuilder: builder,
		logger:            logger,
		transport:         transport,
		// The global limiter protects the whole gateway, including all services.
		global:                 middleware.NewLimiterWithMetrics(c.Settings.Request.MaxInFlight, metrics, "global", ""),
		serviceLimiterRegistry: serviceLimiterRegistry,
		metrics:                metrics,
		config:                 c,
	}
	r.revision.Store(1)
	r.events = make(map[chan Event]struct{})
	// This chain is process-owned and remains stable while generations reload.
	// Its order preserves process-wide behavior: observation wraps protocol
	// guards, global admission, and active-generation dispatch. Request and
	// stream deadlines run inside the matched route so route overrides can be
	// resolved before those middleware execute.
	r.handler = middleware.Chain(
		/**
		这里就到了分流的地方了
		*/
		http.HandlerFunc(r.dispatch),
		/**
		和metric有关 也就是统计请求的一些信息
		*/
		middleware.ObserveWithMetrics(logger, metrics),
		/*
			拒绝不支持的协议
		*/
		middleware.RejectUnsupportedProtocols,
		/*
		 全局限速器
		*/
		middleware.Admission(r.global),
	)
	return r, nil
}

// DefaultBuilder adapts the Gateway builder to Runtime's generation contract.
func DefaultBuilder(c config.Config, transport http.RoundTripper, logger *zap.Logger, serviceLimiters map[string]*middleware.Limiter) (Generation, error) {
	generation, err := gateway.NewWithTransportAndLimiters(c, logger, transport, serviceLimiters)
	// Never convert a nil *Gateway into a non-nil Generation interface: the
	// rollback path would try to close that typed nil value.
	if err != nil {
		return nil, err
	}
	return generation, nil
}

// Handler returns the stable dispatcher to install in Protocol Limen.
func (r *Runtime) Handler() http.Handler { return r.handler }

// Metrics returns the process-owned metrics registry used by the stable
// observer, admission gates and lifecycle components.
func (r *Runtime) Metrics() *telemetry.Metrics { return r.metrics }

// ConfigSnapshot returns the last successfully published configuration. The
// returned value is treated as immutable by callers.
func (r *Runtime) ConfigSnapshot() config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config
}

func (r *Runtime) Revision() uint64 { return r.revision.Load() }

// Subscribe returns a bounded event channel. The caller must call the
// returned cancel function; slow subscribers only lose events, never block
// request handling.
func (r *Runtime) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	r.mu.Lock()
	closed := r.closed
	if !closed {
		r.eventsMu.Lock()
		r.events[ch] = struct{}{}
		r.eventsMu.Unlock()
	}
	r.mu.Unlock()
	if closed {
		close(ch)
		return ch, func() {}
	}
	return ch, func() {
		r.eventsMu.Lock()
		if _, ok := r.events[ch]; ok {
			delete(r.events, ch)
			close(ch)
		}
		r.eventsMu.Unlock()
	}
}

func (r *Runtime) publish(event Event) {
	r.eventsMu.Lock()
	defer r.eventsMu.Unlock()
	for ch := range r.events {
		select {
		case ch <- event:
		default:
		}
	}
}

// HealthSnapshot protects the active generation while copying its bounded
// service/target health state for the admin metrics endpoint.
func (r *Runtime) HealthSnapshot() []telemetry.BackendHealth {
	ref, ok := r.acquire()
	if !ok {
		return nil
	}
	defer r.release(ref)
	snapshotter, ok := ref.generation.(interface {
		HealthSnapshot() []telemetry.BackendHealth
	})
	if !ok {
		return nil
	}
	return snapshotter.HealthSnapshot()
}

func (r *Runtime) DiscoverySnapshot() map[string]discovery.Status {
	ref, ok := r.acquire()
	if !ok {
		return nil
	}
	defer r.release(ref)
	view, ok := ref.generation.(interface {
		DiscoverySnapshot() map[string]discovery.Status
	})
	if !ok {
		return nil
	}
	return view.DiscoverySnapshot()
}

// RegistryHealth performs an explicit admin-only connectivity probe for one
// published registry. It is intentionally separate from service discovery
// status because a registry may be healthy even when it has no subscriptions.
func (r *Runtime) RegistryHealth(name string) discovery.RegistryHealth {
	return r.RegistryHealthConfig(name, nil)
}

// RegistryHealthConfig probes either the published registry or an optional
// admin-console draft. Draft probing lets operators validate a newly created
// Registry before publishing it. A redacted draft inherits the active secret
// for the same name; plaintext passwords are never logged or returned.
func (r *Runtime) RegistryHealthConfig(name string, draft *config.NacosRegistry) discovery.RegistryHealth {
	result := discovery.RegistryHealth{Registry: name, CheckedAt: time.Now()}
	started := time.Now()
	if r == nil || r.discoveryManager == nil {
		result.Error = "discovery manager is not configured"
		return result
	}
	current := r.ConfigSnapshot()
	registry, ok := current.Discovery.Nacos[name]
	if draft != nil {
		registry = *draft
		if ok && registry.Password == "" && registry.PasswordEnv == "" {
			registry.Password = current.Discovery.Nacos[name].Password
			registry.PasswordEnv = current.Discovery.Nacos[name].PasswordEnv
		}
		ok = true
	}
	if !ok {
		result.Error = "nacos registry is not configured"
		r.logRegistryHealth(name, registry, result, started)
		return result
	}
	if err := r.discoveryManager.CheckRegistry(registry); err != nil {
		result.Error = err.Error()
		result.LatencyMS = time.Since(started).Milliseconds()
		r.logRegistryHealth(name, registry, result, started)
		return result
	}
	result.Healthy = true
	result.LatencyMS = time.Since(started).Milliseconds()
	r.logRegistryHealth(name, registry, result, started)
	return result
}

func (r *Runtime) logRegistryHealth(name string, registry config.NacosRegistry, result discovery.RegistryHealth, started time.Time) {
	if r == nil || r.logger == nil {
		return
	}
	fields := []zap.Field{
		zap.String("registry", name),
		zap.Int("servers", len(registry.Servers)),
		zap.String("namespace", registry.NamespaceID),
		zap.Bool("username_configured", registry.Username != ""),
		zap.Bool("healthy", result.Healthy),
		zap.Duration("duration", time.Since(started)),
	}
	if result.Error != "" {
		r.logger.Warn("nacos registry health check failed", append(fields, zap.String("error", result.Error))...)
		return
	}
	r.logger.Info("nacos registry health check passed", fields...)
}

// ServeHTTP delegates to the stable dispatcher.
func (r *Runtime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

// StopAccepting rejects new data requests while allowing already-acquired
// generation requests to drain. It is idempotent and is called before the
// optional load-balancer removal delay during process shutdown.
func (r *Runtime) StopAccepting() {
	if r != nil && r.global != nil {
		r.global.Stop()
	}
}

// Replace validates and publishes a new in-memory route/service generation.
// Listener, server, request, backend-transport, and global timeout settings
// are startup-owned and cannot change through this method.
func (r *Runtime) Replace(c config.Config) error {
	return r.replace(c, nil, nil)
}

// ReplaceAndPersist builds a candidate generation, persists its configuration,
// and only then makes that generation active. expectedRevision provides an
// optimistic-concurrency boundary for control-plane callers: the candidate is
// not built or persisted if another publish has already advanced Runtime.
//
// persist runs while replacement is serialized with reload and shutdown. It
// must persist the supplied configuration atomically and must not call Runtime.
// If it returns an error, the active generation and Runtime configuration are
// left unchanged and the candidate is closed.
func (r *Runtime) ReplaceAndPersist(c config.Config, expectedRevision uint64, persist func(config.Config) error) error {
	if persist == nil {
		return errors.New("runtime persistence callback is nil")
	}
	return r.replace(c, &expectedRevision, persist)
}

// replace implements both ordinary reloads and control-plane publication.
// Holding updateMu from validation through persistence prevents a failed file
// write from racing a later replacement and rolling back a newer generation.
func (r *Runtime) replace(c config.Config, expectedRevision *uint64, persist func(config.Config) error) error {
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
	if expectedRevision != nil && r.revision.Load() != *expectedRevision {
		r.mu.Unlock()
		return ErrRevisionConflict
	}
	//如果旧的generation 并且没有关闭的已经达到了8个 就拒绝重载
	if len(r.retired) >= maxRetiredGenerations {
		r.mu.Unlock()
		return ErrRetiredLimit
	}
	r.mu.Unlock()
	// 构建generation 并且返回一个commit函数 确保类事务提交
	candidate, err := buildGeneration(r.generationBuilder, c, r.transport, r.logger, r.serviceLimiterRegistry)
	if err != nil {
		return err
	}

	// Persist before publishing the candidate. updateMu prevents Close or a
	// second replacement from interleaving between this file write and the swap.
	if persist != nil {
		if err := persist(c); err != nil {
			candidate.generation.Close()
			return err
		}
	}

	r.mu.Lock()
	//提交 主要是为了限速器 limiter提交
	candidate.commit()
	//然后替换active
	old := r.active
	r.active = candidate
	shouldCloseOld := r.retireLocked(old)
	// Active handler, its configuration snapshot, and revision change as one
	// critical section. Readers never observe a new generation with old config.
	r.config = c
	newRevision := r.revision.Add(1)
	r.mu.Unlock()
	if shouldCloseOld {
		old.generation.Close()
	}
	r.publish(Event{Type: "generation_changed", Revision: newRevision, At: time.Now().UTC()})
	return nil
}

func cloneLimens(source map[string]config.LimenConfig) map[string]config.LimenConfig {
	clone := make(map[string]config.LimenConfig, len(source))
	for name, binding := range source {
		binding.Protocols = append([]string(nil), binding.Protocols...)
		binding.TrustedProxies = append([]string(nil), binding.TrustedProxies...)
		if binding.TLS != nil {
			tls := *binding.TLS
			binding.TLS = &tls
		}
		if binding.HTTP3 != nil {
			http3 := *binding.HTTP3
			binding.HTTP3 = &http3
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
	r.global.Stop()
	old := r.active
	r.active = nil
	shouldCloseOld := r.retireLocked(old)
	r.mu.Unlock()
	if shouldCloseOld {
		old.generation.Close()
	}
	r.transport.CloseIdleConnections()
	r.eventsMu.Lock()
	for ch := range r.events {
		close(ch)
		delete(r.events, ch)
	}
	r.eventsMu.Unlock()
}

func (r *Runtime) dispatch(w http.ResponseWriter, req *http.Request) {
	//获得active
	ref, ok := r.acquire()
	if !ok {
		http.Error(w, "runtime is not serving", http.StatusServiceUnavailable)
		return
	}
	//这里release就是说如果generation被取代了，然后需要关闭旧的generation，做法就是release函数去判断，是的就关闭旧的generation
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
	//acquire是根据配置创建临时的entry，并在某个service的limiter从无到有的时候，先直接注册到限速器注册表，然后引用增加1.
	//如果是热更新，则不把临时的entry进行替换，而是返回一个commit函数，和一个release函数，相当于延迟操作，也就是事务。
	serviceLimiters, commitServices, releaseServices := services.acquire(c)
	//builder是DefaultBuilder 用于构建gateway的函数，所以 c, transport, logger, serviceLimiters都是builder的参数
	//			func DefaultBuilder(c config.Config, transport http.RoundTripper, logger *zap.Logger, serviceLimiters map[string]*middleware.Limiter) (Generation, error) {
	//				return gateway.NewWithTransportAndLimiters(c, logger, transport, serviceLimiters)
	//			}
	generationCandidate, err := builder(c, transport, logger, serviceLimiters)
	if err != nil {
		if generationCandidate != nil {
			generationCandidate.Close()
		}
		//相当于回滚 消除临时的entry
		releaseServices()
		return nil, err
	}
	if generationCandidate == nil {
		releaseServices()
		return nil, fmt.Errorf("builder returned an invalid generation")
	}
	handler := generationCandidate.Handler()
	if handler == nil {
		generationCandidate.Close()
		releaseServices()
		return nil, fmt.Errorf("builder returned an invalid generation")
	}
	//这里创建一个generation
	managed := &managedGeneration{Generation: generationCandidate, release: releaseServices}
	return &generationRef{generation: managed, handler: handler, commit: commitServices}, nil
}

type managedGeneration struct {
	Generation
	release func()
	once    sync.Once
}

func (g *managedGeneration) HealthSnapshot() []telemetry.BackendHealth {
	snapshotter, ok := g.Generation.(interface {
		HealthSnapshot() []telemetry.BackendHealth
	})
	if !ok {
		return nil
	}
	return snapshotter.HealthSnapshot()
}

func (g *managedGeneration) Close() {
	g.once.Do(func() {
		defer g.release()
		g.Generation.Close()
	})
}

func (g *managedGeneration) DiscoverySnapshot() map[string]discovery.Status {
	view, ok := g.Generation.(interface {
		DiscoverySnapshot() map[string]discovery.Status
	})
	if !ok {
		return nil
	}
	return view.DiscoverySnapshot()
}

type serviceLimiterRegistry struct {
	mu      sync.Mutex
	entries map[limiterEntryKey]*serviceLimiterEntry
	metrics *telemetry.Metrics
}

type limiterEntryKey struct {
	scope string
	name  string
}

type serviceLimiterEntry struct {
	limiter *middleware.Limiter
	refs    int
}

func newServiceLimiterRegistry(metrics *telemetry.Metrics) *serviceLimiterRegistry {
	return &serviceLimiterRegistry{entries: make(map[limiterEntryKey]*serviceLimiterEntry), metrics: metrics}
}

func (r *serviceLimiterRegistry) acquire(c config.Config) (map[string]*middleware.Limiter, func(), func()) {
	r.mu.Lock()
	limiters := make(map[string]*middleware.Limiter)
	keys := make([]limiterEntryKey, 0)
	limits := make(map[limiterEntryKey]int)
	acquire := func(key limiterEntryKey, limit int) *middleware.Limiter {
		entry := r.entries[key]
		if entry == nil {
			// Acquisition is provisional until the candidate generation commits.
			entry = &serviceLimiterEntry{limiter: middleware.NewLimiterWithMetrics(limit, r.metrics, key.scope, key.name)}
			r.entries[key] = entry
		}
		entry.refs++
		limits[key] = limit
		keys = append(keys, key)
		return entry.limiter
	}
	for name, service := range c.Services {
		//遍历每个service 如果service中有in_flight的middleware 配置 先得到limit的数量
		limit, ok := serviceInFlightLimit(c, service)
		if !ok {
			continue
		}
		limiters[name] = acquire(limiterEntryKey{scope: "service", name: name}, limit)
	}
	for _, route := range c.Routes {
		limit := route.EffectiveBuiltinMiddlewareParameters(c.Settings).MaxInFlight
		limiters[gateway.RouteLimiterKey(route.Name)] = acquire(limiterEntryKey{scope: "route", name: route.Name}, limit)
	}
	r.mu.Unlock()

	var once sync.Once
	var commitOnce sync.Once
	// Return the shared limiters plus commit and rollback functions. Limits are
	// changed only after the candidate generation has passed validation.
	return limiters, func() {
			commitOnce.Do(func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				for key, limit := range limits {
					if entry := r.entries[key]; entry != nil {
						//也就是注册表中已经存在了entry。说明现在要么是app第一次启动，要么就是配置更新。如果是配置更新 commit的时候直接更新限速数量
						entry.limiter.SetLimit(limit)
					}
				}
			})
		}, func() {
			once.Do(func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				for _, key := range keys {
					entry := r.entries[key]
					if entry == nil {
						continue
					}
					entry.refs--
					if entry.refs == 0 {
						delete(r.entries, key)
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
