package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPAllowList(t *testing.T) {
	mw, err := IPAllowList(IPAllowListOptions{SourceRanges: []string{"10.0.0.0/8"}, ClientIP: func(*http.Request) string { return "10.1.2.3" }})
	if err != nil {
		t.Fatal(err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("allowed request got %d", rr.Code)
	}
	mw, _ = IPAllowList(IPAllowListOptions{SourceRanges: []string{"10.0.0.0/8"}, ClientIP: func(*http.Request) string { return "192.0.2.1" }})
	rr = httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("denied request got %d", rr.Code)
	}
}
