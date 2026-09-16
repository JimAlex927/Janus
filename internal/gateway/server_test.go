package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownFinishesAcceptedRequest(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			io.WriteString(w, "complete")
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	g := testGateway(t, backend.URL)
	srv := NewServer("127.0.0.1:0", g)
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + ln.Addr().String() + "/api")
		if err != nil {
			response <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil && (resp.StatusCode != 200 || string(body) != "complete") {
			err = errors.New("accepted response was truncated")
		}
		response <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request never started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- srv.Shutdown(ctx) }()
	// Serve returns when Shutdown closes the listener, before the active request ends.
	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("listener did not close")
	}
	select {
	case err := <-drained:
		t.Fatalf("drain ended before active request: %v", err)
	default:
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
}
