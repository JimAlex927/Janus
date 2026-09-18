package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"janus/internal/config"
	"janus/internal/discovery"
	"janus/internal/telemetry"
)

func TestHealthEndpointsFollowState(t *testing.T) {
	state := NewState()
	h := NewHandler(state)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/livez", http.StatusOK},
		{"/readyz", http.StatusServiceUnavailable},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
		})
	}
	state.SetReady(true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Fatalf("ready response = %d %q", w.Code, w.Body.String())
	}
	state.SetLive(false)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-live status = %d, want 503", w.Code)
	}
}

func TestEmbeddedConsoleIsServed(t *testing.T) {
	h := NewHandler(NewState())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Janus Console") {
		t.Fatalf("console response = %d %q", w.Code, w.Body.String())
	}
}

func TestHealthEndpointsMethodAndHeadSemantics(t *testing.T) {
	h := NewHandler(NewState())
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/livez", nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST response = %d allow=%q", post.Code, post.Header().Get("Allow"))
	}
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/livez", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d body=%q", head.Code, head.Body.String())
	}
}

func TestMetricsEndpointUsesPrivateRegistry(t *testing.T) {
	state := NewState()
	metrics := telemetry.NewMetrics()
	metrics.RecordRequest(telemetry.Outcome{Route: "api", Service: "orders", Status: 200}, "", 5*time.Millisecond)
	h := NewHandlerWithMetrics(state, metrics, func() []telemetry.BackendHealth {
		return []telemetry.BackendHealth{{Service: "orders", Target: "0", Healthy: true}}
	})
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "janus_requests_total") || !strings.Contains(get.Body.String(), "janus_backend_health") {
		t.Fatalf("metrics response = %d %q", get.Code, get.Body.String())
	}
	if got := get.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("metrics content type = %q", got)
	}
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/metrics", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("metrics HEAD response = %d %q", head.Code, head.Body.String())
	}
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("metrics POST status = %d", post.Code)
	}
}

func TestAdminLoginProtectsConfigurationPublishing(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	current := config.Config{Settings: config.Settings{Admin: config.AdminSettings{Username: "admin", PasswordHash: string(hash)}}}
	published := false
	var publishedConfig config.Config
	h := NewHandlerWithOptions(Options{
		State: NewState(), Current: func() config.Config { return current }, Revision: func() uint64 { return 1 },
		Publish: func(candidate config.Config) error {
			published = true
			publishedConfig = candidate
			return candidate.Validate()
		},
	})
	body := `{"listen":"127.0.0.1:8080","settings":{"admin":{"username":"admin"}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/","service":"s"}]}`
	unauthorized := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/publish", bytes.NewBufferString(body))
	req.Header.Set("X-Janus-Revision", "1")
	h.ServeHTTP(unauthorized, req)
	if unauthorized.Code != http.StatusUnauthorized || published {
		t.Fatalf("unauthorized publish = %d, published=%v", unauthorized.Code, published)
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"Username":"admin","Password":"secret"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	authorized := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/config/publish", bytes.NewBufferString(body))
	req.Header.Set("X-Janus-Revision", "1")
	req.AddCookie(cookie)
	h.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusOK || !published || publishedConfig.Settings.Admin.PasswordHash != current.Settings.Admin.PasswordHash {
		t.Fatalf("authorized publish = %d, published=%v", authorized.Code, published)
	}
}

func TestDiscoveryEndpointRequiresAuthAndReturnsLocalSnapshot(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	current := config.Config{Settings: config.Settings{Admin: config.AdminSettings{Username: "admin", PasswordHash: string(hash)}}}
	h := NewHandlerWithOptions(Options{
		State: NewState(), Current: func() config.Config { return current }, Revision: func() uint64 { return 7 },
		Discovery: func() map[string]discovery.Status {
			return map[string]discovery.Status{"orders": {Instances: 2, Failed: false}}
		},
	})
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/discovery", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized discovery status = %d, want 401", unauthorized.Code)
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"Username":"admin","Password":"secret"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d", login.Code)
	}
	authorized := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/discovery", nil)
	req.AddCookie(login.Result().Cookies()[0])
	h.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusOK || !strings.Contains(authorized.Body.String(), `"orders"`) || !strings.Contains(authorized.Body.String(), `"instances":2`) {
		t.Fatalf("discovery response = %d %q", authorized.Code, authorized.Body.String())
	}
}

func TestRegistryHealthEndpointRequiresAuthAndReturnsProbe(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	current := config.Config{Settings: config.Settings{Admin: config.AdminSettings{Username: "admin", PasswordHash: string(hash)}}}
	h := NewHandlerWithOptions(Options{
		State: NewState(), Current: func() config.Config { return current },
		RegistryHealth: func(name string) discovery.RegistryHealth {
			return discovery.RegistryHealth{Registry: name, Healthy: true, LatencyMS: 4}
		},
	})
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/discovery/registries/health", bytes.NewBufferString(`{"name":"platform"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized health status = %d, want 401", unauthorized.Code)
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"Username":"admin","Password":"secret"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d", login.Code)
	}
	authorized := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/discovery/registries/health", bytes.NewBufferString(`{"name":"platform"}`))
	req.AddCookie(login.Result().Cookies()[0])
	h.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusOK || !strings.Contains(authorized.Body.String(), `"healthy":true`) || !strings.Contains(authorized.Body.String(), `"registry":"platform"`) {
		t.Fatalf("health response = %d %q", authorized.Code, authorized.Body.String())
	}
}

func TestConfigEndpointRedactsNacosPassword(t *testing.T) {
	current := config.Config{
		Discovery: config.DiscoveryConfig{Nacos: map[string]config.NacosRegistry{
			"platform": {Servers: []config.NacosServer{{Address: "127.0.0.1", Port: 8848}}, Username: "nacos", Password: "secret"},
		}},
	}
	h := NewHandlerWithOptions(Options{State: NewState(), Current: func() config.Config { return current }})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), `"password":"secret"`) {
		t.Fatalf("config response leaked Nacos password: %d %q", w.Code, w.Body.String())
	}
}
