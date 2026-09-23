package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestMTLSConfigValidationAndPaths(t *testing.T) {
	base := `{"version":1,"limens":{"public":{"address":"127.0.0.1:8443","protocols":["http1"],"tls":{"cert_file":"server.crt","key_file":"server.key"EXTRA}}},"routes":[{"name":"r","limen":"public","match":"PathPrefix(` + "`/`" + `)","action":{"respond":{"status":200}}}]}`
	for _, tc := range []struct {
		name, extra string
		valid       bool
	}{
		{"default", "", true},
		{"none", `,"client_auth":"none"`, true},
		{"mtls", `,"client_auth":"require_and_verify","client_ca_file":"client-ca.pem"`, true},
		{"missing CA", `,"client_auth":"require_and_verify"`, false},
		{"blank CA", `,"client_auth":"require_and_verify","client_ca_file":"  "`, false},
		{"CA without mode", `,"client_ca_file":"client-ca.pem"`, false},
		{"CA with none", `,"client_auth":"none","client_ca_file":"client-ca.pem"`, false},
		{"unknown", `,"client_auth":"require_any"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := LoadFileBytes("/tmp/janus/config.json", []byte(strings.Replace(base, "EXTRA", tc.extra, 1)))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if err != nil {
				return
			}
			tls := c.Limens["public"].TLS
			if tc.name == "mtls" {
				if tls.ClientCAFile != filepath.Join("/tmp/janus", "client-ca.pem") {
					t.Fatalf("CA path=%s", tls.ClientCAFile)
				}
				view, err := json.Marshal(c.EffectiveView())
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(view), `"client_auth":"require_and_verify"`) || strings.Contains(string(view), "client-ca.pem") || strings.Contains(string(view), "client_ca_file") {
					t.Fatalf("effective view=%s", view)
				}
			} else if tls.ClientCAFile != "" {
				t.Fatal("empty CA path was resolved")
			}
		})
	}
	abs := filepath.Join(t.TempDir(), "client-ca.pem")
	extra, _ := json.Marshal(TLSSettings{CertFile: "server.crt", KeyFile: "server.key", ClientAuth: ClientAuthRequireAndVerify, ClientCAFile: abs})
	body := strings.Replace(base, `{"cert_file":"server.crt","key_file":"server.key"EXTRA}`, string(extra), 1)
	c, err := LoadFileBytes("/tmp/janus/config.json", []byte(body))
	if err != nil || c.Limens["public"].TLS.ClientCAFile != abs {
		t.Fatalf("absolute CA path changed: %v", err)
	}
}
