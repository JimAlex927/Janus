package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"janus/internal/config"
	"janus/internal/limen"
)

func serveTestGateway(t *testing.T, g *Gateway, settings config.Settings) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := limen.New(ln.Addr().String(), g, settings)
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		g.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("test server stopped: %v", err)
		}
	})
	return "http://" + ln.Addr().String()
}

func testGateway(t *testing.T, upstream string) *Gateway {
	return testGatewayWithSettings(t, upstream, config.DefaultSettings())
}

func testGatewayWithSettings(t *testing.T, upstream string, settings config.Settings) *Gateway {
	return testGatewayWithConfig(t, config.Config{
		Listen:   "127.0.0.1:8080",
		Settings: settings,
		Services: map[string]config.Service{"s": {Upstreams: []string{upstream}}},
		Routes:   []config.Route{{Name: "api", PathPrefix: "/api", Service: "s"}},
	})
}

func testGatewayWithConfig(t *testing.T, c config.Config) *Gateway {
	t.Helper()
	g, err := New(c, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

func durationPointer(value time.Duration) *config.Duration {
	duration := config.Duration(value)
	return &duration
}

func intPointer(value int) *int { return &value }

func TestForwardingOverRealConnections(t *testing.T) {
	type observed struct{ uri, host, body, xff, proto, originalHost, forwarded, realIP, forwardedPort, scheme, hop string }
	seen := make(chan observed, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- observed{r.RequestURI, r.Host, string(body), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host"), r.Header.Get("Forwarded"), r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-Port"), r.Header.Get("X-Scheme"), r.Header.Get("X-Hop")}
		w.Header().Set("Trailer", "X-Checksum")
		w.Header().Set("Connection", "X-Backend-Hop")
		w.Header().Set("X-Backend-Hop", "private")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, "saved")
		w.Header().Set("X-Checksum", "abc")
	}))
	defer backend.Close()
	front := httptest.NewServer(testGateway(t, backend.URL))
	defer front.Close()
	r, err := http.NewRequest("POST", front.URL+"/api/a%2Fb?x=1&x=2", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "client.example"
	for k, v := range map[string]string{"Connection": "X-Hop", "X-Hop": "private", "X-Forwarded-For": "203.0.113.99", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "spoof.example", "X-Forwarded-Port": "443", "Forwarded": "for=spoof", "X-Real-IP": "spoof", "X-Scheme": "https"} {
		r.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 || string(body) != "saved" || resp.Trailer.Get("X-Checksum") != "abc" || resp.Header.Get("X-Backend-Hop") != "" {
		t.Fatalf("bad response: status=%d body=%q headers=%v trailers=%v", resp.StatusCode, body, resp.Header, resp.Trailer)
	}
	got := <-seen
	if got.uri != "/api/a%2Fb?x=1&x=2" || got.body != "payload" || got.host != strings.TrimPrefix(backend.URL, "http://") {
		t.Fatalf("bad forwarded request: %+v", got)
	}
	if got.xff != "127.0.0.1" || got.proto != "http" || got.originalHost != "client.example" || got.forwarded != "" || got.realIP != "" || got.forwardedPort != "" || got.scheme != "" || got.hop != "" {
		t.Fatalf("untrusted headers propagated: %+v", got)
	}
}

func TestTrustedForwardingOverRealConnection(t *testing.T) {
	seen := make(chan [3]string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- [3]string{r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host")}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	g := testGatewayWithConfig(t, config.Config{
		Listen:         "127.0.0.1:8080",
		TrustedProxies: []string{"127.0.0.0/8"},
		Services:       map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes:         []config.Route{{Name: "api", PathPrefix: "/api", Service: "s"}},
	})
	front := serveTestGateway(t, g, config.DefaultSettings())
	req, err := http.NewRequest(http.MethodGet, front+"/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 127.0.0.2")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "public.example:443")
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	got := <-seen
	if got != [3]string{"203.0.113.9, 127.0.0.2, 127.0.0.1", "https", "public.example:443"} {
		t.Fatalf("forwarded identity = %#v", got)
	}
}

