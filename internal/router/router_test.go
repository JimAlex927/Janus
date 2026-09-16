package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrecedenceAndPathBoundaries(t *testing.T) {
	handler := func(id string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, id) })
	}
	rt := New([]Route{
		{PathPrefix: "/api", Handler: handler("generic")},
		{Host: "example.com", PathPrefix: "/api", Handler: handler("specific")},
		{Host: "example.com", PathPrefix: "/api/users", Handler: handler("users")},
		{PathPrefix: "/api/longer", Handler: handler("generic-long")},
	})
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
