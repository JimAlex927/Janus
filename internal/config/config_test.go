package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComprehensiveExampleConfig(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "janus-comprehensive.example.json")
	c, _, err := LoadFileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Limens) != 2 || len(c.Routes) != 8 {
		t.Fatalf("loaded limens/routes = %d/%d, want 2/8", len(c.Limens), len(c.Routes))
	}
	if c.Routes[0].Match == "" || c.Routes[0].Action == nil || c.Routes[0].Action.Forward == nil {
		t.Fatal("comprehensive example did not load match and forward action")
	}
}

func TestAllExampleConfigsUseCurrentRouteShape(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no configuration examples found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			c, _, err := LoadFileSnapshot(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, route := range c.Routes {
				if route.Match == "" || route.Action == nil {
					t.Fatalf("route %q does not use match/action form", route.Name)
				}
			}
		})
	}
}

func FuzzLoadNeverPanics(f *testing.F) {
	f.Add([]byte(`{"listen":"127.0.0.1:8080","services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/","service":"s"}]}`))
	f.Add([]byte(`{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1"]}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","limen":"public","path_prefix":"/","service":"s"}]}`))
	f.Add([]byte(`{"listen":"127.0.0.1:8080","listen":"127.0.0.1:8081"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Load(bytes.NewReader(data))
	})
}

func TestEffectiveViewNormalizesLegacyConfigAndOmitsTLSAssets(t *testing.T) {
	body := `{"listen":"127.0.0.1:8080","settings":{"request":{"maximum_duration":"1s"}},"services":{"s":{"upstreams":["https://backend.internal:8443"]}},"routes":[{"name":"r","path_prefix":"/","service":"s"}]}`
	c, err := Load(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	view := c.EffectiveView()
	limen, ok := view.Limens["default"]
	if !ok || limen.Address != "127.0.0.1:8080" || len(limen.Protocols) != 1 || limen.Protocols[0] != ProtocolHTTP1 {
		t.Fatalf("legacy limen was not normalized: %+v", view.Limens)
	}
	if view.Version != CurrentConfigVersion || view.Settings.Server.WriteTimeout.Duration() != 6*time.Second {
		t.Fatalf("effective defaults = version %d, write timeout %s", view.Version, view.Settings.Server.WriteTimeout.Duration())
	}
	tlsConfig, err := Load(strings.NewReader(`{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1"],"tls":{"cert_file":"private/server.crt","key_file":"private/server.key"}}},"services":{"s":{"upstreams":["https://backend.internal:8443"]}},"routes":[{"name":"r","limen":"public","path_prefix":"/","service":"s"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(tlsConfig.EffectiveView())
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "cert_file") || strings.Contains(text, "key_file") {
		t.Fatalf("effective view exposed TLS asset fields: %s", text)
	}
}

func TestLoad(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"valid", valid, true},
		{"unknown field", strings.Replace(valid, `"listen"`, `"lissten"`, 1), false},
		{"duplicate top-level key", `{"listen":"127.0.0.1:8080","services":{"s":{"upstreams":["http://localhost:9000"]}},"services":{"s":{"upstreams":["http://localhost:9001"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`, false},
		{"duplicate nested key", `{"listen":"127.0.0.1:8080","services":{"s":{"upstreams":["http://localhost:9000"],"upstreams":["http://localhost:9001"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`, false},
		{"extra document", valid + ` {}`, false},
		{"missing service", strings.Replace(valid, `"service":"s"`, `"service":"missing"`, 1), false},
		{"empty pool", strings.Replace(valid, `["http://localhost:9000"]`, `[]`, 1), false},
		{"unsupported scheme", strings.Replace(valid, "http://", "file://", 1), false},
		{"credentials", strings.Replace(valid, "localhost:9000", "user:secret@localhost:9000", 1), false},
		{"upstream base path", strings.Replace(valid, "localhost:9000", "localhost:9000/base", 1), false},
		{"trailing slash", strings.Replace(valid, `"/api"`, `"/api/"`, 1), false},
		{"invalid port", strings.Replace(valid, "localhost:9000", "localhost:99999", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(tc.body))
			if (err == nil) != tc.ok {
				t.Fatalf("valid=%v, error=%v", tc.ok, err)
			}
		})
	}
}

