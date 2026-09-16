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
	if got := c.Settings.Backend.ConnectTimeout.Duration(); got != 3*time.Second {
		t.Fatalf("connect timeout = %s, want 3s default", got)
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