func TestHTTPSCertificateVerification(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer backend.Close()
	g := testGateway(t, backend.URL)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "http://gateway/api", nil))
	if w.Code != 502 {
		t.Fatalf("untrusted certificate accepted: %d", w.Code)
	}
	// Trust only this test certificate, preserving normal hostname verification.
	g = testGateway(t, backend.URL)
	g.transport.TLSClientConfig = backend.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	w = httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "http://gateway/api", nil))
	if w.Code != 204 {
		t.Fatalf("trusted HTTPS failed: %d", w.Code)
	}
}

func TestUnsupportedTunnel(t *testing.T) {
	g := testGateway(t, "http://127.0.0.1:9000")
	r := httptest.NewRequest(http.MethodConnect, "http://gateway/api", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("CONNECT: got %d", w.Code)
	}
}

func TestOverallTimeoutCancelsBackend(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer backend.Close()

	settings := config.DefaultSettings()
	settings.Request.MaximumDuration = config.Duration(50 * time.Millisecond)
	g, err := New(config.Config{
		Listen:   "127.0.0.1:8080",
		Settings: settings,
		Services: map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "api", PathPrefix: "/api", Service: "s"}},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		g.ServeHTTP(w, httptest.NewRequest("GET", "http://gateway/api", nil))
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not start")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("overall timeout did not end request")
	}
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", w.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("overall timeout did not cancel backend")
	}
}

func TestRouteTimeoutOverrideCanExtendGlobalMaximumDuration(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	settings := config.DefaultSettings()
	settings.Request.MaximumDuration = config.Duration(30 * time.Millisecond)
	settings.Server.WriteTimeout = config.Duration(40 * time.Millisecond)
	g := testGatewayWithConfig(t, config.Config{
		Listen:   "127.0.0.1:8080",
		Settings: settings,
		Services: map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes: []config.Route{{
			Name: "api", PathPrefix: "/api", Service: "s",
			BuiltinMiddlewareOverrides: &config.BuiltinMiddlewareOverrides{
				Timeout:      &config.TimeoutMiddlewareOverride{MaximumDuration: durationPointer(250 * time.Millisecond)},
				WriteTimeout: &config.WriteTimeoutMiddlewareOverride{Timeout: durationPointer(300 * time.Millisecond)},
			},
		}},
	})
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/api", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("route extended timeout status = %d, want 204", w.Code)
	}
}

func TestRouteAdmissionOverrideLimitsOnlyMatchedRoute(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	g := testGatewayWithConfig(t, config.Config{
		Listen:   "127.0.0.1:8080",
		Services: map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes: []config.Route{{
			Name: "api", PathPrefix: "/api", Service: "s",
			BuiltinMiddlewareOverrides: &config.BuiltinMiddlewareOverrides{
				Admission: &config.AdmissionMiddlewareOverride{MaxInFlight: intPointer(1)},
			},
		}},
	})
	firstDone := make(chan struct{})
	go func() {
		g.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gateway/api", nil))
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first route request did not reach backend")
	}
	second := httptest.NewRecorder()
	g.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "http://gateway/api", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated route status = %d, want 503", second.Code)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first route request did not finish")
	}
}

func TestOverallTimeoutReturns504OverRealConnection(t *testing.T) {
	started := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer backend.Close()

	settings := config.DefaultSettings()
	settings.Request.MaximumDuration = config.Duration(50 * time.Millisecond)
	settings.Server.WriteTimeout = config.Duration(500 * time.Millisecond)
	g := testGatewayWithSettings(t, backend.URL, settings)
	front := serveTestGateway(t, g, settings)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(front + "/api")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
}

func TestOverallTimeoutTerminatesAfterResponseCommitment(t *testing.T) {
	started := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = io.WriteString(w, "prefix")
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer backend.Close()

	settings := config.DefaultSettings()
	settings.Request.MaximumDuration = config.Duration(75 * time.Millisecond)
	settings.Server.WriteTimeout = config.Duration(500 * time.Millisecond)
	g := testGatewayWithSettings(t, backend.URL, settings)
	front := serveTestGateway(t, g, settings)

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front, "http://"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /api HTTP/1.1\r\nHost: gateway\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	raw, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	response := string(raw)
	if !strings.HasPrefix(response, "HTTP/1.1 200 OK\r\n") || !strings.Contains(response, "prefix") || strings.Contains(response, "0\r\n\r\n") || strings.Contains(response, "504 Gateway Timeout") {
		t.Fatalf("committed response = %q; want incomplete 200 response without 504", response)
	}
}