func TestLoadRejectsOversizeInput(t *testing.T) {
	if _, err := Load(strings.NewReader(strings.Repeat("x", MaxConfigBytes+1))); err == nil {
		t.Fatal("expected oversized configuration rejection")
	}
}

func TestDuplicateRouteMatch(t *testing.T) {
	c := Config{Listen: "127.0.0.1:8080", Services: map[string]Service{"s": {Upstreams: []string{"http://localhost:9000"}}}, Routes: []Route{
		{Name: "a", Host: "EXAMPLE.com", PathPrefix: "/api", Service: "s"},
		{Name: "b", Host: "example.com", PathPrefix: "/api", Service: "s"},
	}}
	if c.Validate() == nil {
		t.Fatal("expected duplicate route rejection")
	}
}

func TestRouteMiddlewareConfig(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","middlewares":{"buffered":{"buffer":{"max_response_body_bytes":1024}}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s","middlewares":["buffered"]}]}`
	c, err := Load(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	if c.Middlewares["buffered"].Buffer.MaxResponseBodyBytes != 1024 || len(c.Routes[0].Middlewares) != 1 {
		t.Fatalf("middleware definition was not loaded: %+v", c)
	}
}

func TestRouteMiddlewareConfigRejectsBadReferences(t *testing.T) {
	base := `{"listen":"127.0.0.1:8080","middlewares":{"buffered":{"buffer":{"max_response_body_bytes":1024}}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s","middlewares":["buffered"]}]}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing reference", strings.Replace(base, `"buffered"]`, `"missing"]`, 1)},
		{"duplicate reference", strings.Replace(base, `"buffered"]`, `"buffered","buffered"]`, 1)},
		{"missing policy", strings.Replace(base, `{"buffered":{"buffer":{"max_response_body_bytes":1024}}}`, `{"empty":{}}`, 1)},
		{"invalid response limit", strings.Replace(base, `"max_response_body_bytes":1024`, `"max_response_body_bytes":0`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(tc.body)); err == nil {
				t.Fatal("expected configuration validation error")
			}
		})
	}
}

func TestServiceMiddlewareConfig(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","middlewares":{"cap":{"body_limit":{"max_bytes":1024}}},"services":{"s":{"upstreams":["http://localhost:9000"],"middlewares":["cap"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing reference", strings.Replace(valid, `"cap"]`, `"missing"]`, 1)},
		{"duplicate reference", strings.Replace(valid, `"cap"]`, `"cap","cap"]`, 1)},
		{"multiple policies", strings.Replace(valid, `"body_limit":{"max_bytes":1024}`, `"body_limit":{"max_bytes":1024},"buffer":{"max_response_body_bytes":1024}`, 1)},
		{"invalid body limit", strings.Replace(valid, `"max_bytes":1024`, `"max_bytes":0`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(tc.body)); err == nil {
				t.Fatal("expected configuration validation error")
			}
		})
	}
}

func TestMiddlewareScopeConfig(t *testing.T) {
	base := `{"listen":"127.0.0.1:8080","middlewares":{"policy":{"scope":"%s","body_limit":{"max_bytes":1024}}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s","middlewares":["policy"]}]}`
	if _, err := Load(strings.NewReader(fmt.Sprintf(base, MiddlewareScopeRoute))); err != nil {
		t.Fatalf("route-scoped middleware should load without an attachment: %v", err)
	}
	serviceScoped := fmt.Sprintf(base, MiddlewareScopeService)
	if _, err := Load(strings.NewReader(serviceScoped)); err == nil {
		t.Fatal("expected service-scoped middleware to be rejected when attached to a route")
	}
	invalidScope := strings.Replace(fmt.Sprintf(base, MiddlewareScopeRoute), `"scope":"route"`, `"scope":"admin"`, 1)
	if _, err := Load(strings.NewReader(invalidScope)); err == nil {
		t.Fatal("expected unsupported middleware scope to be rejected")
	}
}

