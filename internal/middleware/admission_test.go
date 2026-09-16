package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdmissionRejectsWithoutWaitingAndReleases(t *testing.T) {
	limiter := NewLimiter(1)
	started, release := make(chan struct{}, 1), make(chan struct{})
	h := Admission(limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusCreated)
	}))
	firstDone := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not acquire admission")
	}
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated status = %d, want 503", second.Code)
	}
	select {
	case <-firstDone:
		t.Fatal("first request returned before release")
	default:
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first request did not release admission")
	}
	third := httptest.NewRecorder()
	completed := make(chan struct{})
	go func() {
		h.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "/", nil))
		close(completed)
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("admission did not become available")
	}
	if third.Code != http.StatusCreated {
		t.Fatalf("released status = %d, want 201", third.Code)
	}
}

func TestAdmissionReleasesAfterPanic(t *testing.T) {
	limiter := NewLimiter(1)
	h := Admission(limiter)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("handler panic was not propagated")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if got := limiter.Active(); got != 0 {
		t.Fatalf("active permits after panic = %d, want 0", got)
	}
}

func TestAdmissionLoweredLimitWaitsForExistingPermits(t *testing.T) {
	limiter := NewLimiter(2)
	if !limiter.Acquire() || !limiter.Acquire() {
		t.Fatal("failed to acquire initial permits")
	}
	limiter.SetLimit(1)
	if limiter.Acquire() {
		t.Fatal("lowered limiter admitted above the new cap")
	}
	limiter.Release()
	if limiter.Acquire() {
		t.Fatal("lowered limiter admitted while active usage was still at the cap")
	}
	limiter.Release()
	if !limiter.Acquire() {
		t.Fatal("limiter did not admit after usage fell below the cap")
	}
	limiter.Release()
}
