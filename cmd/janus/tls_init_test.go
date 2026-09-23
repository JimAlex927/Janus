package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"janus/internal/config"
	"janus/internal/limen"
)

func TestInitializeTLSCreatesVerifiablePairWithoutOverwrite(t *testing.T) {
	configPath := writeTLSInitTestConfig(t, "127.0.0.1:8443")
	result, err := initializeTLS(configPath, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Limen != "secure" || result.CAFingerprint == "" {
		t.Fatalf("unexpected TLS result: %+v", result)
	}
	for _, path := range []string{result.CACertFile, result.CAKeyFile, result.ServerCertFile, result.ServerKeyFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	pair, err := tls.LoadX509KeyPair(result.ServerCertFile, result.ServerKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	server, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(result.CACertFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("generated CA is not a certificate")
	}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		if _, err := server.Verify(x509.VerifyOptions{Roots: roots, DNSName: host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			t.Fatalf("verify %s: %v", host, err)
		}
	}
	c, _, err := config.LoadFileSnapshot(configPath)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := limen.NewBinding("secure", c.LimenBindings()["secure"], http.NotFoundHandler(), c.Settings)
	if err != nil {
		t.Fatalf("Janus did not accept generated certificate: %v", err)
	}
	defer binding.Close()
	if _, err := initializeTLS(configPath, "", ""); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("reinitialization should refuse existing keys, got %v", err)
	}
}

func TestInitializeTLSRequiresExplicitNamesForWildcardBinding(t *testing.T) {
	configPath := writeTLSInitTestConfig(t, "0.0.0.0:8443")
	if _, err := initializeTLS(configPath, "secure", ""); err == nil || !strings.Contains(err.Error(), "-tls-hosts") {
		t.Fatalf("wildcard address should require names, got %v", err)
	}
	result, err := initializeTLS(configPath, "secure", "example.test,127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.ServerCertFile); err != nil {
		t.Fatal(err)
	}
}

func writeTLSInitTestConfig(t *testing.T, address string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "janus.json")
	c := config.Config{
		Version: config.CurrentConfigVersion,
		Limens: map[string]config.LimenConfig{
			"secure": {Address: address, Protocols: []string{config.ProtocolHTTP1}, TLS: &config.TLSSettings{CertFile: "tls/server.crt", KeyFile: "tls/server.key"}},
		},
		Routes: []config.Route{{Name: "ok", Limen: "secure", Match: "PathPrefix(`/`)", Action: &config.RouteAction{Respond: &config.RespondAction{Status: 200}}}},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
