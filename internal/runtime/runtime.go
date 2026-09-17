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
	"janus/internal/telemetry"

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

	version                int
	limens                 map[string]config.LimenConfig
	settings               config.Settings
	generationBuilder      Builder
	logger                 *zap.Logger
	transport              *http.Transport
	global                 *middleware.Limiter
	serviceLimiterRegistry *serviceLimiterRegistry
	metrics                *telemetry.Metrics
	handler                http.Handler
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
	//传输层 用于转发请求的给runtime用的全局传输层
	transport := proxy.NewTransport(c.Settings.Backend)
	//全局共享的监控指标  进程级别的指标注册表 请求完成后可能记录：route=/api service=user-service status=200 duration=35ms
	//最后由管理接口暴露成 Prometheus 格式。 那么也就意味着全局共享，其它很多组件都要拿到它修改metric  比如 middleware
	metrics := telemetry.NewMetrics()
	//全局共享的 Service 限流器注册表  Service 级别并发限流器的注册表。 也就是每个service 如果配置了 in_flight middleware 就会在这里创建limiter
	//也就是entries里面 的value是一个limiter  *middleware.Limiter  。
	//为什么这里单独做呢？而不是交给  in_flight middleware 处理的时候限流呢？还是说middleware也会拿到里面的limiter，然后用limiter具体处理请求的时候限流？
	serviceLimiterRegistry := newServiceLimiterRegistry(metrics)
	//构建Generation 这个generation 就是路由规则某个版本
	initial, err := buildGeneration(builder, c, transport, logger, serviceLimiterRegistry)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	//这里根据配置、可能是热更新的配置，创建generation成功 然后提交，说明可以把限速器更新到注册表中了。
	//inition是generationRef   commit会完成初始化的操作 主要是限速器entry 替换 更确切的说是 已有的entry 如果limit改变 就只改变limit数量
	initial.commit()
	r := &Runtime{
		//这里新的运行时直接把创建的放进来 放在active
		active: initial,
		//retired是旧的。我有一个疑问，如果是配置更新了，这里是新的runtime对象，旧的runtime对象呢？这里应该把旧的runtime对象替换吧？
		//但也有可能就是当前NewWithBuilder方法只是app启动时候构建一次，后续配置更新不会用这个方法，但是 buildGeneration 会多次用到
		//所以当前方法构建的runtime是唯一的
		retired: make(map[*generationRef]struct{}),
		//版本号
		version: c.Version,
		//这里为什么要克隆 是怕无法修改吗？
		limens: cloneLimens(c.LimenBindings()),
		//这里应该是配置的副本
		settings: c.Settings,
		//generation的构建器，一般是默认的。
		generationBuilder: builder,
		//logger
		logger: logger,
		//运行时用的全局 process level 传输层
		transport: transport,
		//这里的global是什么限速器？应该是一个全局的限速器，不仅仅是每个service的。这里默认值就是MaxInFlight
		global: middleware.NewLimiterWithMetrics(c.Settings.Request.MaxInFlight, metrics, "global", ""),
		//限速器注册表
		serviceLimiterRegistry: serviceLimiterRegistry,
		//运行时全局监控指标
		metrics: metrics,
	}
	// This chain is process-owned and is deliberately outside the replaceable
	// generation. Its order preserves the existing behavior: observation wraps
	// protocol guards, timeout, and active-generation dispatch.
	//runtime级别的middleware chain
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
		/*
			对于非sse或者websocket到这里设置一个timeout
		*/
		middleware.Timeout(c.Settings.Request.MaximumDuration.Duration()),
		/*
			sse或者websocket的timeout middleware
		*/
		middleware.StreamTimeout(c.Settings.Stream.MaxDuration.Duration(), c.Settings.Stream.IdleTimeout.Duration()),
		/*
			检查是否是SSE或者websocket，如果是，就把写入response的时间改成infinite. 关闭只让客户端或者backed进行
		*/
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

// Metrics returns the process-owned metrics registry used by the stable
// observer, admission gates and lifecycle components.
func (r *Runtime) Metrics() *telemetry.Metrics { return r.metrics }

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

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		candidate.generation.Close()
		return ErrClosed
	}
	//提交 主要是为了限速器 limiter提交
	candidate.commit()
	//然后替换active
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

type serviceLimiterRegistry struct {
	mu      sync.Mutex
	entries map[string]*serviceLimiterEntry
	metrics *telemetry.Metrics
}

type serviceLimiterEntry struct {
	limiter *middleware.Limiter
	refs    int
}

func newServiceLimiterRegistry(metrics *telemetry.Metrics) *serviceLimiterRegistry {
	return &serviceLimiterRegistry{entries: make(map[string]*serviceLimiterEntry), metrics: metrics}
}

func (r *serviceLimiterRegistry) acquire(c config.Config) (map[string]*middleware.Limiter, func(), func()) {
	r.mu.Lock()
	limiters := make(map[string]*middleware.Limiter)
	names := make([]string, 0)
	limits := make(map[string]int)
	for name, service := range c.Services {
		//遍历每个service 如果service中有in_flight的middleware 配置 先得到limit的数量
		limit, ok := serviceInFlightLimit(c, service)
		if !ok {
			continue
		}
		//然后去查看limiter注册表中是否已经有了 如果没有就创建限速器
		entry := r.entries[name]
		if entry == nil {
			//创建限速器 然后注册到 limiter注册表中 这里是如果配置里面有新的限速器就会立即注册
			// 如果是app启动也会立即注册 如果配置热更新 然后发现新增了 也会立即注册 所以这里存在风险 如果一直不停新增 但是构建generation失败 可能泄漏
			// 但是release这里相当于是rollback，如果generation构建失败，回滚 entry被引用数量减1，就会触发回收。所以这个风险不存在。
			entry = &serviceLimiterEntry{limiter: middleware.NewLimiterWithMetrics(limit, r.metrics, "service", name)}
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
	// 返回每个service的limiter的引用map、这里的limiters是新的配置重新构建的临时entry。 name -->> limiter
	// 这个临时entry 还没有替换到限速器的注册表中，因为要考虑到可能构建新的Genration失败，所以延迟替换。
	//这也就是为什么返回第一个func() 也就是一个commit 的动作
	return limiters, func() {
			commitOnce.Do(func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				for name, limit := range limits {
					if entry := r.entries[name]; entry != nil {
						//也就是注册表中已经存在了entry。说明现在要么是app第一次启动，要么就是配置更新。如果是配置更新 commit的时候直接更新限速数量
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
