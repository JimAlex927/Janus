package limen

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/gateway"
	"janus/internal/middleware"

	"github.com/quic-go/quic-go/http3"
)

// Exercise the real reverse proxy, observer, route, buffer bypass and admission
// chain. A committed SSE timeout must fail the body read and release its permit.
func TestSSEProxyTimeoutReleasesBackendAndAdmission(t *testing.T) {
	for _, version := range []string{"http1", "http2", "http3"} {
		t.Run(version, func(t *testing.T) {
			backendDone := make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(backendDone)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer backend.Close()
			settings := config.DefaultSettings()
			// Keep the timeout short enough for a regression test, but leave enough
			// headroom for TLS/QUIC setup and heavily loaded race/CI workers. The
			// assertion is about the post-commit abort and resource release, not a
			// scheduler-sensitive 100 ms first-byte budget.
			settings.Stream = config.StreamSettings{MaxDuration: config.Duration(3 * time.Second), IdleTimeout: config.Duration(time.Second)}
			g, err := gateway.New(config.Config{
				Listen: "127.0.0.1:8080", Settings: settings,
				Services:    map[string]config.Service{"events": {Upstreams: []string{backend.URL}}},
				Middlewares: map[string]config.Middleware{"buffer": {Buffer: &config.BufferSettings{MaxResponseBodyBytes: 1024}}},
				Routes:      []config.Route{{Name: "events", Match: "PathPrefix(`/`) && Protocol(`sse`)", Service: "events", Middlewares: []string{"buffer"}}},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			limiter := middleware.NewLimiter(1)
			finished := make(chan struct{})
			admitted := middleware.Admission(limiter)(g)
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// HTTP/3 binds its socket before the server goroutine has finished
				// installing the QUIC listener. Probe that boundary explicitly so the
				// SSE assertion below is not coupled to the first goroutine schedule.
				if version == "http3" && r.URL.Path == "/_janus_test_ready" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				defer close(finished)
				admitted.ServeHTTP(w, r)
			})
			cert, key, roots := writeTestCertificate(t)
			protocols := []string{version}
			if version == "http3" {
				protocols = []string{"http1", "http3"}
			}
			l, err := NewBinding("public", config.LimenConfig{Address: "127.0.0.1:0", Protocols: protocols, TLS: &config.TLSSettings{CertFile: cert, KeyFile: key}}, h, settings)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			ln, err := l.Listen()
			if err != nil {
				t.Fatal(err)
			}
			address := ln.Addr().String()
			var transport http.RoundTripper
			tlsConfig := &tls.Config{RootCAs: roots, ServerName: "localhost"}
			if version == "http3" {
				packet, err := l.ListenPacket()
				if err != nil {
					ln.Close()
					t.Fatal(err)
				}
				address = packet.LocalAddr().String()
				tr := &http3.Transport{TLSClientConfig: tlsConfig}
				defer tr.Close()
				transport = tr
			} else {
				tr := &http.Transport{TLSClientConfig: tlsConfig, ForceAttemptHTTP2: version == "http2"}
				defer tr.CloseIdleConnections()
				transport = tr
			}
			served := make(chan error, 1)
			go func() { served <- l.Serve(ln) }()
			defer func() { l.Close(); <-served }()
			if version == "http3" {
				waitForHTTP3Ready(t, address, transport)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept", "text/event-stream")
			resp, err := (&http.Client{Transport: transport}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			wantProto := map[string]int{"http1": 1, "http2": 2, "http3": 3}[version]
			if resp.ProtoMajor != wantProto || resp.StatusCode != 200 {
				t.Fatalf("response=%s %d", resp.Proto, resp.StatusCode)
			}
			reader := bufio.NewReader(resp.Body)
			line, err := reader.ReadString('\n')
			if err != nil || line != "data: first\n" {
				t.Fatalf("first event=%q err=%v", line, err)
			}
			_, err = io.ReadAll(reader)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("expected peer abort before client deadline: body error=%v, client context=%v", err, ctx.Err())
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("proxy handler did not exit")
			}
			select {
			case <-backendDone:
			case <-time.After(3 * time.Second):
				t.Fatal("backend did not observe cancellation")
			}
			if limiter.Active() != 0 || !limiter.Acquire() {
				t.Fatal("stream timeout leaked admission capacity")
			}
			limiter.Release()
		})
	}
}

// waitForHTTP3Ready separates QUIC listener installation from the streaming
// scenario. A short per-attempt deadline prevents quic-go's handshake retry
// budget from turning a scheduling race into a long, opaque test timeout.
func waitForHTTP3Ready(t *testing.T, address string, transport http.RoundTripper) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+"/_janus_test_ready", nil)
		if err == nil {
			resp, requestErr := (&http.Client{Transport: transport}).Do(req)
			if requestErr == nil {
				_ = resp.Body.Close()
				cancel()
				if resp.ProtoMajor == 3 && resp.StatusCode == http.StatusNoContent {
					return
				}
				lastErr = fmt.Errorf("ready response=%s %d", resp.Proto, resp.StatusCode)
			} else {
				lastErr = requestErr
				cancel()
			}
		} else {
			lastErr = err
			cancel()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("HTTP/3 server did not become ready: %v", lastErr)
}