func TestClientCancellationReachesBackendOverRealConnection(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer backend.Close()

	g := testGateway(t, backend.URL)
	front := serveTestGateway(t, g, config.DefaultSettings())
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, front+"/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation did not reach backend")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("client request unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client request did not finish after cancellation")
	}
}

func TestBufferedRouteCommitsOnlyAfterBackendCompletes(t *testing.T) {
	prefixWritten, release := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "prefix")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(prefixWritten)
		<-release
		_, _ = io.WriteString(w, "suffix")
	}))
	defer backend.Close()

	c := config.Config{
		Listen: "127.0.0.1:8080",
		Middlewares: map[string]config.Middleware{
			"buffered": {Buffer: &config.BufferSettings{MaxResponseBodyBytes: 64}},
		},
		Services: map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "api", PathPrefix: "/api", Service: "s", Middlewares: []string{"buffered"}}},
	}
	g := testGatewayWithConfig(t, c)
	front := serveTestGateway(t, g, config.DefaultSettings())
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front, "http://"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /api HTTP/1.1\r\nHost: gateway\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-prefixWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not write its first response chunk")
	}
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	first := make([]byte, 1)
	if n, err := conn.Read(first); err == nil || n != 0 {
		t.Fatalf("buffer released response before backend completed: bytes=%d, err=%v", n, err)
	} else {
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("buffer read failed for an unexpected reason: %v", err)
		}
	}
	close(release)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	raw, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	response := string(raw)
	if !strings.HasPrefix(response, "HTTP/1.1 200 OK\r\n") || !strings.Contains(response, "prefix") || !strings.Contains(response, "suffix") {
		t.Fatalf("buffered response = %q", response)
	}
}

func TestBufferedRouteReturns504BeforeCommitment(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "prefix")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer backend.Close()

	settings := config.DefaultSettings()
	settings.Request.MaximumDuration = config.Duration(75 * time.Millisecond)
	settings.Server.WriteTimeout = config.Duration(500 * time.Millisecond)
	g := testGatewayWithConfig(t, config.Config{
		Listen: "127.0.0.1:8080", Settings: settings,
		Middlewares: map[string]config.Middleware{
			"buffered": {Buffer: &config.BufferSettings{MaxResponseBodyBytes: 64}},
		},
		Services: map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "api", PathPrefix: "/api", Service: "s", Middlewares: []string{"buffered"}}},
	})
	front := serveTestGateway(t, g, settings)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(front + "/api")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
}

func TestCORSMiddlewareWorksAtRouteAndServiceScope(t *testing.T) {
	for _, scope := range []string{"route", "service"} {
		t.Run(scope, func(t *testing.T) {
			var backendCalls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				backendCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, "backend")
			}))
			defer backend.Close()
			cors := config.Middleware{CORS: &config.CORSSettings{
				AllowOrigins:     []string{"https://app.example.com"},
				AllowMethods:     []string{"GET", "POST", "OPTIONS"},
				AllowHeaders:     []string{"Content-Type"},
				ExposeHeaders:    []string{"X-Request-ID"},
				AllowCredentials: true,
				MaxAgeSeconds:    600,
			}}
			service := config.Service{Upstreams: []string{backend.URL}}
			route := config.Route{Name: "api", PathPrefix: "/api", Service: "s"}
			if scope == "route" {
				route.Middlewares = []string{"browser"}
			} else {
				service.Middlewares = []string{"browser"}
			}
			g := testGatewayWithConfig(t, config.Config{
				Listen:      "127.0.0.1:8080",
				Middlewares: map[string]config.Middleware{"browser": cors},
				Services:    map[string]config.Service{"s": service},
				Routes:      []config.Route{route},
			})
			front := serveTestGateway(t, g, config.DefaultSettings())

			preflight, err := http.NewRequest(http.MethodOptions, front+"/api", nil)
			if err != nil {
				t.Fatal(err)
			}
			preflight.Header.Set("Origin", "https://app.example.com")
			preflight.Header.Set("Access-Control-Request-Method", "POST")
			preflight.Header.Set("Access-Control-Request-Headers", "Content-Type")
			response, err := http.DefaultClient.Do(preflight)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent || backendCalls.Load() != 0 {
				t.Fatalf("preflight status=%d backend calls=%d, want 204/0", response.StatusCode, backendCalls.Load())
			}

			request, err := http.NewRequest(http.MethodGet, front+"/api", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", "https://app.example.com")
			response, err = http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != http.StatusAccepted || string(body) != "backend" || backendCalls.Load() != 1 {
				t.Fatalf("response status=%d body=%q backend calls=%d", response.StatusCode, body, backendCalls.Load())
			}
			if got := response.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
				t.Fatalf("allow origin = %q", got)
			}
			if got := response.Header.Get("Access-Control-Expose-Headers"); got != "X-Request-ID" {
				t.Fatalf("expose headers = %q", got)
			}
		})
	}
}

