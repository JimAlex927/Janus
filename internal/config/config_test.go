package config

import (
	"strings"
	"testing"
	"time"
)

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

func TestRouteProtocolConfig(t *testing.T) {
	base := `{"listen":"127.0.0.1:8080","services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/stream","protocols":["sse","websocket"],"service":"s"}]}`
	if _, err := Load(strings.NewReader(base)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"http3", "sse"} {
		body := base
		if value == "http3" {
			body = strings.Replace(body, `"sse","websocket"`, `"http3"`, 1)
		} else {
			body = strings.Replace(body, `"sse","websocket"`, `"`+value+`","`+value+`"`, 1)
		}
		if _, err := Load(strings.NewReader(body)); err == nil {
			t.Fatalf("expected invalid route protocol configuration for %q", value)
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

func TestAdminAddressMustBeLoopback(t *testing.T) {
	base := `{"listen":"127.0.0.1:8080","settings":{"admin":{"address":"127.0.0.1:9090"}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","path_prefix":"/api","service":"s"}]}`
	if _, err := Load(strings.NewReader(base)); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"0.0.0.0:9090", "localhost:9090", "127.0.0.1:0", "127.0.0.1:not-a-port"} {
		t.Run(address, func(t *testing.T) {
			body := strings.Replace(base, "127.0.0.1:9090", address, 1)
			if _, err := Load(strings.NewReader(body)); err == nil {
				t.Fatal("expected invalid admin address")
			}
		})
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
