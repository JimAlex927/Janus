package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"janus/internal/config"
	"janus/internal/discovery"
	janusruntime "janus/internal/runtime"
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
	testConsoleMount(t, "/")
}

func TestEmbeddedConsoleCanMountBelowBuildBaseURL(t *testing.T) {
	for _, base := range []string{"/janus", "/ops/janus/"} {
		t.Run(base, func(t *testing.T) { testConsoleMount(t, base) })
	}
}

func testConsoleMount(t *testing.T, base string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithOptions(Options{
		State: NewState(), UIBaseURL: base,
		Subscribe: func() (<-chan janusruntime.Event, func()) {
			ch := make(chan janusruntime.Event)
			close(ch)
			return ch, func() {}
		},
		Current: func() config.Config {
			return config.Config{Settings: config.Settings{Admin: config.AdminSettings{Username: "admin", PasswordHash: string(hash)}}}
		},
	})
	mount := strings.TrimRight(base, "/") + "/"
	if mount != "/" {
		redirect := httptest.NewRecorder()
		h.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, strings.TrimRight(base, "/"), nil))
		if redirect.Code != http.StatusTemporaryRedirect || redirect.Header().Get("Location") != mount {
			t.Fatalf("base redirect = %d %q", redirect.Code, redirect.Header().Get("Location"))
		}
	}
	for _, pagePath := range []string{mount, mount + "index.html"} {
		page := httptest.NewRecorder()
		pageURL, _ := url.Parse("http://console.test" + pagePath)
		h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, pageURL.String(), nil))
		if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("console = %d %q", page.Code, page.Body.String())
		}
		baseTag := regexp.MustCompile(`<base href="([^"]+)">`).FindStringSubmatch(page.Body.String())
		if len(baseTag) != 2 || baseTag[1] != mount {
			t.Fatalf("base tag = %v", baseTag)
		}
		baseRef, _ := url.Parse(baseTag[1])
		documentBase := pageURL.ResolveReference(baseRef)
		assets := regexp.MustCompile(`(?:src|href)="([^"]*assets/[^"]+)"`).FindAllStringSubmatch(page.Body.String(), -1)
		if len(assets) < 2 {
			t.Fatal("console is missing JS/CSS references")
		}
		for _, match := range assets {
			ref, err := url.Parse(match[1])
			if err != nil {
				t.Fatal(err)
			}
			// Browser URL resolution; never manually prepend the expected mount.
			assetURL := documentBase.ResolveReference(ref)
			asset := httptest.NewRecorder()
			h.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, assetURL.String(), nil))
			if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
				t.Fatalf("asset %s = %d, length %d", assetURL, asset.Code, asset.Body.Len())
			}
		}
	}
	for _, endpoint := range []string{"config", "events"} {
		api := httptest.NewRecorder()
		h.ServeHTTP(api, httptest.NewRequest(http.MethodGet, mount+"api/v1/"+endpoint, nil))
		if api.Code != http.StatusUnauthorized {
			t.Fatalf("%s = %d, want 401", endpoint, api.Code)
		}
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest(http.MethodPost, mount+"api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret"}`)))
	cookies := login.Result().Cookies()
	if login.Code != http.StatusOK || len(cookies) != 1 || cookies[0].Path != mount {
		t.Fatalf("login = %d, cookies %v", login.Code, cookies)
	}
	api := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, mount+"api/v1/config", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(api, req)
	if api.Code != http.StatusOK {
		t.Fatalf("authenticated config = %d", api.Code)
	}
	events := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, mount+"api/v1/events", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(events, req)
	if events.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(events.Body.String(), "event: ready") {
		t.Fatalf("mounted events = %d %q", events.Code, events.Body.String())
	}
	logout := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, mount+"api/v1/auth/logout", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(logout, req)
	cleared := logout.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Path != mount || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout did not clear mounted cookie: %v", cleared)
	}
	api = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, mount+"api/v1/config", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(api, req)
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out config = %d", api.Code)
	}
}

func TestExplicitRootOverridesCompiledConsoleMount(t *testing.T) {
	previous := uiBaseURL
	uiBaseURL = "/compiled"
	t.Cleanup(func() { uiBaseURL = previous })
	testConsoleMount(t, "/")
	page := httptest.NewRecorder()
	NewHandler(NewState()).ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compiled/", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<base href="/compiled/">`) {
		t.Fatalf("compiled default = %d %q", page.Code, page.Body.String())
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
		Publish: func(candidate config.Config, revision uint64) error {
			if revision != 1 {
				t.Fatalf("publish revision = %d, want 1", revision)
			}
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

func TestSaveSettingsInheritsRedactedAdminPasswordHash(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	current := config.Config{
		Version: config.CurrentConfigVersion,
		Limens: map[string]config.LimenConfig{
			"public": {Address: "127.0.0.1:8080", Protocols: []string{config.ProtocolHTTP1}},
		},
		Settings: config.Settings{Admin: config.AdminSettings{
			Address: "127.0.0.1:9090", Username: "admin", PasswordHash: string(hash),
		}},
		Routes: []config.Route{{
			Name:   "health",
			Match:  "Path(`/healthz`)",
			Action: &config.RouteAction{Respond: &config.RespondAction{Status: http.StatusOK}},
		}},
	}
	var saved config.Config
	h := NewHandlerWithOptions(Options{
		State: NewState(), Current: func() config.Config { return current },
		SaveActive: func(candidate config.Config) error {
			saved = candidate
			return nil
		},
	})
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"Username":"admin","Password":"secret"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d", login.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/config/settings", bytes.NewBufferString(`{"request":{"maximum_duration":"45s"},"admin":{"address":"127.0.0.1:9090","username":"admin"}}`))
	request.AddCookie(login.Result().Cookies()[0])
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save settings status = %d, body = %q", response.Code, response.Body.String())
	}
	if saved.Settings.Admin.PasswordHash != current.Settings.Admin.PasswordHash {
		t.Fatalf("saved admin password hash = %q, want active hash", saved.Settings.Admin.PasswordHash)
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
	var received *config.NacosRegistry
	h := NewHandlerWithOptions(Options{
		State: NewState(), Current: func() config.Config { return current },
		RegistryHealth: func(name string, registry *config.NacosRegistry) discovery.RegistryHealth {
			received = registry
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/discovery/registries/health", bytes.NewBufferString(`{"name":"platform","registry":{"servers":[{"address":"192.168.20.69","port":8848}],"username":"nacos"}}`))
	req.AddCookie(login.Result().Cookies()[0])
	h.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusOK || received == nil || received.Servers[0].Address != "192.168.20.69" || !strings.Contains(authorized.Body.String(), `"healthy":true`) || !strings.Contains(authorized.Body.String(), `"registry":"platform"`) {
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

func TestMiddlewareCapabilitiesEndpointUsesBackendCatalog(t *testing.T) {
	h := NewHandlerWithOptions(Options{State: NewState(), Current: func() config.Config { return config.Config{} }})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities/middlewares", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, body = %q", w.Code, w.Body.String())
	}
	for _, expected := range []string{`"type":"buffer"`, `"type":"headers"`, `"type":"cors"`, `"type":"strip_prefix"`, `"type":"add_prefix"`, `"kind":"string_map"`, `"name":"allow_origins"`} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("capabilities response missing %s: %s", expected, w.Body.String())
		}
	}
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/v1/capabilities/middlewares", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("capabilities POST status = %d, want 405", post.Code)
	}
}