func TestServiceHealthCheckConfigAndDefaults(t *testing.T) {
	body := `{"listen":"127.0.0.1:8080","services":{"s":{"upstreams":["http://localhost:9000"],"health_check":{"path":"/healthz","jitter":"250ms","unhealthy_threshold":3,"healthy_threshold":2,"expected_status":204}}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	c, err := Load(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	check := c.Services["s"].HealthCheck
	if check == nil || check.Interval.Duration() != 30*time.Second || check.Timeout.Duration() != 5*time.Second || check.Jitter.Duration() != 250*time.Millisecond || check.UnhealthyThreshold != 3 || check.HealthyThreshold != 2 || check.ExpectedStatus != 204 {
		t.Fatalf("health check defaults/configuration = %+v", check)
	}

	base := `{"listen":"127.0.0.1:8080","services":{"s":{"upstreams":["http://localhost:9000"],"health_check":{"path":"/healthz"}}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing path", strings.Replace(base, `"path":"/healthz"`, `"path":""`, 1)},
		{"timeout exceeds interval", strings.Replace(base, `"path":"/healthz"`, `"path":"/healthz","interval":"1s","timeout":"2s"`, 1)},
		{"jitter exceeds interval", strings.Replace(base, `"path":"/healthz"`, `"path":"/healthz","interval":"1s","jitter":"2s"`, 1)},
		{"invalid threshold", strings.Replace(base, `"path":"/healthz"`, `"path":"/healthz","unhealthy_threshold":101`, 1)},
		{"invalid status", strings.Replace(base, `"path":"/healthz"`, `"path":"/healthz","expected_status":199`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(tc.body)); err == nil {
				t.Fatal("expected health check validation error")
			}
		})
	}
}

func TestInFlightMiddlewareConfig(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","middlewares":{"cap":{"in_flight":{"max_concurrent":2}}},"services":{"s":{"upstreams":["http://localhost:9000"],"middlewares":["cap"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	routeScope := strings.Replace(valid, `"path_prefix":"/api","service":"s"`, `"path_prefix":"/api","service":"s","middlewares":["cap"]`, 1)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"route scope", routeScope},
		{"duplicate service reference", strings.Replace(valid, `"cap"]}},"routes"`, `"cap","cap"]}},"routes"`, 1)},
		{"invalid limit", strings.Replace(valid, `"max_concurrent":2`, `"max_concurrent":0`, 1)},
		{"multiple policies", strings.Replace(valid, `"in_flight":{"max_concurrent":2}`, `"in_flight":{"max_concurrent":2},"buffer":{"max_response_body_bytes":1024}`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(tc.body)); err == nil {
				t.Fatal("expected configuration validation error")
			}
		})
	}
}

func TestHeadersAndStripPrefixMiddlewareConfig(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","middlewares":{"headers":{"headers":{"request_set":{"X-Tenant":"one"},"response_remove":["Server"]}},"strip":{"scope":"route","strip_prefix":{"prefix":"/api"}}},"services":{"s":{"upstreams":["http://localhost:9000"],"middlewares":["headers"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s","middlewares":["strip"]}]}`
	if _, err := Load(strings.NewReader(valid)); err != nil {
		t.Fatalf("valid headers/strip_prefix config failed: %v", err)
	}
	for _, replacement := range []string{
		`"X-Tenant":"bad\nvalue"`,
		`"Content-Length":"1"`,
		`"prefix":"api"`,
		`"scope":"service","strip_prefix"`,
	} {
		candidate := valid
		switch replacement {
		case `"X-Tenant":"bad\nvalue"`:
			candidate = strings.Replace(candidate, `"X-Tenant":"one"`, replacement, 1)
		case `"Content-Length":"1"`:
			candidate = strings.Replace(candidate, `"X-Tenant":"one"`, replacement, 1)
		case `"prefix":"api"`:
			candidate = strings.Replace(candidate, `"prefix":"/api"`, replacement, 1)
		default:
			candidate = strings.Replace(candidate, `"scope":"route","strip_prefix"`, replacement, 1)
		}
		if _, err := Load(strings.NewReader(candidate)); err == nil {
			t.Fatalf("expected invalid middleware config for replacement %s", replacement)
		}
	}
}

