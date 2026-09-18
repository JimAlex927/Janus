package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddPrefixPreservesOriginalAndQuery(t *testing.T) {
	var path, query string
	h := AddPrefix("/internal")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
	}))
	req := httptest.NewRequest(http.MethodGet, "/orders?id=7", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if path != "/internal/orders" || query != "id=7" {
		t.Fatalf("path = %q, query = %q", path, query)
	}
	if req.URL.Path != "/orders" {
		t.Fatalf("original path = %q", req.URL.Path)
	}
}

func TestAddPrefixPreservesEscapedPath(t *testing.T) {
	var escaped string
	h := AddPrefix("/internal")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { escaped = r.URL.EscapedPath() }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a%2Fb", nil))
	if escaped != "/internal/a%2Fb" {
		t.Fatalf("escaped path = %q", escaped)
	}
}
