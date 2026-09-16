package runtime

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/middleware"

	"go.uber.org/zap"
)

func TestFileReloaderPublishesChangedGenerationAndKeepsLastGood(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "janus.json")
	writeReloaderConfig(t, configPath, "first", "127.0.0.1:8080")
	built := 0
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		built++
		body := c.Routes[0].Name
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }),
			closed:  make(chan struct{}),
		}, nil
	}
	initial, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewWithBuilder(initial, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	reloader, err := NewFileReloader(r, configPath, time.Millisecond, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	writeReloaderConfig(t, configPath, "second", "127.0.0.1:8080")
	if err := reloader.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if w.Body.String() != "second" || built != 2 {
		t.Fatalf("reloaded response = %q, builds = %d", w.Body, built)
	}

	if err := os.WriteFile(configPath, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloader.ReloadOnce(); err == nil {
		t.Fatal("expected invalid configuration error")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if w.Body.String() != "second" || built != 2 {
		t.Fatalf("invalid reload changed active generation: body=%q builds=%d", w.Body, built)
	}
	if err := reloader.ReloadOnce(); err == nil {
		t.Fatal("expected unchanged invalid configuration error")
	}
	metrics := string(r.Metrics().Render(nil))
	if !strings.Contains(metrics, `janus_config_reload_total{result="success"} 1`) || !strings.Contains(metrics, `janus_config_reload_total{result="rejected"} 2`) {
		t.Fatalf("reload metrics = %s", metrics)
	}
}

func TestFileReloaderRejectsStartupChanges(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "janus.json")
	writeReloaderConfig(t, configPath, "first", "127.0.0.1:8080")
	c, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewWithBuilder(c, zap.NewNop(), func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, c.Routes[0].Name) }),
			closed:  make(chan struct{}),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	reloader, err := NewFileReloader(r, configPath, time.Millisecond, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	writeReloaderConfig(t, configPath, "second", "127.0.0.1:8081")
	if err := reloader.ReloadOnce(); !errors.Is(err, ErrStartupConfigChanged) {
		t.Fatalf("startup change error = %v, want %v", err, ErrStartupConfigChanged)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if w.Body.String() != "first" {
		t.Fatalf("startup-changing reload response = %q, want first", w.Body)
	}
}

func TestFileReloaderRetriesTransientReplacementFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "janus.json")
	writeReloaderConfig(t, configPath, "first", "127.0.0.1:8080")
	attempts := 0
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		attempts++
		if attempts == 2 {
			return nil, errors.New("transient candidate failure")
		}
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, c.Routes[0].Name) }),
			closed:  make(chan struct{}),
		}, nil
	}
	c, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewWithBuilder(c, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	reloader, err := NewFileReloader(r, configPath, time.Millisecond, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	writeReloaderConfig(t, configPath, "second", "127.0.0.1:8080")
	if err := reloader.ReloadOnce(); err == nil {
		t.Fatal("expected first candidate build to fail")
	}
	if err := reloader.ReloadOnce(); err != nil {
		t.Fatalf("transient candidate failure was not retried: %v", err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if w.Body.String() != "second" || attempts != 3 {
		t.Fatalf("retried reload response = %q, builder attempts = %d", w.Body, attempts)
	}
}

func TestFileReloaderSerializesConcurrentReloads(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "janus.json")
	writeReloaderConfig(t, configPath, "first", "127.0.0.1:8080")
	attempts := 0
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		attempts++
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, c.Routes[0].Name) }),
			closed:  make(chan struct{}),
		}, nil
	}
	c, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewWithBuilder(c, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	reloader, err := NewFileReloader(r, configPath, time.Millisecond, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	writeReloaderConfig(t, configPath, "second", "127.0.0.1:8080")

	results := make(chan error, 2)
	go func() { results <- reloader.ReloadOnce() }()
	go func() { results <- reloader.ReloadOnce() }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent reload failed: %v", err)
		}
	}
	if attempts != 2 {
		t.Fatalf("concurrent reload built %d candidate generations, want one", attempts-1)
	}
}

func writeReloaderConfig(t *testing.T, path, route, listen string) {
	t.Helper()
	body := `{"listen":"` + listen + `","services":{"service":{"upstreams":["http://127.0.0.1:9000"]}},"routes":[{"name":"` + route + `","path_prefix":"/","service":"service"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