func TestCORSMiddlewareConfig(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","middlewares":{"browser":{"cors":{"allow_origins":["https://app.example.com"],"allow_methods":["GET","POST","OPTIONS"],"allow_headers":["Content-Type"],"allow_credentials":true,"max_age_seconds":600}}},"services":{"s":{"upstreams":["http://localhost:9000"],"middlewares":["browser"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s","middlewares":["browser"]}]}`
	if _, err := Load(strings.NewReader(valid)); err != nil {
		t.Fatalf("valid cors configuration failed: %v", err)
	}
	for _, replacement := range []string{
		`"allow_origins":[]`,
		`"allow_origins":["*"],"allow_credentials":true`,
		`"allow_origins":["https://app.example.com/path"]`,
		`"allow_methods":["get"]`,
		`"allow_headers":["Content Type"]`,
		`"max_age_seconds":86401`,
	} {
		candidate := valid
		switch replacement {
		case `"allow_origins":[]`:
			candidate = strings.Replace(candidate, `"allow_origins":["https://app.example.com"]`, replacement, 1)
		case `"allow_origins":["*"],"allow_credentials":true`:
			candidate = strings.Replace(candidate, `"allow_origins":["https://app.example.com"],"allow_methods"`, replacement+`,"allow_methods"`, 1)
		case `"allow_origins":["https://app.example.com/path"]`:
			candidate = strings.Replace(candidate, `"allow_origins":["https://app.example.com"]`, replacement, 1)
		case `"allow_methods":["get"]`:
			candidate = strings.Replace(candidate, `"allow_methods":["GET","POST","OPTIONS"]`, replacement, 1)
		case `"allow_headers":["Content Type"]`:
			candidate = strings.Replace(candidate, `"allow_headers":["Content-Type"]`, replacement, 1)
		case `"max_age_seconds":86401`:
			candidate = strings.Replace(candidate, `"max_age_seconds":600`, replacement, 1)
		}
		if _, err := Load(strings.NewReader(candidate)); err == nil {
			t.Fatalf("accepted invalid cors configuration %s", replacement)
		}
	}
}

func TestMiddlewareCapabilitiesIncludeCORS(t *testing.T) {
	for _, capability := range MiddlewareCapabilities() {
		if capability.Type != "cors" {
			continue
		}
		if len(capability.Scopes) != 2 || capability.Scopes[0] != MiddlewareScopeRoute || capability.Scopes[1] != MiddlewareScopeService {
			t.Fatalf("cors scopes = %#v, want route and service", capability.Scopes)
		}
		if len(capability.Fields) != 6 || capability.Fields[0].Name != "allow_origins" || capability.Fields[5].Name != "max_age_seconds" {
			t.Fatalf("cors fields = %#v, want stable dynamic-form contract", capability.Fields)
		}
		return
	}
	t.Fatal("middleware capability catalog does not expose cors")
}

func TestAddPrefixMiddlewareConfigAndScopes(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","middlewares":{"base":{"add_prefix":{"prefix":"/internal"}}},"services":{"s":{"upstreams":["http://localhost:9000"],"middlewares":["base"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(valid)); err != nil {
		t.Fatalf("valid add_prefix config failed: %v", err)
	}
	invalid := strings.Replace(valid, `"prefix":"/internal"`, `"prefix":"internal/"`, 1)
	if _, err := Load(strings.NewReader(invalid)); err == nil {
		t.Fatal("expected invalid add_prefix path to be rejected")
	}
}

