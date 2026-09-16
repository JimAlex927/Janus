package limen

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"testing"

	"janus/internal/config"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"go.uber.org/zap"
)

func TestCertificateReloaderPublishesOnlyValidatedPairs(t *testing.T) {
	certFile, keyFile, rootsOne := writeTestCertificate(t)
	secondCert, secondKey, rootsTwo := writeTestCertificate(t)
	l, listener := startTestLimen(t, config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	reloader, err := NewCertificateReloader(
		map[string]config.LimenConfig{"public": {TLS: &config.TLSSettings{CertFile: certFile, KeyFile: keyFile}}},
		map[string]*Limen{"public": l}, 0, zap.NewNop(),
	)
	if err != nil {
		t.Fatal(err)
	}

	secondCertBytes, err := os.ReadFile(secondCert)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, secondCertBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloader.ReloadOnce(); err == nil {
		t.Fatal("expected incomplete pair rejection")
	}
	requestWithRoots(t, listener.Addr().String(), rootsOne)

	secondKeyBytes, err := os.ReadFile(secondKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, secondKeyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloader.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	requestWithRoots(t, listener.Addr().String(), rootsTwo)
}

func TestHTTP3CertificateRotationKeepsExistingConnectionAndUpdatesNewHandshake(t *testing.T) {
	certFile, keyFile, rootsOne := writeTestCertificate(t)
	secondCert, secondKey, rootsTwo := writeTestCertificate(t)
	l, packet, serveDone := startHTTP3TestLimen(t, certFile, keyFile)
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	})

	firstTransport := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: rootsOne, ServerName: "localhost"},
		QUICConfig:      &quic.Config{Allow0RTT: false},
	}
	defer firstTransport.Close()
	firstClient := &http.Client{Transport: firstTransport}
	requestHTTP3Body(t, firstClient, packet.LocalAddr().String(), "before")

	if err := l.RotateCertificate(secondCert, secondKey); err != nil {
		t.Fatal(err)
	}
	// The existing QUIC connection has already completed TLS and must remain
	// usable after content rotation.
	requestHTTP3Body(t, firstClient, packet.LocalAddr().String(), "existing")

	secondTransport := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: rootsTwo, ServerName: "localhost"},
		QUICConfig:      &quic.Config{Allow0RTT: false},
	}
	defer secondTransport.Close()
	requestHTTP3Body(t, &http.Client{Transport: secondTransport}, packet.LocalAddr().String(), "new")
}

func startHTTP3TestLimen(t *testing.T, certFile, keyFile string) (*Limen, net.PacketConn, <-chan error) {
	t.Helper()
	l, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.URL.Path)
	}), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	packet, err := l.ListenPacket()
	if err != nil {
		_ = tcp.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcp) }()
	return l, packet, serveDone
}

func requestHTTP3Body(t *testing.T, client *http.Client, address, path string) {
	t.Helper()
	response, err := client.Get("https://" + address + "/" + path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 3 || string(body) != "/"+path {
		t.Fatalf("HTTP/3 response = proto %s body %q", response.Proto, body)
	}
}

func requestWithRoots(t *testing.T, address string, roots *x509.CertPool) {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Get("https://" + address + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
