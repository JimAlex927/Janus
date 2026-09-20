package runtime

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"janus/internal/config"
	"janus/internal/middleware"
)

func TestRealKeepAliveUsesNewGenerationWithoutNewHandshake(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "http1"
		if secure {
			name = "https-http1"
		}
		t.Run(name, func(t *testing.T) {
			builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
				return &testGeneration{closed: make(chan struct{}), handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, c.Routes[0].Name)
				})}, nil
			}
			r, err := NewWithBuilder(validRuntimeConfig("old"), zap.NewNop(), builder)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			server := httptest.NewUnstartedServer(r.Handler())
			var accepted atomic.Int32
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					accepted.Add(1)
				}
			}
			if secure {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := server.Client()
			client.Timeout = 3 * time.Second
			defer client.CloseIdleConnections()
			var handshakes atomic.Int32
			var first net.Conn
			for i, want := range []string{"old", "new"} {
				if i == 1 {
					if err := r.Replace(validRuntimeConfig("new")); err != nil {
						t.Fatal(err)
					}
				}
				var conn net.Conn
				var reused bool
				req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
				req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
					GotConn:          func(info httptrace.GotConnInfo) { conn, reused = info.Conn, info.Reused },
					TLSHandshakeDone: func(tls.ConnectionState, error) { handshakes.Add(1) },
				}))
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || string(body) != want || resp.ProtoMajor != 1 {
					t.Fatalf("response = %q %s %v", body, resp.Proto, err)
				}
				if i == 0 {
					first = conn
				} else if conn != first || !reused {
					t.Fatal("reload opened a new TCP connection")
				}
			}
			if accepted.Load() != 1 {
				t.Fatalf("accepted connections = %d", accepted.Load())
			}
			wantTLS := int32(0)
			if secure {
				wantTLS = 1
			}
			if handshakes.Load() != wantTLS {
				t.Fatalf("TLS handshakes = %d", handshakes.Load())
			}
		})
	}
}

func TestRealHTTP2AndSSEKeepOldStreamAcrossReload(t *testing.T) {
	for _, protocol := range []string{"h2", "sse-http1"} {
		t.Run(protocol, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			oldClosed := make(chan struct{})
			builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
				closed := make(chan struct{})
				if c.Routes[0].Name == "old" {
					closed = oldClosed
				}
				return &testGeneration{closed: closed, handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path != "/slow" {
						_, _ = io.WriteString(w, c.Routes[0].Name)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: "+c.Routes[0].Name+"\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-release:
					case <-req.Context().Done():
						return
					}
					_, _ = io.WriteString(w, "data: "+c.Routes[0].Name+"-end\n\n")
				})}, nil
			}
			r, err := NewWithBuilder(validRuntimeConfig("old"), zap.NewNop(), builder)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			server := httptest.NewUnstartedServer(r.Handler())
			server.EnableHTTP2 = protocol == "h2"
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			defer client.CloseIdleConnections()
			get := func(path string) (*http.Response, net.Conn) {
				var conn net.Conn
				req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
				req.Header.Set("Accept", "text/event-stream")
				req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { conn = info.Conn }}))
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return resp, conn
			}
			slow, first := get("/slow")
			defer slow.Body.Close()
			reader := bufio.NewReader(slow.Body)
			line, err := reader.ReadString('\n')
			if err != nil || line != "data: old\n" {
				t.Fatalf("initial stream = %q %v", line, err)
			}
			if protocol == "h2" && slow.ProtoMajor != 2 {
				t.Fatalf("protocol = %s", slow.Proto)
			}
			if err := r.Replace(validRuntimeConfig("new")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-oldClosed:
				t.Fatal("old generation closed while stream active")
			default:
			}
			fast, second := get("/fast")
			body, err := io.ReadAll(fast.Body)
			fast.Body.Close()
			if err != nil || string(body) != "new" {
				t.Fatalf("new request = %q %v", body, err)
			}
			if protocol == "h2" && (fast.ProtoMajor != 2 || first != second) {
				t.Fatal("H2 did not reuse the active connection")
			}
			unblock()
			body, err = io.ReadAll(reader)
			if err != nil || string(body) != "\ndata: old-end\n\n" {
				t.Fatalf("old stream lost after reload = %q %v", body, err)
			}
			select {
			case <-oldClosed:
			case <-time.After(time.Second):
				t.Fatal("completed old generation not reclaimed")
			}
		})
	}
}

func TestRealGatewayReloadReusesUpstreamConnectionAndChangesTarget(t *testing.T) {
	var connections atomic.Int32
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "first") }))
	backend.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	backend.Start()
	defer backend.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "second") }))
	defer other.Close()
	candidate := func(name, target string) config.Config {
		c := validRuntimeConfig(name)
		c.Services["service"] = config.Service{Upstreams: []string{target}}
		return c
	}
	r, err := New(candidate("old", backend.URL), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	server := httptest.NewServer(r.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	defer client.CloseIdleConnections()
	for i, target := range []string{backend.URL, backend.URL, other.URL} {
		if i > 0 {
			if err := r.Replace(candidate("new", target)); err != nil {
				t.Fatal(err)
			}
		}
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := "first"
		if i == 2 {
			want = "second"
		}
		if err != nil || string(body) != want {
			t.Fatalf("response = %q %v", body, err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("same target accepted %d connections across reload", connections.Load())
	}
}
