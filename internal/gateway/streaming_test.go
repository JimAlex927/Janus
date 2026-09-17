package gateway

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/limen"
)

func startStreamingGateway(t *testing.T, c config.Config) (string, func()) {
	t.Helper()
	g, err := New(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := limen.New("127.0.0.1:0", g, c.Settings)
	listener, err := server.Listen()
	if err != nil {
		g.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	cleanup := func() {
		_ = server.Close()
		_ = <-serveDone
		g.Close()
	}
	return listener.Addr().String(), cleanup
}

func TestSSEPassesEventsBeforeBackendCompletes(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("backend does not support flushing")
			return
		}
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		<-release
		_, _ = io.WriteString(w, "data: second\n\n")
		flusher.Flush()
	}))
	defer backend.Close()
	address, cleanup := startStreamingGateway(t, config.Config{
		Listen:   "127.0.0.1:8080",
		Settings: config.Settings{Request: config.RequestSettings{MaximumDuration: config.Duration(100 * time.Millisecond)}, Server: config.ServerSettings{WriteTimeout: config.Duration(200 * time.Millisecond)}},
		Services: map[string]config.Service{"events": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "events", PathPrefix: "/events", Protocols: []string{config.RouteProtocolSSE}, Service: "events"}},
	})
	defer cleanup()

	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: first\n" {
		t.Fatalf("first event = %q, error %v", first, err)
	}
	time.Sleep(300 * time.Millisecond)
	close(release)
	second, err := reader.ReadString('\n')
	if err != nil || second != "\n" {
		t.Fatalf("event separator = %q, error %v", second, err)
	}
}

func TestWebSocketUpgradeIsProxied(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "not websocket", http.StatusBadRequest)
			return
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
		if err := buffered.Flush(); err != nil {
			t.Error(err)
			return
		}
		payload, err := readClientFrame(buffered.Reader)
		if err != nil {
			t.Error(err)
			return
		}
		writeServerFrame(buffered, append([]byte("echo:"), payload...))
	}))
	defer backend.Close()
	address, cleanup := startStreamingGateway(t, config.Config{
		Listen:   "127.0.0.1:8080",
		Services: map[string]config.Service{"socket": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "socket", PathPrefix: "/socket", Protocols: []string{config.RouteProtocolWebSocket}, Service: "socket"}},
	})
	defer cleanup()

	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("test-websocket-key"))
	request := "GET /socket HTTP/1.1\r\nHost: gateway\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", response.StatusCode)
	}
	writeClientFrame(conn, []byte("ping"))
	payload, err := readServerFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "echo:ping" {
		t.Fatalf("websocket payload = %q", payload)
	}
}

func TestWebSocketStreamTimeoutClosesUpgradedConnection(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if err := buffered.Flush(); err != nil {
			t.Error(err)
			return
		}
		<-r.Context().Done()
	}))
	defer backend.Close()
	settings := config.Settings{
		Request: config.RequestSettings{MaximumDuration: config.Duration(100 * time.Millisecond)},
		Stream:  config.StreamSettings{MaxDuration: config.Duration(time.Second), IdleTimeout: config.Duration(50 * time.Millisecond)},
		Server:  config.ServerSettings{WriteTimeout: config.Duration(500 * time.Millisecond)},
	}
	address, cleanup := startStreamingGateway(t, config.Config{
		Listen:   "127.0.0.1:8080",
		Settings: settings,
		Services: map[string]config.Service{"socket": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "socket", PathPrefix: "/socket", Protocols: []string{config.RouteProtocolWebSocket}, Service: "socket"}},
	})
	defer cleanup()

	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("stream-timeout-key"))
	request := "GET /socket HTTP/1.1\r\nHost: gateway\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", response.StatusCode)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("upgraded connection remained open after idle timeout")
	}
}

func writeClientFrame(w io.Writer, payload []byte) {
	mask := [4]byte{1, 2, 3, 4}
	frame := []byte{0x81, byte(0x80 | len(payload)), mask[0], mask[1], mask[2], mask[3]}
	for i, value := range payload {
		frame = append(frame, value^mask[i%4])
	}
	_, _ = w.Write(frame)
}

func readClientFrame(r *bufio.Reader) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := int(header[1] & 0x7f)
	mask := make([]byte, 4)
	if _, err := io.ReadFull(r, mask); err != nil {
		return nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, nil
}

func writeServerFrame(w *bufio.ReadWriter, payload []byte) {
	_, _ = w.Write(append([]byte{0x81, byte(len(payload))}, payload...))
	_ = w.Flush()
}

func readServerFrame(r *bufio.Reader) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := int(header[1] & 0x7f)
	payload := make([]byte, length)
	_, err := io.ReadFull(r, payload)
	return payload, err
}
