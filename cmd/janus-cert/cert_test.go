package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/limen"
	"software.sslmate.com/src/go-pkcs12"
)

func TestCertCLIHelper(t *testing.T) {
	if os.Getenv("JANUS_TEST_CERT_CLI") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"janus-cert"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(99)
}

func TestIndependentCertCommandsAndMTLS(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir() // Intentionally no gateway config, even at the default path.
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-test.run=^TestCertCLIHelper$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "JANUS_TEST_CERT_CLI=1")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI failed: %v %s", err, out)
		}
		return string(out)
	}
	run("ca", "--out", "pki", "--name", "Jim private CA")
	entries, err := os.ReadDir(filepath.Join(dir, "pki"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("CA command must create only CA pair: %v", err)
	}
	caBefore, _ := os.ReadFile(filepath.Join(dir, "pki", "ca.crt"))
	run("server", "--ca", "pki/ca.crt", "--ca-key", "pki/ca.key", "--hosts", "39.104.66.49,localhost,127.0.0.1", "--out", "server")
	out := run("client", "--ca", "pki/ca.crt", "--ca-key", "pki/ca.key", "--name", "jim-mac", "--out", "mac")
	password, _ := os.ReadFile(filepath.Join(dir, "mac", "client-password.txt"))
	secret := strings.TrimSpace(string(password))
	if len(secret) != 48 || strings.Contains(out, secret) || strings.Contains(out, "BEGIN PRIVATE KEY") {
		t.Fatal("invalid or leaked password")
	}
	p12, _ := os.ReadFile(filepath.Join(dir, "mac", "client.p12"))
	key, cert, chain, err := pkcs12.DecodeChain(p12, secret)
	if err != nil || key == nil || len(chain) != 1 {
		t.Fatalf("P12: %v", err)
	}
	if cert.Subject.CommonName != "jim-mac" || cert.IsCA || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatal("wrong client identity/role")
	}
	if _, _, _, err := pkcs12.DecodeChain(p12, "wrong"); err == nil {
		t.Fatal("P12 accepted wrong password")
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caBefore)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("client accepted as server")
	}
	serverPath := filepath.Join(dir, "server", "server.crt")
	serverKey := filepath.Join(dir, "server", "server.key")
	server, err := tls.LoadX509KeyPair(serverPath, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"39.104.66.49", "localhost", "127.0.0.1"} {
		if err := server.Leaf.VerifyHostname(host); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.Leaf.VerifyHostname("different.test"); err == nil {
		t.Fatal("SAN accepts unrelated name")
	}
	if _, err := server.Leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("server accepted as client")
	}
	run("client", "--ca", "pki/ca.crt", "--ca-key", "pki/ca.key", "--name", "jim-phone", "--out", "phone")
	caAfter, _ := os.ReadFile(filepath.Join(dir, "pki", "ca.crt"))
	if !bytes.Equal(caBefore, caAfter) {
		t.Fatal("signing changed CA")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{"pki/ca.key", "mac/client.key", "mac/client.p12", "mac/client-password.txt"} {
			info, err := os.Stat(filepath.Join(dir, path))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("secret mode: %s %v", path, err)
			}
		}
	}
	// The independent output can then be used by Janus; no config was needed
	// for issuance. Authenticate against the SAME CA on both sides.
	settings := &config.TLSSettings{CertFile: serverPath, KeyFile: serverKey, ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: filepath.Join(dir, "pki", "ca.crt")}
	l, err := limen.NewBinding("test", config.LimenConfig{Address: "127.0.0.1:0", Protocols: []string{"http1"}, TLS: settings}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.VerifiedChains) == 0 {
			t.Error("no verified client")
		}
		w.WriteHeader(204)
	}), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- l.Serve(listener) }()
	defer func() { l.Close(); <-done }()
	client, err := tls.LoadX509KeyPair(filepath.Join(dir, "mac", "client.crt"), filepath.Join(dir, "mac", "client.key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, authenticated := range []bool{true, false} {
		cfg := &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}
		if authenticated {
			cfg.Certificates = []tls.Certificate{client}
		}
		tr := &http.Transport{TLSClientConfig: cfg}
		defer tr.CloseIdleConnections()
		res, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Get("https://" + listener.Addr().String())
		if authenticated {
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 204 {
				t.Fatal(res.Status)
			}
		} else if err == nil {
			res.Body.Close()
			t.Fatal("anonymous client accepted")
		}
	}
}

func TestCertCommandValidation(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"}, {"ca"}, {"server", "--out", t.TempDir()}, {"client", "--out", t.TempDir()},
		{"ca", "--out", t.TempDir(), "--config", "missing.json"},
		{"ca", "--out", t.TempDir(), "extra"},
	} {
		if err := runCertCommand(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	for _, args := range [][]string{nil, {"--help"}, {"ca", "--help"}, {"server", "--help"}, {"client", "--help"}} {
		if err := runCertCommand(args, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
}
