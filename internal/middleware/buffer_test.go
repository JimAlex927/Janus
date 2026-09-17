package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBufferCommitsAfterWrappedHandlerReturns(t *testing.T) {
	returned := false
	h := Buffer(64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Buffered", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("body"))
		returned = true
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !returned || w.Code != http.StatusCreated || w.Body.String() != "body" || w.Header().Get("X-Buffered") != "yes" {
		t.Fatalf("buffered response = status %d, body %q, headers %v", w.Code, w.Body.String(), w.Header())
	}
}

func TestBufferRejectsOversizedResponse(t *testing.T) {
	h := Buffer(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("12345"))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "Internal Server Error") {
		t.Fatalf("oversized response = status %d, body %q", w.Code, w.Body.String())
	}
}

func TestBufferReturns504WhenHandlerReturnsAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	h := Buffer(64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		_, _ = w.Write([]byte("late response"))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "Gateway Timeout") {
		t.Fatalf("late buffered response = status %d, body %q; want 504", w.Code, w.Body.String())
	}
}
