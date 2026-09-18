package discovery

import (
	"errors"
	"sync"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/upstream"
)

type fakeClient struct {
	mu                       sync.Mutex
	value                    Snapshot
	err                      error
	watchErr                 error
	notify                   func()
	watches, cancels, closes int
}

func (c *fakeClient) Watch(_ config.NacosService, notify func()) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.watches++
	c.notify = notify
	return func() { c.mu.Lock(); defer c.mu.Unlock(); c.cancels++ }, c.watchErr
}
func (c *fakeClient) Snapshot(config.NacosService) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, c.err
}
func (c *fakeClient) Close() { c.mu.Lock(); defer c.mu.Unlock(); c.closes++ }
func (c *fakeClient) set(value Snapshot, err error) {
	c.mu.Lock()
	c.value = value
	c.err = err
	notify := c.notify
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}
func sample(version uint64, ip string) Snapshot {
	return Snapshot{Version: version, Instances: []Instance{{IP: ip, Port: 8080, Weight: 1, Healthy: true, Enabled: true}}}
}

func TestManagerSharesWithinNamespaceAndReleasesLastLease(t *testing.T) {
	clients := []*fakeClient{}
	m := NewManager(func(config.NacosRegistry) (Client, error) {
		c := &fakeClient{value: sample(1, "127.0.0.1")}
		clients = append(clients, c)
		return c, nil
	})
	query := config.NacosService{ServiceName: "orders"}
	registry := config.NacosRegistry{NamespaceID: "one"}
	a, err := m.Acquire(registry, query)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Acquire(registry, query)
	if err != nil {
		t.Fatal(err)
	}
	if a.Pool != b.Pool || len(clients) != 1 || clients[0].watches != 1 {
		t.Fatal("identical subscriptions not shared")
	}
	registry.NamespaceID = "two"
	c, err := m.Acquire(registry, query)
	if err != nil {
		t.Fatal(err)
	}
	if a.Pool == c.Pool || len(clients) != 2 {
		t.Fatal("namespace isolation failed")
	}
	a.Close()
	a.Close()
	if clients[0].closes != 0 {
		t.Fatal("closed a still-owned subscription")
	}
	b.Close()
	c.Close()
	for _, client := range clients {
		if client.cancels != 1 || client.closes != 1 {
			t.Fatalf("resource leak: %d cancels, %d closes", client.cancels, client.closes)
		}
	}
	if len(m.feeds) != 0 || len(m.clients) != 0 {
		t.Fatal("manager retained released entries")
	}
}

func TestManagerRollsBackFailedSubscriptionAndInitialSnapshot(t *testing.T) {
	for _, watchFailure := range []bool{false, true} {
		c := &fakeClient{err: errors.New("unavailable")}
		if watchFailure {
			c.watchErr = errors.New("subscribe failed")
		}
		m := NewManager(func(config.NacosRegistry) (Client, error) { return c, nil })
		if _, err := m.Acquire(config.NacosRegistry{}, config.NacosService{ServiceName: "s"}); err == nil {
			t.Fatal("accepted failed subscription")
		}
		if c.cancels != 1 || c.closes != 1 || len(m.clients) != 0 {
			t.Fatal("failed construction leaked resources")
		}
	}
}

func TestDifferentServicesShareClientButNotSubscription(t *testing.T) {
	c := &fakeClient{value: sample(1, "127.0.0.1")}
	created := 0
	m := NewManager(func(config.NacosRegistry) (Client, error) { created++; return c, nil })
	a, err := m.Acquire(config.NacosRegistry{}, config.NacosService{ServiceName: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Acquire(config.NacosRegistry{}, config.NacosService{ServiceName: "b"})
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	if created != 1 || c.watches != 2 || a.Pool == b.Pool {
		t.Fatal("client/feed sharing boundary is wrong")
	}
	a.Close()
	if c.closes != 0 {
		t.Fatal("one Service closed another Service's client")
	}
	b.Close()
	if c.closes != 1 || c.cancels != 2 {
		t.Fatal("shared client not released")
	}
}

func TestSnapshotFreshnessEmptyAndRecovery(t *testing.T) {
	c := &fakeClient{value: sample(1, "127.0.0.1")}
	f := &feedRef{client: c, query: config.NacosService{Scheme: "http"}, pool: &upstream.DynamicPool{}, staleAfter: -time.Second}
	if err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	expires := f.status.ExpiresAt
	f.staleAfter = time.Minute
	if err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	if f.status.ExpiresAt != expires {
		t.Fatal("cached reads renewed stale membership")
	}
	if _, ok := f.pool.NextHealthy(); ok {
		t.Fatal("expired membership used")
	}
	c.value = sample(2, "127.0.0.2")
	if err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	if got, ok := f.pool.NextHealthy(); !ok || got.Host != "127.0.0.2:8080" {
		t.Fatal("fresh data did not recover")
	}
	c.value = sample(1, "127.0.0.1")
	if err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.pool.NextHealthy(); got.Host != "127.0.0.2:8080" {
		t.Fatal("out-of-order update restored removed instance")
	}
	c.err = errors.New("offline")
	if f.refresh() == nil {
		t.Fatal("missing error")
	}
	if _, ok := f.pool.NextHealthy(); !ok {
		t.Fatal("transient failure prematurely cleared pool")
	}
	c.err = nil
	c.value = Snapshot{Version: 3}
	if err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.pool.NextHealthy(); ok {
		t.Fatal("authoritative empty membership kept old instances")
	}
}

func TestDiscoveryFiltersAndValidatesInstances(t *testing.T) {
	s := sample(1, "::1")
	s.Instances = append(s.Instances, s.Instances[0], Instance{IP: "bad", Healthy: false}, Instance{IP: "bad", Healthy: true, Enabled: false})
	targets, err := targetsFor(s, "https")
	if err != nil || len(targets) != 1 || targets[0].URL.String() != "https://[::1]:8080" {
		t.Fatalf("targets: %v %v", targets, err)
	}
	s.Instances = append(s.Instances, Instance{IP: "not-an-ip", Port: 80, Healthy: true, Enabled: true, Weight: 1})
	if _, err := targetsFor(s, "http"); err == nil {
		t.Fatal("invalid instance accepted")
	}
}

func TestSubscriptionCallbackUpdatesLocalPool(t *testing.T) {
	c := &fakeClient{value: sample(1, "127.0.0.1")}
	m := NewManager(func(config.NacosRegistry) (Client, error) { return c, nil })
	lease, err := m.Acquire(config.NacosRegistry{}, config.NacosService{ServiceName: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	c.set(sample(2, "127.0.0.2"), nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, ok := lease.Pool.NextHealthy(); ok && got.Host == "127.0.0.2:8080" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("subscription did not update pool")
}
