package middleware

import (
	"fmt"
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

func TestRateLimitLRUBoundsAndGenerationReset(t *testing.T) {
	options := RateLimitOptions{Average: 1, Period: time.Hour, Burst: 1, MaxKeys: 2}
	l, _ := NewRateLimiter(options)
	l.allow("a")
	l.allow("b")
	l.allow("a")
	l.allow("c")
	if len(l.buckets) != 2 || l.buckets["b"] != nil || l.buckets["a"] == nil {
		t.Fatal("not bounded LRU")
	}
	if allowed, _ := l.allow("a"); allowed {
		t.Fatal("existing bucket refilled unexpectedly")
	}
	next, _ := NewRateLimiter(options)
	if allowed, _ := next.allow("a"); !allowed {
		t.Fatal("generation-local bucket did not reset")
	}
}

func BenchmarkRateLimitHighCardinality(b *testing.B) {
	for _, size := range []int{1024, 65536} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			l, _ := NewRateLimiter(RateLimitOptions{Average: 1, Period: time.Hour, Burst: 1, MaxKeys: size})
			for i := 0; i < size; i++ {
				l.allow(fmt.Sprint(i))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.allow(fmt.Sprint(i + size))
			}
		})
	}
}
