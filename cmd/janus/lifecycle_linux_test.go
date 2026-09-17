//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"janus/internal/config"

	"github.com/quic-go/quic-go/http3"
)

// Test the built entrypoint, not just http.Server.Shutdown: SIGTERM must stop
// new admissions, finish the accepted response, and exit without SIGKILL.
func TestLinuxProcessSIGTERMDrainsAcceptedRequest(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "janus")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "complete")
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	defer finish()
	address := reserveTestAddress(t)
	adminAddress := reserveTestAddress(t)
	settings := config.DefaultSettings()
	settings.Request.MaximumDuration = config.Duration(5 * time.Second)
	settings.Server.WriteTimeout = config.Duration(6 * time.Second)
	settings.Shutdown.DrainTimeout = config.Duration(6 * time.Second)
	settings.Admin.Address = adminAddress
	c := config.Config{Listen: address, Settings: settings, Services: map[string]config.Service{"api": {Upstreams: []string{backend.URL}}}, Routes: []config.Route{{Name: "api", PathPrefix: "/", Service: "api"}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "janus.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-config", path)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			<-processDone
		}
	}()
	client := &http.Client{Timeout: time.Second}
	readyDeadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get("http://" + adminAddress + "/readyz")
		ready := err == nil && resp.StatusCode == http.StatusOK
		if resp != nil {
			resp.Body.Close()
		}
		if ready {
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatal("process never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result := make(chan error, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 8 * time.Second}).Get("http://" + address + "/api")
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			err = readErr
			if err == nil && (resp.StatusCode != 200 || string(body) != "complete") {
				err = io.ErrUnexpectedEOF
			}
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("backend request did not start")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	stopDeadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(stopDeadline) {
			t.Fatal("SIGTERM did not close business listener")
		}
		time.Sleep(10 * time.Millisecond)
	}
	finish()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("accepted response: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("response did not finish")
	}
	select {
	case err := <-processDone:
		waited = true
		if err != nil {
			t.Fatalf("process exit: %v\n%s", err, output.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process exceeded drain deadline")
	}
}

// TestLinuxProcessHTTP3SIGTERMDrainsAcceptedRequest verifies the process
// boundary for the UDP/QUIC adapter. Package-level H3 tests cannot prove that
// the binary binds both sockets, receives SIGTERM, and drains the child
// process using the same lifecycle as production.
func TestLinuxProcessHTTP3SIGTERMDrainsAcceptedRequest(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "janus")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	certFile, keyFile := writeProcessTestCertificate(t, dir)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "h3-complete")
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	defer finish()

	address := reserveDualProtocolAddress(t)
	adminAddress := reserveTestAddress(t)
	settings := config.DefaultSettings()
	settings.Admin.Address = adminAddress
	settings.Request.MaximumDuration = config.Duration(5 * time.Second)
	settings.Server.WriteTimeout = config.Duration(6 * time.Second)
	settings.Shutdown.DrainTimeout = config.Duration(6 * time.Second)
	c := config.Config{
		Version: config.CurrentConfigVersion,
		Limens: map[string]config.LimenConfig{
			"public": {
				Address:   address,
				Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
				TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
			},
		},
		Settings: settings,
		Services: map[string]config.Service{"api": {Upstreams: []string{backend.URL}}},
		Routes:   []config.Route{{Name: "api", PathPrefix: "/", Service: "api"}},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "janus.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-config", path)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			<-processDone
		}
	}()

	client := &http.Client{Timeout: time.Second}
	waitProcessStatus(t, client, "http://"+adminAddress+"/readyz", http.StatusOK)

	transport := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer transport.Close()
	result := make(chan error, 1)
	go func() {
		response, requestErr := (&http.Client{Transport: transport, Timeout: 8 * time.Second}).Get("https://" + address + "/api")
		if requestErr == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			requestErr = readErr
			if requestErr == nil && (response.StatusCode != http.StatusOK || string(body) != "h3-complete") {
				requestErr = io.ErrUnexpectedEOF
			}
		}
		result <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP/3 backend request did not start")
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitProcessStatus(t, client, "http://"+adminAddress+"/readyz", http.StatusServiceUnavailable)
	finish()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("accepted HTTP/3 response: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP/3 response did not finish during drain")
	}
	select {
	case err := <-processDone:
		waited = true
		if err != nil {
			t.Fatalf("process exit: %v\n%s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP/3 process exceeded drain deadline")
	}
}

func waitProcessStatus(t *testing.T, client *http.Client, target string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get(target)
		status := 0
		if response != nil {
			status = response.StatusCode
			response.Body.Close()
		}
		if err == nil && status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s did not reach status %d; got status %d, error %v", target, want, status, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reserveDualProtocolAddress(t *testing.T) string {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		tcp, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := tcp.Addr().(*net.TCPAddr).Port
		address := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
		udp, udpErr := net.ListenPacket("udp", address)
		_ = tcp.Close()
		if udpErr == nil {
			_ = udp.Close()
			return address
		}
	}
	t.Fatal("could not reserve a shared TCP/UDP port")
	return ""
}

func writeProcessTestCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func reserveTestAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	ln.Close()
	return address
}