func TestCORSPreflightMatchesRouteRequestedMethodAtEitherScope(t *testing.T) {
	for _, scope := range []string{"route", "service"} {
		t.Run(scope, func(t *testing.T) {
			var backendCalls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				backendCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer backend.Close()
			service := config.Service{Upstreams: []string{backend.URL}}
			route := config.Route{Name: "write-api", Match: "PathPrefix(`/api`) && Method(`POST`)", Service: "s"}
			if scope == "route" {
				route.Middlewares = []string{"browser"}
			} else {
				service.Middlewares = []string{"browser"}
			}
			g := testGatewayWithConfig(t, config.Config{
				Listen: "127.0.0.1:8080",
				Middlewares: map[string]config.Middleware{
					"browser": {CORS: &config.CORSSettings{
						AllowOrigins: []string{"https://app.example.com"},
						AllowMethods: []string{http.MethodPost},
					}},
				},
				Services: map[string]config.Service{"s": service},
				Routes:   []config.Route{route},
			})
			front := serveTestGateway(t, g, config.DefaultSettings())
			preflight, err := http.NewRequest(http.MethodOptions, front+"/api", nil)
			if err != nil {
				t.Fatal(err)
			}
			preflight.Header.Set("Origin", "https://app.example.com")
			preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
			response, err := http.DefaultClient.Do(preflight)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent || backendCalls.Load() != 0 {
				t.Fatalf("preflight status=%d backend calls=%d, want 204/0", response.StatusCode, backendCalls.Load())
			}
		})
	}
}

func TestBodyLimitRejectsKnownLengthBeforeBackend(t *testing.T) {
	called := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()
	g := testGatewayWithConfig(t, config.Config{
		Listen: "127.0.0.1:8080",
		Middlewares: map[string]config.Middleware{
			"upload-cap": {BodyLimit: &config.BodyLimitSettings{MaxBytes: 4}},
		},
		Services: map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "api", PathPrefix: "/api", Service: "s", Middlewares: []string{"upload-cap"}}},
	})
	front := serveTestGateway(t, g, config.DefaultSettings())
	req, err := http.NewRequest(http.MethodPost, front+"/api", strings.NewReader("12345"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	select {
	case <-called:
		t.Fatal("backend received a request for a known oversized body")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBodyLimitRejectsChunkedBodyWhileForwarding(t *testing.T) {
	backendStarted := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendStarted <- struct{}{}
		_, _ = io.ReadAll(r.Body)
	}))
	defer backend.Close()
	g := testGatewayWithConfig(t, config.Config{
		Listen: "127.0.0.1:8080",
		Middlewares: map[string]config.Middleware{
			"upload-cap": {BodyLimit: &config.BodyLimitSettings{MaxBytes: 4}},
		},
		Services: map[string]config.Service{"s": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "api", PathPrefix: "/api", Service: "s", Middlewares: []string{"upload-cap"}}},
	})
	front := serveTestGateway(t, g, config.DefaultSettings())
	req, err := http.NewRequest(http.MethodPost, front+"/api", io.NopCloser(strings.NewReader("12345")))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	select {
	case <-backendStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not observe the bounded chunked request")
	}
}

