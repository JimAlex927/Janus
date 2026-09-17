package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimeoutClosesRequestBodyWhenDeadlineExpires(t *testing.T) {
	body := &blockingBody{closed: make(chan struct{})}
	done := make(chan error, 1)
	h := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		done <- err
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body))
	select {
	case err := <-done:
		if !errors.Is(err, errBodyClosed) {
			t.Fatalf("body read error = %v, want body close", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request body was not closed after timeout")
	}
}

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
	cancelled := make(chan struct{}, 1)
	h := Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("request context has no deadline")
			return
		}
		seen <- deadline
		<-r.Context().Done()
		cancelled <- struct{}{}
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
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("earlier parent deadline did not cancel the request")
	}
}

var errBodyClosed = errors.New("body closed")

type blockingBody struct {
	closed chan struct{}
}

func (b *blockingBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, errBodyClosed
}

func (b *blockingBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}
