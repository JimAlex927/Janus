package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"janus/internal/config"
)

func TestVaultTLSForwardingPreservesOriginAndStripsPrefix(t *testing.T) {
	const publicHost = "39.104.66.49:44091"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != publicHost || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("Origin") != "https://"+r.Host {
			http.Error(w, "origin mismatch", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/api/probe" || r.Header.Get("X-Forwarded-Host") != publicHost || r.Header.Get("X-Forwarded-Prefix") != "/vault" {
			t.Errorf("bad proxy request: %s host=%s headers=%v", r.URL, r.Host, r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	// Decode JSON too: strict config decoding and Gateway construction must
	// preserve the new option, not only direct proxy users.
	c, err := config.Load(strings.NewReader(`{"listen":"127.0.0.1:8080","middlewares":{"strip":{"scope":"route","strip_prefix":{"prefix":"/vault"}}},"services":{"vault":{"upstreams":["` + backend.URL + `"],"pass_host_header":true}},"routes":[{"name":"vault","match":"PathPrefix(` + "`/vault`" + `)","middlewares":["strip"],"action":{"forward":{"service":"vault"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewTLSServer(testGatewayWithConfig(t, c))
	defer front.Close()
	r, _ := http.NewRequest(http.MethodPost, front.URL+"/vault/api/probe", strings.NewReader(`{}`))
	r.Host = publicHost
	r.Header.Set("Origin", "https://"+publicHost)
	r.Header.Set("X-Forwarded-Host", "attacker.invalid")
	r.Header.Set("X-Forwarded-Proto", "http")
	res, err := front.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 204 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
}

func TestPassHostHeaderIsPerService(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Host) }))
	defer backend.Close()
	g := testGatewayWithConfig(t, config.Config{
		Listen: "127.0.0.1:8080",
		Services: map[string]config.Service{
			"preserve": {Upstreams: []string{backend.URL}, PassHostHeader: true},
			"default":  {Upstreams: []string{backend.URL}},
		},
		Routes: []config.Route{
			{Name: "preserve", PathPrefix: "/preserve", Service: "preserve"},
			{Name: "default", PathPrefix: "/default", Service: "default"},
		},
	})
	for _, path := range []string{"/preserve", "/default"} {
		r := httptest.NewRequest("GET", "http://public.example:44091"+path, nil)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		want := "public.example:44091"
		if path == "/default" {
			want = strings.TrimPrefix(backend.URL, "http://")
		}
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}
