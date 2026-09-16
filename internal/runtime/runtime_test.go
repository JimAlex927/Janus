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
	"janus/internal/middleware"

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
	builder := func(_ config.Config, transport http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
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
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
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
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
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
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
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
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
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

func TestRuntimeGlobalAdmissionIsSharedByAllGenerations(t *testing.T) {
	c := validRuntimeConfig("initial")
	c.Settings.Request.MaxInFlight = 1
	started, release := make(chan struct{}, 1), make(chan struct{})
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				started <- struct{}{}
				<-release
				w.WriteHeader(http.StatusCreated)
			}),
			closed: make(chan struct{}),
		}, nil
	}
	r, err := NewWithBuilder(c, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		r.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter handler")
	}
	second := httptest.NewRecorder()
	r.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("global saturation status = %d, want 503", second.Code)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first response = %d, want 201", firstResponse.Code)
	}
}

func TestRuntimeServiceAdmissionSurvivesGenerationReplacement(t *testing.T) {
	c := validRuntimeConfig("first")
	c.Middlewares = map[string]config.Middleware{
		"service-cap": {InFlight: &config.InFlightSettings{MaxConcurrent: 1}},
	}
	c.Services["service"] = config.Service{
		Upstreams:   []string{"http://127.0.0.1:9000"},
		Middlewares: []string{"service-cap"},
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	built := 0
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger, limiters map[string]*middleware.Limiter) (Generation, error) {
		limiter := limiters["service"]
		if limiter == nil {
			return nil, errors.New("service limiter was not injected")
		}
		built++
		id := built
		return &testGeneration{
			handler: middleware.Admission(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if id == 1 {
					started <- struct{}{}
					<-release
				}
				w.WriteHeader(http.StatusCreated)
			})),
			closed: make(chan struct{}),
		}, nil
	}
	r, err := NewWithBuilder(c, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	firstDone := make(chan struct{})
	go func() {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first service request did not enter handler")
	}
	replacement := c
	replacement.Routes = []config.Route{{Name: "second", PathPrefix: "/", Service: "service"}}
	if err := r.Replace(replacement); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	r.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("service saturation across generations = %d, want 503", second.Code)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("old service request did not finish")
	}
	third := httptest.NewRecorder()
	r.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if third.Code != http.StatusCreated {
		t.Fatalf("service admission after release = %d, want 201", third.Code)
	}
}

func TestRuntimeFailedReplacementDoesNotChangeServiceLimit(t *testing.T) {
	c := validRuntimeConfig("first")
	c.Middlewares = map[string]config.Middleware{
		"service-cap": {InFlight: &config.InFlightSettings{MaxConcurrent: 1}},
	}
	c.Services["service"] = config.Service{
		Upstreams:   []string{"http://127.0.0.1:9000"},
		Middlewares: []string{"service-cap"},
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, limiters map[string]*middleware.Limiter) (Generation, error) {
		if c.Routes[0].Name == "bad" {
			return nil, errors.New("candidate rejected")
		}
		limiter := limiters["service"]
		return &testGeneration{
			handler: middleware.Admission(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				started <- struct{}{}
				<-release
				w.WriteHeader(http.StatusCreated)
			})),
			closed: make(chan struct{}),
		}, nil
	}
	r, err := NewWithBuilder(c, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	firstDone := make(chan struct{})
	go func() {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first service request did not enter handler")
	}
	replacement := c
	replacement.Middlewares = map[string]config.Middleware{
		"service-cap": {InFlight: &config.InFlightSettings{MaxConcurrent: 2}},
	}
	replacement.Routes = []config.Route{{Name: "bad", PathPrefix: "/", Service: "service"}}
	if err := r.Replace(replacement); err == nil || err.Error() != "candidate rejected" {
		t.Fatalf("replacement error = %v, want candidate rejection", err)
	}
	second := httptest.NewRecorder()
	r.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed replacement changed active service cap: status=%d, want 503", second.Code)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first service request did not finish")
	}
}
