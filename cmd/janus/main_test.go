package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"janus/internal/config"

	"go.uber.org/zap"
)

// TestRunLifecycle exercises the same run function used by the binary. It
// covers readiness, a real request, file-based route replacement, and signal-
// equivalent context cancellation without relying on a platform-specific
// process launcher.
func TestRunLifecycle(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "backend-ok")
	}))
	defer backend.Close()

	listen := reserveMainTestAddress(t)
	adminAddress := reserveMainTestAddress(t)
	configPath := filepath.Join(t.TempDir(), "janus.json")
	settings := config.DefaultSettings()
	settings.Admin.Address = adminAddress
	settings.Request.MaximumDuration = config.Duration(2 * time.Second)
	settings.Server.WriteTimeout = config.Duration(3 * time.Second)
	settings.Shutdown.DrainTimeout = config.Duration(3 * time.Second)
	current := config.Config{
		Version: config.CurrentConfigVersion,
		Limens: map[string]config.LimenConfig{
			"public": {Address: listen, Protocols: []string{config.ProtocolHTTP1}},
		},
		Settings: settings,
		Services: map[string]config.Service{
			"api": {Upstreams: []string{backend.URL}},
		},
		Routes: []config.Route{{Name: "initial", PathPrefix: "/initial", Service: "api"}},
	}
	writeMainTestConfig(t, configPath, current)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, configPath, false, false, 10*time.Millisecond, zap.NewNop())
	}()

	stopped := false
	defer func() {
		if stopped {
			return
		}
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("run did not stop during test cleanup")
		}
	}()

	waitMainTestResponse(t, "http://"+adminAddress+"/readyz", http.StatusOK, "ok\n")
	waitMainTestResponse(t, "http://"+listen+"/initial", http.StatusOK, "backend-ok")

	current.Routes[0] = config.Route{Name: "reloaded", PathPrefix: "/reloaded", Service: "api"}
	writeMainTestConfig(t, configPath, current)
	waitMainTestResponse(t, "http://"+listen+"/reloaded", http.StatusOK, "backend-ok")

	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop after context cancellation")
	}
}

func waitMainTestResponse(t *testing.T, target string, wantStatus int, wantBody string) {
	t.Helper()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, err := client.Get(target)
		var body []byte
		if response != nil {
			body, err = io.ReadAll(response.Body)
			response.Body.Close()
		}
		if err == nil && response.StatusCode == wantStatus && string(body) == wantBody {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s did not reach status %d with body %q; got status/body %d/%q; last error: %v", target, wantStatus, wantBody, responseStatus(response), string(body), err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func responseStatus(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

func writeMainTestConfig(t *testing.T, path string, c config.Config) {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func reserveMainTestAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
