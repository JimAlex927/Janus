package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"go.uber.org/zap"
	"janus/internal/upstream"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestErrorMapping(t *testing.T) {
	u, _ := url.Parse("http://backend")
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"connection failure", errors.New("connection refused"), 502},
		{"deadline", context.DeadlineExceeded, 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, _ := upstream.New([]*url.URL{u})
			p := New(pool, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.err }), zap.NewNop())
			w := httptest.NewRecorder()
			p.ServeHTTP(w, httptest.NewRequest("GET", "http://gateway/", nil))
			if w.Code != tc.status {
				t.Fatalf("got %d", w.Code)
			}
		})
	}
}

func TestCancellationReachesBackend(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)
	pool, _ := upstream.New([]*url.URL{u})
	transport := NewTransport()
	defer transport.CloseIdleConnections()
	p := New(pool, transport, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "http://gateway/", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); p.ServeHTTP(httptest.NewRecorder(), r) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not receive cancellation")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not exit")
	}
}
