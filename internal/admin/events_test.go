package admin

import (
	"bufio"
	"net/http/httptest"
	"testing"
	"time"

	"janus/internal/config"
	janusruntime "janus/internal/runtime"
)

func TestEventsOutliveServerWriteTimeout(t *testing.T) {
	events := make(chan janusruntime.Event, 1)
	unsubscribed := make(chan struct{})
	h := NewHandlerWithOptions(Options{
		UIBaseURL: "/janus",
		Current:   func() config.Config { return config.Config{} },
		Subscribe: func() (<-chan janusruntime.Event, func()) {
			return events, func() { close(unsubscribed) }
		},
	})
	server := httptest.NewUnstartedServer(h)
	server.Config.WriteTimeout = 30 * time.Millisecond
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	defer client.CloseIdleConnections()
	resp, err := client.Get(server.URL + "/janus/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	for _, want := range []string{"event: ready\n", "data: {\"revision\":0}\n", "\n"} {
		got, err := reader.ReadString('\n')
		if err != nil || got != want {
			t.Fatalf("ready line = %q %v", got, err)
		}
	}
	// Let the server's original per-response deadline expire, then send a
	// real event. Waiting is intentional: this tests net/http's socket deadline.
	time.Sleep(90 * time.Millisecond)
	events <- janusruntime.Event{Type: "reload"}
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: reload\n" {
		t.Fatalf("SSE after WriteTimeout = %q %v", line, err)
	}
	resp.Body.Close()
	select {
	case <-unsubscribed:
	case <-time.After(time.Second):
		t.Fatal("disconnected stream did not unsubscribe")
	}
}
