package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"janus/internal/config"
)

func TestMTLSDraftRoundTripAndStage(t *testing.T) {
	f := newLibraryFixture(t)
	if res := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"mtls"}`); res.Code != http.StatusCreated {
		t.Fatal(res.Body.String())
	}
	c := testVersionedConfig()
	binding := c.Limens["default"]
	binding.TLS = &config.TLSSettings{CertFile: "server.crt", KeyFile: "server.key", ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: "../certs/client-ca.pem"}
	c.Limens["default"] = binding
	body, err := json.Marshal(map[string]any{"content": c})
	if err != nil {
		t.Fatal(err)
	}
	if res := f.do(t, http.MethodPut, "/api/v1/configs/1", string(body)); res.Code != http.StatusOK {
		t.Fatal(res.Body.String())
	}
	for _, path := range []string{"/api/v1/configs/1"} {
		res := f.do(t, http.MethodGet, path, "")
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"client_auth":"require_and_verify"`) || !strings.Contains(res.Body.String(), `"client_ca_file":"../certs/client-ca.pem"`) {
			t.Fatal(res.Body.String())
		}
	}
	// Publishing application routes cannot silently enable a TLS policy.
	res := f.do(t, http.MethodPost, "/api/v1/configs/1/publish", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "require a file edit and restart") || f.published.Limens["default"].TLS != nil {
		t.Fatal(res.Body.String())
	}
	res = f.do(t, http.MethodPost, "/api/v1/configs/1/stage-limens", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"restart_required":true`) {
		t.Fatal(res.Body.String())
	}
	res = f.do(t, http.MethodGet, "/api/v1/config", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"client_ca_file":"../certs/client-ca.pem"`) {
		t.Fatal(res.Body.String())
	}
}
