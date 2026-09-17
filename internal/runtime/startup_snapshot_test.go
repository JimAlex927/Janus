package runtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"janus/internal/config"
	"janus/internal/middleware"

	"go.uber.org/zap"
)

func TestRegressionStartupChangeMustNotBeSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "janus.json")
	writeReloaderConfig(t, path, "first", "127.0.0.1:8080")
	c, startupHash, err := config.LoadFileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewWithBuilder(c, zap.NewNop(), func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		return &testGeneration{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, c.Routes[0].Name) }), closed: make(chan struct{})}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	writeReloaderConfig(t, path, "second", "127.0.0.1:8080")
	watcher, err := NewFileReloader(r, path, 0, nil, startupHash)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := watcher.ReloadOnce(); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "http://gateway/", nil))
	if w.Body.String() != "second" {
		t.Fatalf("disk=second, active=%q after three successful polls", w.Body.String())
	}
}
