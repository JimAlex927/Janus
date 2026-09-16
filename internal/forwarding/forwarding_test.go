package forwarding

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveTrustedMultiHopChain(t *testing.T) {
	policy, err := NewPolicy([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://gateway/api", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.2.0.4")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "public.example:443")

	identity := policy.Resolve(r)
	if identity.ClientIP != "203.0.113.9" || identity.ForwardedFor != "203.0.113.9, 10.2.0.4, 10.0.0.1" || identity.ForwardedProto != "https" || identity.ForwardedHost != "public.example:443" {
		t.Fatalf("trusted identity = %+v", identity)
	}
}

func TestResolveUntrustedPeerIgnoresSpoofedHeaders(t *testing.T) {
	policy, err := NewPolicy([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://gateway/api", nil)
	r.RemoteAddr = "192.0.2.5:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "public.example")

	identity := policy.Resolve(r)
	if identity.ClientIP != "192.0.2.5" || identity.ForwardedFor != "192.0.2.5" || identity.ForwardedProto != "http" || identity.ForwardedHost != "gateway" {
		t.Fatalf("untrusted identity = %+v", identity)
	}
}

func TestResolveMalformedChainFallsBackToPeer(t *testing.T) {
	policy, err := NewPolicy([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://gateway/api", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9,not-an-ip")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "public.example")

	identity := policy.Resolve(r)
	if identity.ClientIP != "10.0.0.1" || identity.ForwardedFor != "10.0.0.1" || identity.ForwardedProto != "https" || identity.ForwardedHost != "public.example" {
		t.Fatalf("malformed identity = %+v", identity)
	}
}
