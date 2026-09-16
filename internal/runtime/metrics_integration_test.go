package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"janus/internal/config"
	"janus/internal/middleware"

	"go.uber.org/zap"
)

func TestRuntimePublishesRequestAndInFlightMetrics(t *testing.T) {
	r, err := NewWithBuilder(validRuntimeConfig("api"), zap.NewNop(), func(_ config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }),
			closed:  make(chan struct{}),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	output := string(r.Metrics().Render(nil))
	if !strings.Contains(output, `janus_requests_total{route="",service="",status="201"} 1`) || !strings.Contains(output, `janus_in_flight_requests{scope="global",service=""} 0`) {
		t.Fatalf("runtime metrics = %s", output)
	}
}
