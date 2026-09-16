package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"janus/internal/config"
)

func testGateway(t *testing.T, upstream string) *Gateway {
	t.Helper()
	g, err := New(config.Config{Listen: "127.0.0.1:8080", Services: map[string]config.Service{"s": {Upstreams: []string{upstream}}}, Routes: []config.Route{{Name: "api", PathPrefix: "/api", Service: "s"}}}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

func TestForwardingOverRealConnections(t *testing.T) {
	type observed struct{ uri, host, body, xff, proto, originalHost, forwarded, realIP, forwardedPort, hop string }
	seen := make(chan observed, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- observed{r.RequestURI, r.Host, string(body), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host"), r.Header.Get("Forwarded"), r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-Port"), r.Header.Get("X-Hop")}
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
	for k, v := range map[string]string{"Connection": "X-Hop", "X-Hop": "private", "X-Forwarded-For": "203.0.113.99", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "spoof.example", "X-Forwarded-Port": "443", "Forwarded": "for=spoof", "X-Real-IP": "spoof"} {
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
	if got.xff != "127.0.0.1" || got.proto != "http" || got.originalHost != "client.example" || got.forwarded != "" || got.realIP != "" || got.forwardedPort != "" || got.hop != "" {
		t.Fatalf("untrusted headers propagated: %+v", got)
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

func TestUnsupportedTunnelAndUpgrade(t *testing.T) {
	g := testGateway(t, "http://127.0.0.1:9000")
	for _, method := range []string{"CONNECT", "GET"} {
		r := httptest.NewRequest(method, "http://gateway/api", nil)
		if method == "GET" {
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Connection", "Upgrade")
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 501 {
			t.Fatalf("%s: got %d", method, w.Code)
		}
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
	settings.Request.NormalDuration = config.Duration(10 * time.Millisecond)
	settings.Request.MaximumDuration = config.Duration(50 * time.Millisecond)
	settings.SLO.AcceptableP99Latency = config.Duration(5 * time.Millisecond)
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
