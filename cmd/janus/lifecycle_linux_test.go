//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