func TestRouteProtocolMatchConfig(t *testing.T) {
	base := "{\"listen\":\"127.0.0.1:8080\",\"services\":{\"s\":{\"upstreams\":[\"http://localhost:9000\"]}},\"routes\":[{\"name\":\"r\",\"match\":\"PathPrefix(`/stream`) && Protocol(`sse`)\",\"service\":\"s\"}]}"
	if _, err := Load(strings.NewReader(base)); err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{"PathPrefix(`/stream`) && Protocol(`http3`)", "PathPrefix(`/stream`) && Protocol()"} {
		body := strings.Replace(base, "PathPrefix(`/stream`) && Protocol(`sse`)", expression, 1)
		if _, err := Load(strings.NewReader(body)); err == nil {
			t.Fatalf("expected invalid Protocol expression %q", expression)
		}
	}
}

func TestVersionedLimenConfig(t *testing.T) {
	body := `{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1","http2"],"tls":{"cert_file":"server.crt","key_file":"server.key","min_version":"1.3"}}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","limen":"public","path_prefix":"/api","service":"s"}]}`
	c, err := Load(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	binding := c.LimenBindings()["public"]
	if binding.Address != "127.0.0.1:8443" || len(binding.Protocols) != 2 || binding.TLS == nil || binding.TLS.MinVersion != "1.3" {
		t.Fatalf("versioned limen was not loaded: %+v", binding)
	}
}

func TestTrustedProxyConfigIsPerLimenAndCanonicalized(t *testing.T) {
	body := `{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1"],"trusted_proxies":["10.0.0.0/8","2001:db8::/32"]}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","limen":"public","path_prefix":"/api","service":"s"}]}`
	c, err := Load(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := c.LimenBindings()["public"].TrustedProxies
	if len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "2001:db8::/32" {
		t.Fatalf("trusted proxy CIDRs = %#v", got)
	}
	legacy := `{"listen":"127.0.0.1:8080","trusted_proxies":["127.0.0.0/8"],"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(legacy)); err != nil {
		t.Fatalf("legacy trusted proxy config rejected: %v", err)
	}

	for _, bad := range []string{
		strings.Replace(body, `"10.0.0.0/8","2001:db8::/32"`, `"10.0.0.1"`, 1),
		strings.Replace(body, `"10.0.0.0/8","2001:db8::/32"`, `"10.0.0.0/8","10.0.0.0/8"`, 1),
		strings.Replace(body, `"limen":"public"`, `"limen":"missing"`, 1),
	} {
		if _, err := Load(strings.NewReader(bad)); err == nil {
			t.Fatal("expected invalid trusted proxy configuration")
		}
	}
}

func TestVersionedLimenConfigRejectsInvalidBindings(t *testing.T) {
	base := `{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1"]}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","limen":"public","path_prefix":"/api","service":"s"}]}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"http2 without tls", strings.Replace(base, `["http1"]`, `["http2"]`, 1)},
		{"h2c with tls", strings.Replace(strings.Replace(base, `["http1"]`, `["h2c"]`, 1), `"protocols":["h2c"]`, `"protocols":["h2c"],"tls":{"cert_file":"server.crt","key_file":"server.key"}`, 1)},
		{"h2 and h2c together", strings.Replace(base, `["http1"]`, `["http2","h2c"]`, 1)},
		{"unknown protocol", strings.Replace(base, `["http1"]`, `["tcp"]`, 1)},
		{"http3 without TLS", strings.Replace(base, `["http1"]`, `["http1","http3"]`, 1)},
		{"http3 without TCP fallback", strings.Replace(base, `["http1"]`, `["http3"]`, 1)},
		{"mixed legacy syntax", strings.Replace(base, `{"version":1`, `{"version":1,"listen":"127.0.0.1:8080"`, 1)},
		{"missing route limen with multiple bindings", strings.Replace(base, `"limen":"public"`, `"limen":"missing"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(tc.body)); err == nil {
				t.Fatal("expected configuration validation error")
			}
		})
	}
}

func TestVersionedLimenConfigAcceptsH2C(t *testing.T) {
	body := `{"version":1,"limens":{"internal":{"address":"127.0.0.1:8080","protocols":["http1","h2c"]}} ,"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","limen":"internal","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
}

func TestVersionedLimenConfigAcceptsHTTP3WithTLSAndTCPFallback(t *testing.T) {
	body := `{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1","http3"],"tls":{"cert_file":"server.crt","key_file":"server.key"},"http3":{"max_concurrent_streams":7}}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","limen":"public","path_prefix":"/api","service":"s"}]}`
	c, err := Load(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LimenBindings()["public"].HTTP3.MaxConcurrentStreams; got != 7 {
		t.Fatalf("HTTP/3 max concurrent streams = %d, want 7", got)
	}
	for _, bad := range []string{
		strings.Replace(body, `"max_concurrent_streams":7`, `"max_concurrent_streams":-1`, 1),
		strings.Replace(body, `"max_concurrent_streams":7`, `"max_concurrent_streams":1000001`, 1),
		strings.Replace(body, `"http1","http3"`, `"http1"`, 1),
	} {
		if _, err := Load(strings.NewReader(bad)); err == nil {
			t.Fatal("expected invalid HTTP/3 setting")
		}
	}
}

func TestVersionedSingleLimenTreatsOmittedBindingAsDefaultScope(t *testing.T) {
	body := `{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1"]}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"a","path_prefix":"/api","service":"s"},{"name":"b","limen":"public","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(body)); err == nil {
		t.Fatal("expected duplicate route rejection")
	}
}

func TestSettingsDurationSyntaxAndDefaults(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8080","settings":{"request":{"maximum_duration":"750ms"}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	c, err := Load(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Settings.Request.MaximumDuration.Duration(); got != 750*time.Millisecond {
		t.Fatalf("maximum duration = %s, want 750ms", got)
	}
	if got := c.Settings.Request.ReadTimeout.Duration(); got != 30*time.Second {
		t.Fatalf("read timeout = %s, want 30s default", got)
	}
	if got := c.Settings.Server.WriteTimeout.Duration(); got != 5*time.Second+750*time.Millisecond {
		t.Fatalf("write timeout = %s, want overall timeout plus 5s headroom", got)
	}
	if got := c.Settings.Backend.ConnectTimeout.Duration(); got != 3*time.Second {
		t.Fatalf("connect timeout = %s, want 3s default", got)
	}
	if c.Settings.Request.MaxInFlight != DefaultGlobalInFlight {
		t.Fatalf("max in-flight = %d, want %d default", c.Settings.Request.MaxInFlight, DefaultGlobalInFlight)
	}
	if got := c.Settings.Stream.MaxDuration.Duration(); got != DefaultStreamMaxDuration {
		t.Fatalf("stream max duration = %s, want %s", got, DefaultStreamMaxDuration)
	}
	if got := c.Settings.Stream.IdleTimeout.Duration(); got != DefaultStreamIdleTimeout {
		t.Fatalf("stream idle timeout = %s, want %s", got, DefaultStreamIdleTimeout)
	}
	if got := c.Settings.Shutdown.DrainTimeout.Duration(); got != 5*time.Second+750*time.Millisecond {
		t.Fatalf("drain timeout = %s, want server write timeout", got)
	}
}

func TestMaximumOverallDerivesValidWriteHeadroom(t *testing.T) {
	settings := Settings{Request: RequestSettings{MaximumDuration: Duration(MaxSettingDuration)}}.WithDefaults()
	if got, want := settings.Server.WriteTimeout.Duration(), MaxServerWriteTimeout; got != want {
		t.Fatalf("write timeout = %s, want %s", got, want)
	}
	if err := settings.Validate(); err != nil {
		t.Fatalf("maximum overall with derived write timeout is invalid: %v", err)
	}
}

func TestSettingsRejectInvalidBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Settings)
	}{
		{"read timeout below minimum", func(s *Settings) {
			s.Request.ReadTimeout = Duration(time.Microsecond)
		}},
		{"negative duration", func(s *Settings) {
			s.Request.MaximumDuration = Duration(-time.Second)
		}},
		{"write timeout does not leave headroom", func(s *Settings) {
			s.Server.WriteTimeout = s.Request.MaximumDuration
		}},
		{"header bytes too large", func(s *Settings) {
			s.Server.MaxHeaderBytes = MaxHeaderBytes + 1
		}},
		{"global admission too large", func(s *Settings) {
			s.Request.MaxInFlight = MaxGlobalInFlight + 1
		}},
		{"stream idle exceeds lifetime", func(s *Settings) {
			s.Stream.IdleTimeout = s.Stream.MaxDuration + Duration(time.Second)
		}},
		{"stream lifetime below minimum", func(s *Settings) {
			s.Stream.MaxDuration = Duration(time.Microsecond)
		}},
		{"drain shorter than write", func(s *Settings) {
			s.Shutdown.DrainTimeout = Duration(s.Server.WriteTimeout.Duration() - time.Millisecond)
		}},
		{"removal delay exceeds drain", func(s *Settings) {
			s.Shutdown.LoadBalancerRemovalDelay = Duration(s.Shutdown.DrainTimeout.Duration() + time.Second)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := DefaultSettings()
			tc.edit(&settings)
			if err := settings.Validate(); err == nil {
				t.Fatal("expected settings validation error")
			}
		})
	}
}

func TestAdminAddressMustBeLoopbackOrPrivate(t *testing.T) {
	base := `{"listen":"127.0.0.1:8080","settings":{"admin":{"address":"127.0.0.1:9090"}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(base)); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"10.0.0.5:9090", "192.168.1.5:9090", "172.16.1.5:9090", "0.0.0.0:9090", "localhost:9090", "127.0.0.1:0", "127.0.0.1:not-a-port"} {
		t.Run(address, func(t *testing.T) {
			body := strings.Replace(base, "127.0.0.1:9090", address, 1)
			valid := strings.HasPrefix(address, "10.") || strings.HasPrefix(address, "192.168.") || strings.HasPrefix(address, "172.16.")
			if _, err := Load(strings.NewReader(body)); (err == nil) != valid {
				t.Fatal("expected invalid admin address")
			}
		})
	}
}

func TestAdminCredentialsMustBeCompleteBcryptHash(t *testing.T) {
	base := `{"listen":"127.0.0.1:8080","settings":{"admin":{"address":"10.0.0.5:9090","username":"admin","password_hash":"$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/","service":"s"}]}`
	if _, err := Load(strings.NewReader(base)); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{`"username":""`, `"password_hash":""`, `"password_hash":"plain-text"`} {
		body := strings.Replace(base, `"username":"admin"`, replacement, 1)
		if strings.Contains(replacement, "password_hash") {
			body = strings.Replace(base, `"password_hash":"$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"`, replacement, 1)
		}
		if _, err := Load(strings.NewReader(body)); err == nil {
			t.Fatalf("expected invalid admin credentials for %s", replacement)
		}
	}
}

func TestShutdownRemovalDelayUsesBoundedGraceBudget(t *testing.T) {
	body := `{"listen":"127.0.0.1:8080","settings":{"request":{"maximum_duration":"1s"},"server":{"write_timeout":"2s"},"shutdown":{"drain_timeout":"5s","load_balancer_removal_delay":"1s"}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	c, err := Load(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Settings.Shutdown.LoadBalancerRemovalDelay.Duration(); got != time.Second {
		t.Fatalf("removal delay = %s, want 1s", got)
	}
}
