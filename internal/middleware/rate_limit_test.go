package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimitBurstAndRefill(t *testing.T) {
	clock := time.Unix(0, 0)
	mw, err := RateLimit(RateLimitOptions{Average: 1, Period: time.Second, Burst: 2, MaxKeys: 2, ClientIP: func(*http.Request) string { return "client" }, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
		if rr.Code != http.StatusNoContent {
			t.Fatalf("burst request %d got %d", i, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") != "1" {
		t.Fatalf("limit response: %d %v", rr.Code, rr.Header())
	}
	clock = clock.Add(time.Second)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("refilled request got %d", rr.Code)
	}
}
