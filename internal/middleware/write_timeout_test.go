package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestWriteTimeoutSetsDeadlineForCurrentResponse(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	started := time.Now()
	h := WriteTimeout(250 * time.Millisecond)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.deadline.Before(started.Add(200*time.Millisecond)) || w.deadline.After(time.Now().Add(300*time.Millisecond)) {
		t.Fatalf("write deadline = %s, want approximately 250ms from now", w.deadline)
	}
}
