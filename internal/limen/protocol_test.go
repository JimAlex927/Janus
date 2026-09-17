package limen

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/internal/config"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func startTestLimen(t *testing.T, binding config.LimenConfig, handler http.Handler) (*Limen, net.Listener) {
	t.Helper()
	l, err := NewBinding("public", binding, handler, config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(listener) }()
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})
	return l, listener
}

func TestLimenServesHTTP3AndAdvertisesUDPPort(t *testing.T) {
	certFile, keyFile, roots := writeTestCertificate(t)
	l, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
		HTTP3:     &config.HTTP3Settings{MaxConcurrentStreams: 7},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if l.http3.QUICConfig.Allow0RTT || l.http3.QUICConfig.MaxIncomingStreams != 7 {
		t.Fatalf("HTTP/3 QUIC settings = %+v", l.http3.QUICConfig)
	}
	tcpListener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	udpListener, err := l.ListenPacket()
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcpListener) }()
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"},
		QUICConfig:      &quic.Config{Allow0RTT: false},
	}
	defer transport.Close()
	response, err := (&http.Client{Transport: transport}).Get("https://" + udpListener.LocalAddr().String() + "/h3")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 3 || string(body) != "HTTP/3.0" {
		t.Fatalf("HTTP/3 response = proto %s body %q", response.Proto, body)
	}

	h1Transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, Protocols: new(http.Protocols)}
	h1Transport.Protocols.SetHTTP1(true)
	defer h1Transport.CloseIdleConnections()
	h1Response, err := (&http.Client{Transport: h1Transport}).Get("https://" + tcpListener.Addr().String() + "/h1")
	if err != nil {
		t.Fatal(err)
	}
	if got := h1Response.Header.Get("Alt-Svc"); !strings.Contains(got, "h3=\":") {
		h1Response.Body.Close()
		t.Fatalf("Alt-Svc = %q, want HTTP/3 advertisement", got)
	}
	_, _ = io.Copy(io.Discard, h1Response.Body)
	h1Response.Body.Close()
}

func TestLimenRejectsHTTP3WithoutTLSOrTCPFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding config.LimenConfig
	}{
		{name: "without tls", binding: config.LimenConfig{Address: "127.0.0.1:8443", Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3}}},
		{name: "without tcp fallback", binding: config.LimenConfig{Address: "127.0.0.1:8443", Protocols: []string{config.ProtocolHTTP3}, TLS: &config.TLSSettings{CertFile: "missing", KeyFile: "missing"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewBinding("public", tc.binding, http.NotFoundHandler(), config.DefaultSettings()); err == nil {
				t.Fatal("expected HTTP/3 binding validation error")
			}
		})
	}
}

func TestLimenHTTP3StreamsAreIsolated(t *testing.T) {
	certFile, keyFile, roots := writeTestCertificate(t)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	l, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
			_, _ = io.WriteString(w, "slow")
			return
		}
		_, _ = io.WriteString(w, "fast")
	}), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	tcpListener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	udpListener, err := l.ListenPacket()
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcpListener) }()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"},
		QUICConfig:      &quic.Config{Allow0RTT: false},
	}
	defer transport.Close()
	client := &http.Client{Transport: transport}
	slowDone := make(chan error, 1)
	go func() {
		response, err := client.Get("https://" + udpListener.LocalAddr().String() + "/slow")
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil || string(body) != "slow" {
				err = fmt.Errorf("slow response body = %q, read error = %v", body, readErr)
			}
		}
		slowDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP/3 slow stream did not start")
	}

	fastResponse, err := client.Get("https://" + udpListener.LocalAddr().String() + "/fast")
	if err != nil {
		t.Fatal(err)
	}
	fastBody, err := io.ReadAll(fastResponse.Body)
	fastResponse.Body.Close()
	if err != nil || string(fastBody) != "fast" || fastResponse.ProtoMajor != 3 {
		t.Fatalf("HTTP/3 fast response = proto %s body %q error %v", fastResponse.Proto, fastBody, err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-slowDone; err != nil {
		t.Fatal(err)
	}
}

func TestLimenHTTP3ClientCancellationReachesHandler(t *testing.T) {
	certFile, keyFile, roots := writeTestCertificate(t)
	started, canceled := make(chan struct{}), make(chan struct{})
	l, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	tcpListener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	udpListener, err := l.ListenPacket()
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcpListener) }()
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"},
		QUICConfig:      &quic.Config{Allow0RTT: false},
	}
	defer transport.Close()
	request, err := http.NewRequest(http.MethodGet, "https://"+udpListener.LocalAddr().String()+"/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestCtx)
	requestDone := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Transport: transport}).Do(request)
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP/3 cancellation handler did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP/3 client cancellation did not reach handler")
	}
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP/3 client request did not finish after cancellation")
	}
}

