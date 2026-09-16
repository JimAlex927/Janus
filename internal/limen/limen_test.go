package limen

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"janus/internal/config"
)

func TestLimenUsesConfiguredBudgets(t *testing.T) {
	settings := config.DefaultSettings()
	settings.Request.ReadTimeout = config.Duration(1200 * time.Millisecond)
	settings.Request.MaximumDuration = config.Duration(2 * time.Second)
	settings.Server.WriteTimeout = config.Duration(3 * time.Second)
	settings.Server.ReadHeaderTimeout = config.Duration(350 * time.Millisecond)
	settings.Server.IdleTimeout = config.Duration(7 * time.Second)
	settings.Server.MaxHeaderBytes = 48 << 10

	l := New("127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), settings)
	if l.server.ReadHeaderTimeout != 350*time.Millisecond || l.server.ReadTimeout != 1200*time.Millisecond ||
		l.server.WriteTimeout != 3*time.Second || l.server.IdleTimeout != 7*time.Second || l.server.MaxHeaderBytes != 48<<10 {
		t.Fatalf("server settings were not applied: %+v", l.server)
	}
}

func TestLimenListenReportsBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	l := New(occupied.Addr().String(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), config.DefaultSettings())
	if _, err := l.Listen(); err == nil {
		t.Fatal("Listen succeeded on an occupied address")
	}
}

func TestLimenServeFailureClosesHTTP3Packet(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t)
	reserved, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.LocalAddr().String()
	_ = reserved.Close()

	l, err := NewBinding("public", config.LimenConfig{
		Address:   address,
		Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.NotFoundHandler(), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	tcpListener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ListenPacket(); err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcpListener) }()
	if err := tcpListener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		_ = l.Close()
		t.Fatal("Limen did not return after TCP listener failure")
	}

	replacement, err := net.ListenPacket("udp", address)
	if err != nil {
		_ = l.Close()
		t.Fatalf("HTTP/3 UDP socket remained bound after Serve failure: %v", err)
	}
	_ = replacement.Close()
	_ = l.Close()
}

func TestLimenSlowUploadTerminatesAtReadDeadline(t *testing.T) {
	type readResult struct {
		n   int64
		err error
	}
	readDone := make(chan readResult, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		readDone <- readResult{n: n, err: err}
	})

	settings := config.DefaultSettings()
	settings.Request.ReadTimeout = config.Duration(100 * time.Millisecond)
	settings.Server.ReadHeaderTimeout = config.Duration(time.Second)
	settings.Server.WriteTimeout = config.Duration(time.Second)
	l := New("127.0.0.1:0", handler, settings)
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(ln) }()
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "POST /upload HTTP/1.1\r\nHost: limen\r\nContent-Length: 4\r\nConnection: close\r\n\r\nx"); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-readDone:
		if result.n != 1 {
			t.Fatalf("body bytes read = %d, want 1", result.n)
		}
		var timeoutErr net.Error
		if !errors.As(result.err, &timeoutErr) || !timeoutErr.Timeout() {
			t.Fatalf("body read error = %v, want a timeout", result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow upload was not terminated by the read deadline")
	}
}

func TestLimenSlowReaderTerminatesAtWriteDeadline(t *testing.T) {
	started := make(chan struct{}, 1)
	writeDone := make(chan error, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		started <- struct{}{}
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for {
			if _, err := w.Write(chunk); err != nil {
				writeDone <- err
				return
			}
		}
	})

	settings := config.DefaultSettings()
	settings.Server.WriteTimeout = config.Duration(200 * time.Millisecond)
	l := New("127.0.0.1:0", handler, settings)
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(ln) }()
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	if _, err := io.WriteString(conn, "GET /stream HTTP/1.1\r\nHost: limen\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not start")
	}

	select {
	case err := <-writeDone:
		var timeoutErr net.Error
		if !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() {
			t.Fatalf("response write error = %v, want a timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow reader was not terminated by the write deadline")
	}
}

func TestLimenShutdownForceClosesHijackedConnectionAtDeadline(t *testing.T) {
	handshakeDone := make(chan struct{})
	l := New("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_, _ = io.WriteString(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = buffered.Flush()
		close(handshakeDone)
		// The hijacked connection is intentionally left open until Limen
		// reaches the bounded shutdown deadline.
		_ = conn
	}), config.DefaultSettings())
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(ln) }()
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /socket HTTP/1.1\r\nHost: limen\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	select {
	case <-handshakeDone:
	case <-time.After(time.Second):
		t.Fatal("hijacked connection did not start")
	}
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d", response.StatusCode)
	}
	l.hijackedMu.Lock()
	hijackedCount := len(l.hijacked)
	l.hijackedMu.Unlock()
	if hijackedCount != 1 {
		t.Fatalf("tracked hijacked connections = %d, want 1", hijackedCount)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := l.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("hijacked connection remained open after forced shutdown")
	}
}
