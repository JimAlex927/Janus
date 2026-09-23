package runtime

import (
	"errors"
	"net/http"
	"testing"

	"go.uber.org/zap"
	"janus/internal/config"
	"janus/internal/middleware"
)

func TestMTLSPolicyIsStartupOwned(t *testing.T) {
	c := validRuntimeConfig("first")
	c.Version = config.CurrentConfigVersion
	c.Listen = ""
	c.Limens = map[string]config.LimenConfig{"public": {Address: "127.0.0.1:8443", Protocols: []string{"http1"}, TLS: &config.TLSSettings{CertFile: "server.crt", KeyFile: "server.key", ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: "client-ca.pem"}}}
	for i := range c.Routes {
		c.Routes[i].Limen = "public"
	}
	builder := func(_ config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		return &testGeneration{handler: http.NotFoundHandler(), closed: make(chan struct{})}, nil
	}
	r, err := NewWithBuilder(c, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, disable := range []bool{false, true} {
		candidate := c
		binding := c.Limens["public"]
		tlsSettings := *binding.TLS
		binding.TLS = &tlsSettings
		if disable {
			tlsSettings.ClientAuth = config.ClientAuthNone
			tlsSettings.ClientCAFile = ""
		} else {
			tlsSettings.ClientCAFile = "other-ca.pem"
		}
		candidate.Limens = map[string]config.LimenConfig{"public": binding}
		if err := r.Replace(candidate); !errors.Is(err, ErrStartupConfigChanged) {
			t.Fatalf("mTLS change accepted: %v", err)
		}
	}
	if err := r.Replace(c); err != nil {
		t.Fatalf("unchanged mTLS rejected: %v", err)
	}
}
