package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"janus/internal/config"
)

func TestDirectRouteActions(t *testing.T) {
	g, err := New(config.Config{
		Listen: "127.0.0.1:8080",
		Routes: []config.Route{
			{
				Name:  "health",
				Match: "Path(`/healthz`) && Method(`GET`)",
				Action: &config.RouteAction{Respond: &config.RespondAction{
					Status:  http.StatusOK,
					Body:    "ok\n",
					Headers: map[string]string{"Content-Type": "text/plain"},
				}},
			},
			{
				Name:  "redirect",
				Match: "Path(`/old`)",
				Action: &config.RouteAction{Redirect: &config.RedirectAction{
					Status:   http.StatusPermanentRedirect,
					Location: "/new",
				}},
			},
		},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	response := httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" || response.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("respond action = %d %q %#v", response.Code, response.Body.String(), response.Header())
	}

	response = httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/old", nil))
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "/new" {
		t.Fatalf("redirect action = %d location %q", response.Code, response.Header().Get("Location"))
	}
}