func TestLimenHTTP3FailureStopsTCPFallback(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t)
	l, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
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
	l.packetMu.Lock()
	actualPacket := l.packet
	brokenPacket := &brokenPacketConn{PacketConn: actualPacket, broken: make(chan struct{}), err: errors.New("injected UDP failure")}
	l.packet = brokenPacket
	l.packetMu.Unlock()
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcpListener) }()
	t.Cleanup(func() {
		_ = l.Close()
	})

	probe, err := net.DialTimeout("tcp", tcpListener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = probe.Close()
	brokenPacket.Break()
	select {
	case err := <-serveDone:
		if err == nil {
			t.Fatal("Limen returned nil after HTTP/3 failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Limen did not return after HTTP/3 packet failure")
	}

	probe, err = net.DialTimeout("tcp", tcpListener.Addr().String(), time.Second)
	if err == nil {
		_ = probe.Close()
		t.Fatal("TCP fallback remained available after HTTP/3 failure")
	}
}

type brokenPacketConn struct {
	net.PacketConn
	broken chan struct{}
	err    error
}

func (c *brokenPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	if err != nil {
		select {
		case <-c.broken:
			return 0, nil, c.err
		default:
		}
	}
	return n, addr, err
}

func (c *brokenPacketConn) Break() {
	close(c.broken)
	_ = c.SetDeadline(time.Now())
}

func TestLimenHTTP3ShutdownHonorsDrainDeadline(t *testing.T) {
	certFile, keyFile, roots := writeTestCertificate(t)
	started, release := make(chan struct{}), make(chan struct{})
	l, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	tcpListener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	udpListener, err := l.ListenPacket()
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcpListener) }()
	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"},
		QUICConfig:      &quic.Config{Allow0RTT: false},
	}
	defer transport.Close()
	go func() {
		response, requestErr := (&http.Client{Transport: transport}).Get("https://" + udpListener.LocalAddr().String() + "/drain")
		if response != nil {
			response.Body.Close()
		}
		_ = requestErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(release)
		_ = l.Close()
		t.Fatal("HTTP/3 drain handler did not start")
	}

	shutdownDone := make(chan error, 1)
	shutdownContext, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go func() { shutdownDone <- l.Shutdown(shutdownContext) }()
	select {
	case shutdownErr := <-shutdownDone:
		if !errors.Is(shutdownErr, context.DeadlineExceeded) {
			t.Fatalf("HTTP/3 shutdown error = %v, want deadline exceeded", shutdownErr)
		}
	case <-time.After(time.Second):
		close(release)
		_ = l.Close()
		t.Fatal("HTTP/3 shutdown exceeded its context deadline")
	}
	close(release)
	_ = l.Close()
	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP/3 server did not stop after forced shutdown")
	}
}

func TestLimenNegotiatesHTTP2AndHTTP1Fallback(t *testing.T) {
	certFile, keyFile, roots := writeTestCertificate(t)
	binding := config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP2},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}
	_, listener := startTestLimen(t, binding, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))

	h2Transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2: true,
	}
	defer h2Transport.CloseIdleConnections()
	h2Client := &http.Client{Transport: h2Transport}
	h2Response, err := h2Client.Get("https://" + listener.Addr().String() + "/h2")
	if err != nil {
		t.Fatal(err)
	}
	h2Body, err := io.ReadAll(h2Response.Body)
	h2Response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if h2Response.ProtoMajor != 2 || string(h2Body) != "HTTP/2.0" {
		t.Fatalf("HTTP/2 response = proto %s body %q", h2Response.Proto, h2Body)
	}

	h1Transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, Protocols: new(http.Protocols)}
	h1Transport.Protocols.SetHTTP1(true)
	defer h1Transport.CloseIdleConnections()
	h1Response, err := (&http.Client{Transport: h1Transport}).Get("https://" + listener.Addr().String() + "/h1")
	if err != nil {
		t.Fatal(err)
	}
	h1Body, err := io.ReadAll(h1Response.Body)
	h1Response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if h1Response.ProtoMajor != 1 || string(h1Body) != "HTTP/1.1" {
		t.Fatalf("HTTP/1 response = proto %s body %q", h1Response.Proto, h1Body)
	}
}

