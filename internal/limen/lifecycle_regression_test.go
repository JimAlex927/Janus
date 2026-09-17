package limen

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/middleware"

	"github.com/quic-go/quic-go/http3"
)

func TestRegressionCertificateChangedDuringStartupMustApply(t *testing.T) {
	cert, key, _ := writeTestCertificate(t)
	otherCert, otherKey, _ := writeTestCertificate(t)
	l, err := NewBinding("public", config.LimenConfig{Address: "127.0.0.1:0", Protocols: []string{"http1"}, TLS: &config.TLSSettings{CertFile: cert, KeyFile: key}}, http.NotFoundHandler(), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	expected, err := loadTLSKeyPair(otherCert, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := os.ReadFile(otherCert)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := os.ReadFile(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cert, cb, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, kb, 0600); err != nil {
		t.Fatal(err)
	}
	watcher, err := NewCertificateReloader(map[string]config.LimenConfig{"public": {TLS: &config.TLSSettings{CertFile: cert, KeyFile: key}}}, map[string]*Limen{"public": l}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := watcher.ReloadOnce(); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(l.cert.Load().Certificate[0], expected.Certificate[0]) {
		t.Fatal("disk certificate changed during startup but active TLS identity remains old after three polls")
	}
}

func TestRegressionH3GracefulShutdownCompletesAcceptedRequest(t *testing.T) {
	cert, key, roots := writeTestCertificate(t)
	started, release := make(chan struct{}), make(chan struct{})
	l, err := NewBinding("public", config.LimenConfig{Address: "127.0.0.1:0", Protocols: []string{"http1", "http3"}, TLS: &config.TLSSettings{CertFile: cert, KeyFile: key}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; io.WriteString(w, "complete") }), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tcp, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := l.ListenPacket()
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- l.Serve(tcp) }()
	tr := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"}}
	defer tr.Close()
	result := make(chan error, 1)
	go func() {
		resp, e := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Get("https://" + udp.LocalAddr().String())
		if e == nil {
			b, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			e = readErr
			if e == nil && string(b) != "complete" {
				e = fmt.Errorf("body=%q", b)
			}
		}
		result <- e
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("request did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- l.Shutdown(ctx) }()
	select {
	case <-served:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("listener did not stop")
	}
	close(release)
	select {
	case e := <-result:
		if e != nil {
			t.Errorf("accepted H3 request lost during graceful drain: %v", e)
		}
	case <-time.After(4 * time.Second):
		t.Error("request hung")
	}
	select {
	case e := <-drained:
		if e != nil {
			t.Errorf("drain: %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Error("drain hung")
	}
}

func TestRegressionSSESlowReaderMustReleaseHandler(t *testing.T) {
	done := make(chan struct{})
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer close(done)
		w.Header().Set("Content-Type", "text/event-stream")
		p := make([]byte, 64<<10)
		for i := 0; i < 16384; i++ {
			n, e := w.Write(p)
			if e != nil {
				t.Logf("write exit after %d chunks: bytes=%d err=%v", i, n, e)
				return
			}
		}
		t.Error("wrote 1GiB without blocking; slow-reader precondition was not exercised")
	}), middleware.StreamTimeout(100*time.Millisecond, 100*time.Millisecond), middleware.ClearStreamingWriteDeadline)
	l := New("127.0.0.1:0", h, config.DefaultSettings())
	defer l.Close()
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- l.Serve(smallWriteBufferListener{ln}) }()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if c, ok := conn.(*net.TCPConn); ok {
		c.SetReadBuffer(1024)
	}
	fmt.Fprint(conn, "GET /events HTTP/1.1\r\nHost: localhost\r\nAccept: text/event-stream\r\n\r\n")
	select {
	case <-done:
	case <-time.After(800 * time.Millisecond):
		t.Error("100ms stream timeout did not release blocked downstream Write after 800ms")
	}
	conn.Close()
	l.Close()
	<-served
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("handler did not exit after client closed")
	}
}

type smallWriteBufferListener struct{ net.Listener }

func (l smallWriteBufferListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e == nil {
		if tcp, ok := c.(*net.TCPConn); ok {
			tcp.SetWriteBuffer(1024)
		}
	}
	return c, e
}

func TestRegressionSSEEarlyHintsStillAllows504(t *testing.T) {
	h := middleware.StreamTimeout(20*time.Millisecond, 0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		<-r.Context().Done()
	}))
	s := httptest.NewServer(h)
	defer s.Close()
	req, _ := http.NewRequest("GET", s.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 504 {
		t.Fatalf("103 then timeout returned final %d, want 504", resp.StatusCode)
	}
}
