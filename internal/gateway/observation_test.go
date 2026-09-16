package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"janus/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestAccessObservationCoversEarlyAndUpstreamErrors(t *testing.T) {
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hanging.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	badUpstream := closed.URL
	closed.Close()

	core, logs := observer.New(zap.InfoLevel)
	settings := config.DefaultSettings()
	settings.Request.MaximumDuration = config.Duration(50 * time.Millisecond)
	settings.Server.WriteTimeout = config.Duration(500 * time.Millisecond)
	g, err := New(config.Config{
		Listen:   "127.0.0.1:8080",
		Settings: settings,
		Middlewares: map[string]config.Middleware{
			"body-cap": {BodyLimit: &config.BodyLimitSettings{MaxBytes: 4}},
		},
		Services: map[string]config.Service{
			"ok":  {Upstreams: []string{hanging.URL}},
			"bad": {Upstreams: []string{badUpstream}},
		},
		Routes: []config.Route{
			{Name: "limited", PathPrefix: "/limited", Service: "ok", Middlewares: []string{"body-cap"}},
			{Name: "bad", PathPrefix: "/bad", Service: "bad"},
			{Name: "timeout", PathPrefix: "/timeout", Service: "ok"},
		},
	}, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	cases := []struct {
		name       string
		method     string
		target     string
		body       string
		status     int
		errorClass string
		route      string
	}{
		{name: "not found", method: http.MethodGet, target: "/missing?secret=redacted", status: http.StatusNotFound, errorClass: "route_not_found"},
		{name: "unsupported", method: http.MethodConnect, target: "/limited", status: http.StatusNotImplemented, errorClass: "unsupported_protocol"},
		{name: "body limit", method: http.MethodPost, target: "/limited", body: "12345", status: http.StatusRequestEntityTooLarge, errorClass: "request_body_too_large", route: "limited"},
		{name: "upstream", method: http.MethodGet, target: "/bad", status: http.StatusBadGateway, errorClass: "upstream", route: "bad"},
		{name: "timeout", method: http.MethodGet, target: "/timeout", status: http.StatusGatewayTimeout, errorClass: "timeout", route: "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://gateway"+tc.target, strings.NewReader(tc.body))
			if tc.method == http.MethodConnect {
				request.Body = http.NoBody
			}
			response := httptest.NewRecorder()
			g.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			var accessEntries []observer.LoggedEntry
			for _, entry := range logs.All() {
				if entry.Message == "request completed" {
					accessEntries = append(accessEntries, entry)
				}
			}
			if len(accessEntries) != 1 {
				t.Fatalf("access log entries = %d, all logs=%v", len(accessEntries), logs.All())
			}
			fields := accessEntries[0].ContextMap()
			if fields["error_class"] != tc.errorClass || fields["route"] != tc.route {
				t.Fatalf("observed fields = %v, want error=%q route=%q", fields, tc.errorClass, tc.route)
			}
			if _, ok := fields["query"]; ok {
				t.Fatal("access log contains a query field")
			}
			logs.TakeAll()
		})
	}
}