func TestServiceBodyLimitAppliesToAllRoutes(t *testing.T) {
	called := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()
	g := testGatewayWithConfig(t, config.Config{
		Listen: "127.0.0.1:8080",
		Middlewares: map[string]config.Middleware{
			"service-cap": {BodyLimit: &config.BodyLimitSettings{MaxBytes: 4}},
			"route-cap":   {BodyLimit: &config.BodyLimitSettings{MaxBytes: 8}},
		},
		Services: map[string]config.Service{
			"s": {Upstreams: []string{backend.URL}, Middlewares: []string{"service-cap"}},
		},
		Routes: []config.Route{
			{Name: "wide", PathPrefix: "/wide", Service: "s", Middlewares: []string{"route-cap"}},
			{Name: "plain", PathPrefix: "/plain", Service: "s"},
		},
	})
	front := serveTestGateway(t, g, config.DefaultSettings())
	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/wide", "/plain"} {
		resp, err := client.Post(front+path, "text/plain", strings.NewReader("12345"))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			resp.Body.Close()
			t.Fatalf("%s status = %d, want 413", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	select {
	case <-called:
		t.Fatal("service body limit allowed an oversized request to reach the backend")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestServiceAddPrefixRewritesPathBeforeProxying(t *testing.T) {
	seen := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	c := config.Config{
		Listen: "127.0.0.1:8080",
		Middlewares: map[string]config.Middleware{
			"internal-base": {AddPrefix: &config.AddPrefixSettings{Prefix: "/internal"}},
		},
		Services: map[string]config.Service{
			"s": {Upstreams: []string{backend.URL}, Middlewares: []string{"internal-base"}},
		},
		Routes: []config.Route{{Name: "api", PathPrefix: "/api", Service: "s"}},
	}
	g, err := New(c, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/api/a%2Fb", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	if got := <-seen; got != "/internal/api/a%2Fb" {
		t.Fatalf("backend escaped path = %q", got)
	}
}

func TestStripPrefixForwardsTrustedComposedPrefix(t *testing.T) {
	type observedRequest struct {
		path   string
		prefix string
	}
	seen := make(chan observedRequest, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- observedRequest{path: r.URL.EscapedPath(), prefix: r.Header.Get("X-Forwarded-Prefix")}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	c := config.Config{
		Listen: "127.0.0.1:8080",
		Middlewares: map[string]config.Middleware{
			"strip-api": {Scope: config.MiddlewareScopeRoute, StripPrefix: &config.StripPrefixSettings{Prefix: "/api"}},
			"strip-v1":  {Scope: config.MiddlewareScopeRoute, StripPrefix: &config.StripPrefixSettings{Prefix: "/v1"}},
		},
		Services: map[string]config.Service{
			"s": {Upstreams: []string{backend.URL}},
		},
		Routes: []config.Route{{Name: "api", PathPrefix: "/api", Service: "s", Middlewares: []string{"strip-api", "strip-v1"}}},
	}
	g, err := New(c, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	req := httptest.NewRequest(http.MethodGet, "http://gateway/api/v1/orders", nil)
	req.Header.Set("X-Forwarded-Prefix", "/spoofed")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	got := <-seen
	if got.path != "/orders" || got.prefix != "/api/v1" {
		t.Fatalf("backend path/prefix = %q/%q, want /orders//api/v1", got.path, got.prefix)
	}
}

func TestAllUnhealthyServiceReturns503WithoutForwarding(t *testing.T) {
	var userCalls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		userCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	g := testGatewayWithConfig(t, config.Config{
		Listen: "127.0.0.1:8080",
		Services: map[string]config.Service{
			"s": {
				Upstreams: []string{backend.URL},
				HealthCheck: &config.HealthCheckSettings{
					Path:               "/healthz",
					Interval:           config.Duration(20 * time.Millisecond),
					Timeout:            config.Duration(10 * time.Millisecond),
					UnhealthyThreshold: 1,
					HealthyThreshold:   1,
				},
			},
		},
		Routes: []config.Route{{Name: "api", PathPrefix: "/api", Service: "s"}},
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/api", nil))
		if w.Code == http.StatusServiceUnavailable {
			break
		}
		// Targets start eligible until the first probe completes. Ignore that
		// expected warm-up request and assert no forwarding after exclusion.
		userCalls.Store(0)
		if time.Now().After(deadline) {
			t.Fatalf("service never became unavailable; last status=%d", w.Code)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := userCalls.Load(); got != 0 {
		t.Fatalf("unhealthy service forwarded %d user requests", got)
	}
	snapshot := g.HealthSnapshot()
	if len(snapshot) != 1 || snapshot[0].Healthy {
		t.Fatalf("health snapshot = %+v, want one unhealthy target", snapshot)
	}
}
