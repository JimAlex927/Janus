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

func TestVersionedLimenConfigRejectsInvalidBindings(t *testing.T) {
	base := `{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1"]}},"services":{"s":{"upstreams":["http://localhost:9000"]}},"routes":[{"name":"r","limen":"public","path_prefix":"/api","service":"s"}]}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"http2 without tls", strings.Replace(base, `["http1"]`, `["http2"]`, 1)},
		{"unknown protocol", strings.Replace(base, `["http1"]`, `["http3"]`, 1)},
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
