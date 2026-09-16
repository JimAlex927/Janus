package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimeoutCancelsContext(t *testing.T) {
	done := make(chan struct{})
	h := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(done)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout did not cancel the request context")
	}
}

func TestTimeoutPreservesEarlierParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	seen := make(chan time.Time, 1)
	h := Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("request context has no deadline")
			return
		}
		seen <- deadline
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(parent))
	select {
	case deadline := <-seen:
		if remaining := time.Until(deadline); remaining > 100*time.Millisecond {
			t.Fatalf("parent deadline was extended, remaining %s", remaining)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not run")
	}
}
