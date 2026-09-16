package limen

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"testing"

	"janus/internal/config"

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
