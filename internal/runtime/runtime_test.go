package runtime

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"janus/internal/config"

	"go.uber.org/zap"
)

type testGeneration struct {
	handler   http.Handler
	closed    chan struct{}
	closeOnce sync.Once
}

func (g *testGeneration) Handler() http.Handler { return g.handler }

func (g *testGeneration) Close() {
	g.closeOnce.Do(func() { close(g.closed) })
}

func validRuntimeConfig(name string) config.Config {
	return config.Config{
		Listen:   "127.0.0.1:8080",
		Services: map[string]config.Service{"service": {Upstreams: []string{"http://127.0.0.1:9000"}}},
		Routes:   []config.Route{{Name: name, PathPrefix: "/", Service: "service"}},
	}
}

func TestRuntimeKeepsStableHandlerAcrossReplacement(t *testing.T) {
	built := 0
	var transports []http.RoundTripper
	builder := func(_ config.Config, transport http.RoundTripper, _ *zap.Logger) (Generation, error) {
		built++
		transports = append(transports, transport)
		body := "generation-" + string(rune('0'+built))
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }),
			closed:  make(chan struct{}),
		}, nil
	}
	r, err := NewWithBuilder(validRuntimeConfig("first"), zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	stable := r.Handler()

	first := httptest.NewRecorder()
	stable.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if first.Code != http.StatusOK || first.Body.String() != "generation-1" {
		t.Fatalf("initial response = %d %q", first.Code, first.Body.String())
	}
	if err := r.Replace(validRuntimeConfig("second")); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	stable.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if second.Code != http.StatusOK || second.Body.String() != "generation-2" {
		t.Fatalf("replacement response = %d %q", second.Code, second.Body.String())
	}
	if len(transports) != 2 || transports[0] != transports[1] {
		t.Fatal("replacement did not reuse the process-owned transport")
	}
}

func TestRuntimeReplacementRollbackClosesCandidate(t *testing.T) {
	oldClosed := make(chan struct{})
	candidateClosed := make(chan struct{})
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger) (Generation, error) {
		if c.Routes[0].Name == "bad" {
			return &testGeneration{
				handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}),
				closed:  candidateClosed,
			}, errors.New("candidate rejected")
		}
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "old") }),
			closed:  oldClosed,
		}, nil
	}
	r, err := NewWithBuilder(validRuntimeConfig("good"), zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Replace(validRuntimeConfig("bad")); err == nil || err.Error() != "candidate rejected" {
		t.Fatalf("Replace error = %v, want candidate rejection", err)
	}
	select {
	case <-candidateClosed:
	case <-time.After(time.Second):
		t.Fatal("failed candidate was not closed")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if w.Body.String() != "old" {
		t.Fatalf("rollback response = %q, want old generation", w.Body.String())
	}
}

func TestRuntimeRetiresGenerationAfterRequestRelease(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	oldClosed := make(chan struct{})
	newClosed := make(chan struct{})
	built := 0
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger) (Generation, error) {
		built++
		id := built
		closed := newClosed
		if id == 1 {
			closed = oldClosed
		}
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if id == 1 {
					close(started)
					<-release
				}
				_, _ = io.WriteString(w, "done")
			}),
			closed: closed,
		}, nil
	}
	r, err := NewWithBuilder(validRuntimeConfig("first"), zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	requestDone := make(chan struct{})
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
		close(requestDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old request did not start")
	}
	if err := r.Replace(validRuntimeConfig("second")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldClosed:
		t.Fatal("old generation closed while a request was active")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("old request did not finish")
	}
	select {
	case <-oldClosed:
	case <-time.After(time.Second):
		t.Fatal("old generation was not closed after request release")
	}
}

func TestRuntimePanicStillReleasesGeneration(t *testing.T) {
	oldClosed := make(chan struct{})
	built := 0
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger) (Generation, error) {
		built++
		closed := make(chan struct{})
		if built == 1 {
			closed = oldClosed
		}
		return &testGeneration{
			handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { panic("handler panic") }),
			closed:  closed,
		}, nil
	}
	r, err := NewWithBuilder(validRuntimeConfig("panic"), zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("request panic was not propagated")
			}
		}()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	}()
	if err := r.Replace(validRuntimeConfig("replacement")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldClosed:
	case <-time.After(time.Second):
		t.Fatal("panic path did not release the old generation")
	}
}

func TestRuntimeBoundsRetiredGenerations(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, maxRetiredGenerations+1)
	var requests sync.WaitGroup
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger) (Generation, error) {
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				started <- struct{}{}
				<-release
				_, _ = io.WriteString(w, "done")
			}),
			closed: make(chan struct{}),
		}, nil
	}
	r, err := NewWithBuilder(validRuntimeConfig("initial"), zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(release)
		requests.Wait()
		r.Close()
	}()

	for i := 0; i < maxRetiredGenerations; i++ {
		requests.Add(1)
		go func() {
			defer requests.Done()
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("generation request did not start")
		}
		if err := r.Replace(validRuntimeConfig("replacement")); err != nil {
			t.Fatalf("replacement %d failed: %v", i, err)
		}
	}
	if err := r.Replace(validRuntimeConfig("overflow")); !errors.Is(err, ErrRetiredLimit) {
		t.Fatalf("overflow replacement error = %v, want %v", err, ErrRetiredLimit)
	}
}
