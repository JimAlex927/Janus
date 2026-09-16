package limen

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"

	"janus/internal/config"
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
