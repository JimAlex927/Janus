package limen

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"janus/internal/config"
)

func TestReadTLSAssetRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.pem")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", config.MaxTLSAssetBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTLSAsset(path, "certificate"); err == nil {
		t.Fatal("oversized TLS asset was accepted")
	}
}

func TestNewBindingRejectsOversizedTLSCertificate(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t)
	if err := os.WriteFile(certFile, []byte(strings.Repeat("x", config.MaxTLSAssetBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewBinding("public", config.LimenConfig{
		Address:   "127.0.0.1:0",
		Protocols: []string{config.ProtocolHTTP1},
		TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
	}, http.NotFoundHandler(), config.DefaultSettings())
	if err == nil {
		t.Fatal("oversized TLS certificate was accepted at startup")
	}
}
