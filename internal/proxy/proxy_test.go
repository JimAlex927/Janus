package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"janus/internal/config"

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

func TestNewTransportUsesConfiguredSettings(t *testing.T) {
	settings := config.DefaultSettings().Backend
	settings.TLSHandshakeTimeout = config.Duration(1200 * time.Millisecond)
	settings.ResponseHeaderTimeout = config.Duration(1800 * time.Millisecond)
	settings.MaxResponseHeaderBytes = 96 << 10
	settings.MaxIdleConns = 17
	settings.MaxIdleConnsPerHost = 5
	settings.MaxConnsPerHost = 9
	settings.IdleConnTimeout = config.Duration(11 * time.Second)
	disableCompression := false
	settings.DisableCompression = &disableCompression

	transport := NewTransport(settings)
	defer transport.CloseIdleConnections()
	if transport.TLSHandshakeTimeout != 1200*time.Millisecond ||
		transport.ResponseHeaderTimeout != 1800*time.Millisecond ||
		transport.MaxResponseHeaderBytes != 96<<10 || transport.MaxIdleConns != 17 ||
		transport.MaxIdleConnsPerHost != 5 || transport.MaxConnsPerHost != 9 ||
		transport.IdleConnTimeout != 11*time.Second || transport.DisableCompression {
		t.Fatalf("transport settings were not applied: %+v", transport)
	}
}

func TestTransportNegotiatesHTTP2ToHTTPSBackend(t *testing.T) {
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	transport := NewTransport()
	transport.TLSClientConfig = backend.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	defer transport.CloseIdleConnections()
	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := upstream.New([]*url.URL{u})
	if err != nil {
		t.Fatal(err)
	}
	proxyHandler := New(pool, transport, zap.NewNop())
	w := httptest.NewRecorder()
	proxyHandler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if w.Code != http.StatusOK || w.Body.String() != "HTTP/2.0" {
		t.Fatalf("backend response = %d %q, want negotiated HTTP/2", w.Code, w.Body.String())
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
