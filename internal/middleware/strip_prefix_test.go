package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripPrefixUsesPathBoundaryAndPreservesOriginalRequest(t *testing.T) {
	var gotPath, gotPrefix string
	h := StripPrefix("/api")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotPrefix = r.Header.Get("X-Forwarded-Prefix")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotPath != "/orders" || gotPrefix != "/api" {
		t.Fatalf("path = %q, forwarded prefix = %q", gotPath, gotPrefix)
	}
	if req.URL.Path != "/api/orders" || req.Header.Get("X-Forwarded-Prefix") != "" {
		t.Fatalf("original request was mutated: path=%q headers=%#v", req.URL.Path, req.Header)
	}

	gotPath, gotPrefix = "", ""
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/apix/orders", nil))
	if gotPath != "/apix/orders" || gotPrefix != "" {
		t.Fatalf("non-boundary path = %q, prefix = %q", gotPath, gotPrefix)
	}
}

func TestStripPrefixMapsExactPrefixToRoot(t *testing.T) {
	var got string
	h := StripPrefix("/api")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.URL.Path }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api", nil))
	if got != "/" {
		t.Fatalf("path = %q, want root", got)
	}
}

func TestStripPrefixPreservesEscapedPathSemantics(t *testing.T) {
	var escaped string
	h := StripPrefix("/api")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { escaped = r.URL.EscapedPath() }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/a%2Fb", nil))
	if escaped != "/a%2Fb" {
		t.Fatalf("escaped path = %q", escaped)
	}
}
