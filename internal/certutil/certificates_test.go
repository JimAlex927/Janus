package certutil

import (
	"bytes"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestNoOverwriteOrImplicitCA(t *testing.T) {
	dir := t.TempDir()
	if _, err := IssueClient(dir, "missing.crt", "missing.key", "device", 365); err == nil {
		t.Fatal("missing CA accepted")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("signing implicitly created files")
	}
	ca, err := CreateCA(dir, "test CA", 365)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(ca.KeyFile)
	if _, err := CreateCA(dir, "replacement", 365); err == nil {
		t.Fatal("CA overwritten")
	}
	after, _ := os.ReadFile(ca.KeyFile)
	if !bytes.Equal(before, after) {
		t.Fatal("CA key changed")
	}
	clientDir := filepath.Join(dir, "device")
	client, err := IssueClient(clientDir, ca.CertFile, ca.KeyFile, "device", 730)
	if err != nil {
		t.Fatal(err)
	}
	if client.NotAfter.After(ca.NotAfter) {
		t.Fatal("leaf outlives CA")
	}
	before, _ = os.ReadFile(client.KeyFile)
	if _, err := IssueClient(clientDir, ca.CertFile, ca.KeyFile, "other", 365); err == nil {
		t.Fatal("client overwritten")
	}
	after, _ = os.ReadFile(client.KeyFile)
	if !bytes.Equal(before, after) {
		t.Fatal("client key changed")
	}
	other, err := CreateCA(filepath.Join(dir, "other"), "other", 365)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IssueClient(filepath.Join(dir, "bad"), ca.CertFile, other.KeyFile, "bad", 365); err == nil {
		t.Fatal("mismatched CA accepted")
	}
	if _, err := IssueClient(filepath.Join(dir, "bad"), client.CertFile, client.KeyFile, "bad", 365); err == nil {
		t.Fatal("leaf used as signing CA")
	}
	// Generated root and private key can be loaded for subsequent signing.
	cert, key, err := loadCA(ca.CertFile, ca.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 || key == nil {
		t.Fatal("invalid signing CA")
	}
}

func TestHostsAndOptions(t *testing.T) {
	for _, hosts := range []string{"", "https://example.test", "example.test:443", "example.test/path", "0.0.0.0", "::", "a,,b", "*.example.test", "bad_name", "-bad.test"} {
		if _, err := parseHosts(hosts); err == nil {
			t.Fatalf("bad hosts accepted: %s", hosts)
		}
	}
	if hosts, err := parseHosts("EXAMPLE.test,example.test,127.0.0.1,::1"); err != nil || len(hosts) != 3 {
		t.Fatalf("hosts=%v error=%v", hosts, err)
	}
	for _, days := range []int{-1, 0, 36501} {
		if _, err := CreateCA(t.TempDir(), "test", days); err == nil {
			t.Fatal("invalid validity accepted")
		}
	}
	if _, err := CreateCA(t.TempDir(), "\n", 365); err == nil {
		t.Fatal("control characters accepted")
	}
}

func TestWriteRollbackPreservesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	os.WriteFile(existing, []byte("keep"), 0600)
	first := filepath.Join(dir, "created")
	if err := writeNewFiles([]outputFile{{first, []byte("new")}, {filepath.Join(first, "child"), []byte("bad")}}); err == nil {
		t.Fatal("bad parent accepted")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("partial output not cleaned")
	}
	if got, _ := os.ReadFile(existing); string(got) != "keep" {
		t.Fatal("unrelated file changed")
	}
}

func TestCAAssetsRejectCorruptPEM(t *testing.T) {
	ca, err := CreateCA(t.TempDir(), "test", 365)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(ca.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{
		append([]byte("garbage\n"), valid...),
		append(append([]byte{}, valid...), []byte("garbage")...),
		append([]byte("-----BEGIN CERTIFICATE-----\nbad\n"), valid...),
		append(append([]byte{}, valid...), valid...),
	} {
		if err := singlePEM(data, true); err == nil {
			t.Fatal("malformed CA accepted")
		}
	}
	if err := singlePEM(valid, false); err == nil {
		t.Fatal("certificate accepted as key")
	}
}
