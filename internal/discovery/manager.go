package discovery

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"janus/internal/config"
	"janus/internal/upstream"
)

// Manager is owned by one Runtime. Client and subscription references are
// acquired provisionally during generation construction and released on either
// rollback or the last in-flight request leaving a retired generation.
// Configurations are part of the keys, so a candidate never mutates an active
// generation's namespace, credentials, service or expiry policy.
type Manager struct {
	mu      sync.Mutex
	factory Factory
	clients map[string]*clientRef
	feeds   map[string]*feedRef
}

type clientRef struct {
	client Client
	refs   int // distinct subscribed feeds, not HTTP requests
}

type feedRef struct {
	refs       int // generation/service owners
	clientKey  string
	client     Client
	query      config.NacosService
	pool       *upstream.DynamicPool
	staleAfter time.Duration
	version    uint64
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}
	unwatch    func()
	statusMu   sync.Mutex
	status     Status
}

// Status contains no credentials or registry error text. The UI can distinguish
// empty membership from unavailable discovery and expired cached membership.
type Status struct {
	LastUpdate time.Time `json:"last_update"`
	ExpiresAt  time.Time `json:"expires_at"`
	Instances  int       `json:"instances"`
	Failed     bool      `json:"failed"`
	Expired    bool      `json:"expired"`
}

type Lease struct {
	Pool    *upstream.DynamicPool
	feed    *feedRef
	release func()
}

func (l *Lease) Close() { l.release() }
func (l *Lease) Status() Status {
	l.feed.statusMu.Lock()
	defer l.feed.statusMu.Unlock()
	s := l.feed.status
	s.Expired = !s.ExpiresAt.IsZero() && !time.Now().Before(s.ExpiresAt)
	return s
}

func NewManager(factory Factory) *Manager {
	return &Manager{factory: factory, clients: make(map[string]*clientRef), feeds: make(map[string]*feedRef)}
}

func key(value any) string { data, _ := json.Marshal(value); return string(data) }

// Acquire may contact Nacos; it is called only when building configuration.
// A valid empty service can be published and returns 503 until instances arrive.
// Failure to establish a new subscription rejects the candidate configuration.
func (m *Manager) Acquire(registry config.NacosRegistry, query config.NacosService) (*Lease, error) {
	registry = registry.WithDefaults()
	query = query.WithDefaults()
	clientKey := key(registry)
	feedKey := clientKey + "\x00" + key(query)
	m.mu.Lock()
	defer m.mu.Unlock()
	if f := m.feeds[feedKey]; f != nil {
		f.refs++
		return m.lease(feedKey, f), nil
	}
	c := m.clients[clientKey]
	if c == nil {
		client, err := m.factory(registry)
		if err != nil {
			return nil, err
		}
		c = &clientRef{client: client}
		m.clients[clientKey] = c
	}
	f := &feedRef{refs: 1, clientKey: clientKey, client: c.client, query: query,
		pool: &upstream.DynamicPool{}, staleAfter: registry.StaleAfter.Duration(),
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	cancel, err := c.client.Watch(query, func() {
		// SDK callbacks never hold the Manager lock or wait on HTTP consumers.
		select {
		case f.wake <- struct{}{}:
		default:
		}
	})
	if err == nil {
		f.unwatch = cancel
		err = f.refresh()
	}
	if err != nil {
		if cancel != nil {
			cancel()
		}
		if c.refs == 0 {
			delete(m.clients, clientKey)
			c.client.Close()
		}
		return nil, fmt.Errorf("nacos service %q: %w", query.ServiceName, err)
	}
	c.refs++
	m.feeds[feedKey] = f
	go f.run()
	return m.lease(feedKey, f), nil
}

func (m *Manager) lease(feedKey string, f *feedRef) *Lease {
	var once sync.Once
	return &Lease{Pool: f.pool, feed: f, release: func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			f.refs--
			if f.refs != 0 {
				return
			}
			delete(m.feeds, feedKey)
			close(f.stop)
			<-f.done
			f.unwatch()
			c := m.clients[f.clientKey]
			c.refs--
			if c.refs == 0 {
				delete(m.clients, f.clientKey)
				c.client.Close()
			}
		})
	}}
}

func (f *feedRef) run() {
	defer close(f.done)
	// The SDK also refreshes unchanged services without invoking a change
	// callback. Observe its version periodically; a cached read alone isn't fresh.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-f.wake:
		case <-ticker.C:
		}
		select {
		case <-f.stop:
			return
		default:
		}
		_ = f.refresh()
	}
}

func (f *feedRef) refresh() error {
	snapshot, err := f.client.Snapshot(f.query)
	f.statusMu.Lock()
	defer f.statusMu.Unlock()
	if err != nil {
		f.status.Failed = true
		return err
	}
	if snapshot.Version != 0 && snapshot.Version <= f.version {
		return nil
	}
	targets, err := targetsFor(snapshot, f.query.Scheme)
	if err != nil {
		f.status.Failed = true
		return err
	}
	now := time.Now()
	expires := now.Add(f.staleAfter)
	if err = f.pool.Replace(targets, expires); err != nil {
		f.status.Failed = true
		return err
	}
	f.version = snapshot.Version
	f.status = Status{LastUpdate: now, ExpiresAt: expires, Instances: len(targets)}
	return nil
}