func TestLimenHTTP2ConcurrentStreamsAreIsolated(t *testing.T) {
	certFile, keyFile, roots := writeTestCertificate(t)
	started, release := make(chan struct{}), make(chan struct{})
	_, listener := startTestLimen(t, config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP2},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
			_, _ = io.WriteString(w, "slow")
			return
		}
		_, _ = io.WriteString(w, "fast")
	}))

	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2: true,
		MaxConnsPerHost:   1,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	slowConn := make(chan net.Conn, 1)
	fastConn := make(chan net.Conn, 1)
	slowRequest, err := http.NewRequest(http.MethodGet, "https://"+listener.Addr().String()+"/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	slowRequest = slowRequest.WithContext(httptrace.WithClientTrace(slowRequest.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { slowConn <- info.Conn },
	}))
	slowResponse := make(chan *http.Response, 1)
	slowErrors := make(chan error, 1)
	go func() {
		response, err := client.Do(slowRequest)
		if err != nil {
			slowErrors <- err
			return
		}
		slowResponse <- response
	}()
	select {
	case <-started:
	case err := <-slowErrors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("slow stream did not start")
	}
	var firstConn net.Conn
	select {
	case firstConn = <-slowConn:
	case <-time.After(time.Second):
		t.Fatal("slow stream connection was not observed")
	}

	fastRequest, err := http.NewRequest(http.MethodGet, "https://"+listener.Addr().String()+"/fast", nil)
	if err != nil {
		t.Fatal(err)
	}
	fastConnTrace := httptrace.WithClientTrace(fastRequest.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { fastConn <- info.Conn },
	})
	fastRequest = fastRequest.WithContext(fastConnTrace)
	fastResponse, err := client.Do(fastRequest)
	if err != nil {
		t.Fatal(err)
	}
	fastBody, err := io.ReadAll(fastResponse.Body)
	fastResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if fastResponse.ProtoMajor != 2 || string(fastBody) != "fast" {
		t.Fatalf("fast stream = proto %s body %q", fastResponse.Proto, fastBody)
	}
	select {
	case secondConn := <-fastConn:
		if secondConn != firstConn {
			t.Fatal("streams did not share the HTTP/2 connection")
		}
	case <-time.After(time.Second):
		t.Fatal("fast stream connection was not observed")
	}

	close(release)
	select {
	case response := <-slowResponse:
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "slow" {
			t.Fatalf("slow stream = body %q error %v", body, err)
		}
	case err := <-slowErrors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("slow stream did not finish")
	}
}

func TestLimenHTTP2ShutdownDrainsActiveStream(t *testing.T) {
	certFile, keyFile, roots := writeTestCertificate(t)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	l, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP2},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
		}
		_, _ = io.WriteString(w, "done")
	}), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	tcpListener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcpListener) }()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2: true,
		MaxConnsPerHost:   1,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	responseDone := make(chan *http.Response, 1)
	responseErr := make(chan error, 1)
	go func() {
		response, requestErr := client.Get("https://" + tcpListener.Addr().String() + "/slow")
		if requestErr != nil {
			responseErr <- requestErr
			return
		}
		responseDone <- response
	}()
	select {
	case <-started:
	case err := <-responseErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP/2 active stream did not start")
	}

	shutdownDone := make(chan error, 1)
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { shutdownDone <- l.Shutdown(shutdownContext) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown completed before active stream: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case response := <-responseDone:
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.ProtoMajor != 2 || string(body) != "done" {
			t.Fatalf("drained HTTP/2 response = proto %s body %q error %v", response.Proto, body, readErr)
		}
	case err := <-responseErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("active HTTP/2 stream did not finish")
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("HTTP/2 shutdown = %v", err)
	}
	if probe, err := net.DialTimeout("tcp", tcpListener.Addr().String(), time.Second); err == nil {
		probe.Close()
		t.Fatal("HTTP/2 Limen accepted a new TCP connection after shutdown")
	}
}
