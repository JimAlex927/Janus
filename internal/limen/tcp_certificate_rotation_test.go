package limen

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
	"janus/internal/config"
)

func TestHTTPCertificateRotationPreservesEstablishedTLSConnection(t *testing.T) {
	cert, key, roots := writeTestCertificate(t)
	newCert, newKey, _ := writeTestCertificate(t)
	newCertBytes, err := os.ReadFile(newCert)
	if err != nil {
		t.Fatal(err)
	}
	roots.AppendCertsFromPEM(newCertBytes)
	newKeyBytes, err := os.ReadFile(newKey)
	if err != nil {
		t.Fatal(err)
	}
	l, listener := startTestLimen(t, config.LimenConfig{Address: "127.0.0.1:0", Protocols: []string{config.ProtocolHTTP1}, TLS: &config.TLSSettings{CertFile: cert, KeyFile: key}}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))
	reloader, err := NewCertificateReloader(map[string]config.LimenConfig{"public": {TLS: &config.TLSSettings{CertFile: cert, KeyFile: key}}}, map[string]*Limen{"public": l}, 0, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	client := func() *http.Client {
		return &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}}}
	}
	oldClient := client()
	defer oldClient.CloseIdleConnections()
	request := func(c *http.Client) (net.Conn, string) {
		var conn net.Conn
		req, _ := http.NewRequest("GET", "https://"+listener.Addr().String(), nil)
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { conn = info.Conn }}))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return conn, string(resp.TLS.PeerCertificates[0].Raw)
	}
	first, oldLeaf := request(oldClient)
	if err := os.WriteFile(cert, newCertBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := reloader.ReloadOnce(); err == nil {
		t.Fatal("invalid pair accepted")
	}
	if conn, leaf := request(oldClient); conn != first || leaf != oldLeaf {
		t.Fatal("invalid rotation disturbed old connection")
	}
	if err := os.WriteFile(key, newKeyBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := reloader.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	if conn, leaf := request(oldClient); conn != first || leaf != oldLeaf {
		t.Fatal("rotation changed established session")
	}
	fresh := client()
	defer fresh.CloseIdleConnections()
	if conn, leaf := request(fresh); conn == first || leaf == oldLeaf {
		t.Fatal("fresh handshake did not see new cert")
	}
}
