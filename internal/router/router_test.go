package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"janus/internal/protocol"
)

func TestPrecedenceAndPathBoundaries(t *testing.T) {
	handler := func(id string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, id) })
	}
	rt, err := New([]Route{
		{PathPrefix: "/api", Handler: handler("generic")},
		{Host: "example.com", PathPrefix: "/api", Handler: handler("specific")},
		{Host: "example.com", PathPrefix: "/api/users", Handler: handler("users")},
		{PathPrefix: "/api/longer", Handler: handler("generic-long")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, path, want string
		status           int
	}{
		{"example.com", "/api", "specific", 200},
		{"EXAMPLE.COM:8080", "/api/users/42", "users", 200},
		{"example.com", "/api/longer", "specific", 200},
		{"other.com", "/api/longer", "generic-long", 200},
		{"other.com", "/api/x", "generic", 200},
		{"example.com", "/apix", "404 page not found\n", 404},
	} {
		t.Run(tc.host+tc.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tc.host+tc.path, nil)
			w := httptest.NewRecorder()
			rt.ServeHTTP(w, r)
			if w.Code != tc.status || w.Body.String() != tc.want {
				t.Fatalf("got %d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestLimenScopedRoutes(t *testing.T) {
	rt, err := New([]Route{
		{Limen: "public", PathPrefix: "/api", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("public")) })},
		{Limen: "admin", PathPrefix: "/api", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("admin")) })},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
	}{
		{"public", "public"},
		{"admin", "admin"},
		{"unknown", "404 page not found\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := protocol.WithLimenID(httptest.NewRequest("GET", "http://gateway/api", nil), tc.name)
			w := httptest.NewRecorder()
			rt.ServeHTTP(w, r)
			if w.Code != http.StatusOK && tc.name != "unknown" {
				t.Fatalf("status = %d", w.Code)
			}
			if w.Body.String() != tc.want {
				t.Fatalf("body = %q, want %q", w.Body.String(), tc.want)
			}
		})
	}
}

func TestRouteProtocolScope(t *testing.T) {
	rt, err := New([]Route{
		{Protocols: []string{"sse"}, PathPrefix: "/events", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("sse")) })},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, accept string
		code, body   int
	}{
		{name: "sse", accept: "text/event-stream", code: http.StatusOK},
		{name: "ordinary", accept: "application/json", code: http.StatusNotImplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://gateway/events", nil)
			r.Header.Set("Accept", tc.accept)
			w := httptest.NewRecorder()
			rt.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d", w.Code, tc.code)
			}
		})
	}
}

func TestCompiledMatchAndActionPrecedence(t *testing.T) {
	handler := func(id string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(id)) })
	}
	rt, err := New([]Route{
		{Name: "fallback", Match: "Host(`api.example.com`) && PathPrefix(`/api`)", Priority: 1, Handler: handler("fallback")},
		{Name: "read", Match: "Host(`api.example.com`) && PathPrefix(`/api`) && Method(`GET`)", Priority: 10, Handler: handler("read")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, want string
	}{
		{method: http.MethodGet, want: "read"},
		{method: http.MethodPost, want: "fallback"},
	} {
		r := httptest.NewRequest(test.method, "http://api.example.com/api/users", nil)
		w := httptest.NewRecorder()
		rt.ServeHTTP(w, r)
		if got := w.Body.String(); got != test.want {
			t.Fatalf("method %s: body = %q, want %q", test.method, got, test.want)
		}
	}
}

func TestComplexRuleFallsBackWithoutLosingMatch(t *testing.T) {
	rt, err := New([]Route{{
		Name:    "public",
		Match:   "Host(`api.example.com`) || PathPrefix(`/public`)",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("matched")) }),
	}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://other.example.com/public/item", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "matched" {
		t.Fatalf("got %d %q, want 200 matched", w.Code, w.Body.String())
	}
}

func TestCompiledMatchPredicates(t *testing.T) {
	rt, err := New([]Route{{
		Name:    "match",
		Match:   "Host(`api.example.com`) && PathPrefix(`/api`) && Method(`GET`) && Header(`X-Tenant`, `blue`) && Query(`version`, `2`) && Protocol(`http`)",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("matched")) }),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		host       string
		path       string
		method     string
		tenant     string
		query      string
		wantStatus int
		wantBody   string
	}{
		{"all predicates", "API.EXAMPLE.COM:8443", "/api/items?version=2", http.MethodGet, "blue", "", http.StatusOK, "matched"},
		{"wrong method", "api.example.com", "/api/items?version=2", http.MethodPost, "blue", "", http.StatusNotFound, "404 page not found\n"},
		{"wrong header", "api.example.com", "/api/items?version=2", http.MethodGet, "red", "", http.StatusNotFound, "404 page not found\n"},
		{"wrong query", "api.example.com", "/api/items?version=3", http.MethodGet, "blue", "", http.StatusNotFound, "404 page not found\n"},
		{"path boundary", "api.example.com", "/apix?version=2", http.MethodGet, "blue", "", http.StatusNotFound, "404 page not found\n"},
		{"trailing dot", "api.example.com.", "/api?version=2", http.MethodGet, "blue", "", http.StatusOK, "matched"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, "http://"+test.host+test.path, nil)
			r.Header.Set("X-Tenant", test.tenant)
			w := httptest.NewRecorder()
			rt.ServeHTTP(w, r)
			if w.Code != test.wantStatus || w.Body.String() != test.wantBody {
				t.Fatalf("got %d %q, want %d %q", w.Code, w.Body.String(), test.wantStatus, test.wantBody)
			}
		})
	}
}

func TestComplexRoutePriorityIsDeterministic(t *testing.T) {
	rt, err := New([]Route{
		{Name: "broad", Match: "Host(`api.example.com`) || PathPrefix(`/public`)", Priority: 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("broad")) })},
		{Name: "specific", Match: "Host(`api.example.com`) && PathPrefix(`/api/users`)", Priority: 20, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("specific")) })},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://api.example.com/api/users/1", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)
	if got := w.Body.String(); got != "specific" {
		t.Fatalf("body = %q, want specific", got)
	}
}
