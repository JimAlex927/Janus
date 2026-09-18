package runtime

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/discovery"
)

type registryStub struct {
	mu      sync.Mutex
	value   discovery.Snapshot
	notify  func()
	watches atomic.Int32
	cancels atomic.Int32
	closes  atomic.Int32
}

func (s *registryStub) Watch(_ config.NacosService, notify func()) (func(), error) {
	s.mu.Lock()
	s.notify = notify
	s.mu.Unlock()
	s.watches.Add(1)
	return func() { s.cancels.Add(1) }, nil
}
func (s *registryStub) Snapshot(config.NacosService) (discovery.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, nil
}
func (s *registryStub) ServerHealthy() bool { return true }
func (s *registryStub) Close()              { s.closes.Add(1) }
func (s *registryStub) update(version uint64, backend string) {
	value := discovery.Snapshot{Version: version}
	if backend != "" {
		u, _ := url.Parse(backend)
		ip, port, _ := net.SplitHostPort(u.Host)
		number, _ := strconv.ParseUint(port, 10, 64)
		value.Instances = []discovery.Instance{{IP: ip, Port: number, Weight: 1, Healthy: true, Enabled: true}}
	}
	s.mu.Lock()
	s.value = value
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func discoveredConfig() config.Config {
	return config.Config{Listen: "127.0.0.1:8080",
		Discovery: config.DiscoveryConfig{Nacos: map[string]config.NacosRegistry{"main": {NamespaceID: "one", Servers: []config.NacosServer{{Address: "127.0.0.1", Port: 8848}}}}},
		Services:  map[string]config.Service{"s": {Nacos: &config.NacosService{Registry: "main", ServiceName: "orders"}}},
		Routes:    []config.Route{{Name: "r", PathPrefix: "/", Service: "s"}},
	}
}

func TestDiscoveredBackendUpdateAndEmptyResultAtHTTPClient(t *testing.T) {
	one := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "one") }))
	defer one.Close()
	two := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "two") }))
	defer two.Close()
	stub := &registryStub{}
	stub.update(1, one.URL)
	var clients atomic.Int32
	manager := discovery.NewManager(func(reg config.NacosRegistry) (discovery.Client, error) {
		if reg.NamespaceID == "broken" {
			return nil, errors.New("offline")
		}
		clients.Add(1)
		return stub, nil
	})
	c := discoveredConfig()
	rt, err := NewWithDiscovery(c, nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	frontend := httptest.NewServer(rt)
	defer frontend.Close()
	client := &http.Client{Timeout: time.Second}
	assertResponse := func(wantStatus int, wantBody string) {
		t.Helper()
		response, err := client.Get(frontend.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != wantStatus || (wantBody != "" && string(body) != wantBody) {
			t.Fatalf("client result = %d %q", response.StatusCode, body)
		}
	}
	waitUpdate := func(previous time.Time) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if rt.DiscoverySnapshot()["s"].LastUpdate.After(previous) {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("membership update not applied")
	}
	assertResponse(200, "one")
	before := rt.DiscoverySnapshot()["s"].LastUpdate
	stub.update(2, two.URL)
	waitUpdate(before)
	assertResponse(200, "two")
	if rt.Revision() != 1 {
		t.Fatal("membership update rebuilt route generation")
	}
	if err := rt.Replace(c); err != nil {
		t.Fatal(err)
	}
	if clients.Load() != 1 || stub.watches.Load() != 1 || stub.cancels.Load() != 0 {
		t.Fatal("route reload replaced a shared subscription")
	}
	broken := discoveredConfig()
	reg := broken.Discovery.Nacos["main"]
	reg.NamespaceID = "broken"
	broken.Discovery.Nacos["main"] = reg
	if rt.Replace(broken) == nil {
		t.Fatal("published unavailable namespace")
	}
	assertResponse(200, "two")
	before = rt.DiscoverySnapshot()["s"].LastUpdate
	stub.update(3, "")
	waitUpdate(before)
	assertResponse(503, "")
	before = rt.DiscoverySnapshot()["s"].LastUpdate
	stub.update(4, one.URL)
	waitUpdate(before)
	assertResponse(200, "one")
	rt.Close()
	if stub.cancels.Load() != 1 || stub.closes.Load() != 1 {
		t.Fatal("runtime close leaked registry resources")
	}
}

func TestRetiredGenerationKeepsDiscoveryUntilRequestDrains(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	release := func() { unblock.Do(func() { close(finish) }) }
	defer release()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-finish
		_, _ = io.WriteString(w, "completed")
	}))
	defer backend.Close()
	stub := &registryStub{}
	stub.update(1, backend.URL)
	manager := discovery.NewManager(func(config.NacosRegistry) (discovery.Client, error) { return stub, nil })
	rt, err := NewWithDiscovery(discoveredConfig(), nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	frontend := httptest.NewServer(rt)
	defer frontend.Close()
	defer release()
	done := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: 3 * time.Second}).Get(frontend.URL)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			err = readErr
			if err == nil && (response.StatusCode != 200 || string(body) != "completed") {
				err = errors.New("in-flight client response was interrupted")
			}
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not enter backend")
	}
	replacement := discoveredConfig()
	replacement.Services = map[string]config.Service{"s": {Upstreams: []string{backend.URL}}}
	if err := rt.Replace(replacement); err != nil {
		release()
		t.Fatal(err)
	}
	if stub.closes.Load() != 0 {
		release()
		t.Fatal("retired generation closed discovery before drain")
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && stub.closes.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if stub.closes.Load() != 1 || stub.cancels.Load() != 1 {
		t.Fatal("last retired request did not release discovery")
	}
}
