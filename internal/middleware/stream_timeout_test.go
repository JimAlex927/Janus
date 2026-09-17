package middleware

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamTimeoutReturns504BeforeCommitment(t *testing.T) {
	h := StreamTimeout(10*time.Millisecond, 0)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://gateway/events", nil)
	r.Header.Set("Accept", "text/event-stream")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusGatewayTimeout || w.Body.String() == "" {
		t.Fatalf("stream timeout response = %d %q, want 504", w.Code, w.Body.String())
	}
}

func TestStreamIdleTimeoutResetsOnResponseActivity(t *testing.T) {
	done := make(chan struct{})
	h := StreamTimeout(time.Second, 50*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("stream writer does not preserve Flusher")
			return
		}
		_, _ = io.WriteString(w, "first")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, "second")
		close(done)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://gateway/events", nil)
	r.Header.Set("Accept", "text/event-stream")
	h.ServeHTTP(w, r)
	select {
	case <-done:
	default:
		t.Fatal("stream handler did not finish")
	}
	if w.Code != http.StatusOK || w.Body.String() != "firstsecond" {
		t.Fatalf("stream response = %d %q, want 200 firstsecond", w.Code, w.Body.String())
	}
}

func TestStreamTimeoutClosesHijackedConnection(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	control := newStreamControl()
	go runStreamTimers(ctx, cancel, control, nil, time.Hour, time.Hour)
	serverConn, clientConn := net.Pipe()
	control.hijacked.Store(true)
	control.setConn(&streamConn{Conn: serverConn, control: control})
	cancel(context.DeadlineExceeded)
	defer clientConn.Close()
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := clientConn.Read(make([]byte, 1)); err == nil {
		t.Fatal("hijacked connection remained open after stream cancellation")
	}
}

func TestHijackedStreamActivityResetsIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	control := newStreamControl()
	activity := make(chan struct{}, 1)
	go runStreamTimers(ctx, cancel, control, activity, time.Second, 100*time.Millisecond)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	wrapped := &streamConn{Conn: serverConn, control: control, activity: activity}
	control.hijacked.Store(true)
	control.setConn(wrapped)

	readDone := make(chan error, 1)
	go func() {
		_, err := clientConn.Read(make([]byte, 1))
		readDone <- err
	}()
	if _, err := wrapped.Write([]byte("x")); err != nil {
		t.Fatalf("initial WebSocket write failed: %v", err)
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("initial WebSocket read failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("initial WebSocket frame was not delivered")
	}
	time.Sleep(50 * time.Millisecond)
	secondRead := make(chan error, 1)
	go func() {
		_, err := clientConn.Read(make([]byte, 1))
		secondRead <- err
	}()
	if _, err := wrapped.Write([]byte("y")); err != nil {
		t.Fatalf("idle timeout did not reset on WebSocket activity: %v", err)
	}
	select {
	case err := <-secondRead:
		if err != nil {
			t.Fatalf("second WebSocket read failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second WebSocket frame was not delivered")
	}
	time.Sleep(75 * time.Millisecond)
	thirdRead := make(chan error, 1)
	go func() {
		_, err := clientConn.Read(make([]byte, 1))
		thirdRead <- err
	}()
	if _, err := wrapped.Write([]byte("z")); err != nil {
		t.Fatalf("second activity did not reset idle timeout: %v", err)
	}
	select {
	case err := <-thirdRead:
		if err != nil {
			t.Fatalf("third WebSocket read failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("third WebSocket frame was not delivered")
	}
}
