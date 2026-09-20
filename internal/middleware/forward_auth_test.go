package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForwardAuthAllowsAndCopiesResponseHeaders(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Forwarded-Method") != "POST" {
			t.Errorf("auth request = %s %v", r.Method, r.Header)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{"X-User": {"alice"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	mw, err := ForwardAuth(ForwardAuthOptions{Address: "https://auth.example/check", AuthResponseHeaders: []string{"X-User"}, Client: client, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User") != "alice" {
			t.Errorf("X-User = %q", r.Header.Get("X-User"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodPost, "/orders?id=1", nil)
	r.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestForwardAuthReturnsDenialResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"WWW-Authenticate": {"Bearer"}}, Body: io.NopCloser(strings.NewReader("denied"))}, nil
	})}
	mw, err := ForwardAuth(ForwardAuthOptions{Address: "https://auth.example/check", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if called || response.Code != http.StatusUnauthorized || response.Body.String() != "denied" {
		t.Fatalf("called=%v code=%d body=%q", called, response.Code, response.Body.String())
	}
}

func TestForwardAuthClearsUnverifiedIdentityHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		pattern string
	}{
		{name: "explicit", headers: []string{"X-User", "X-Role"}},
		{name: "regex", pattern: "^X-(User|Role)$"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{"X-User": {"alice"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			mw, err := ForwardAuth(ForwardAuthOptions{Address: "https://auth.example/check", AuthResponseHeaders: tc.headers, AuthResponseHeadersRegex: tc.pattern, Client: client})
			if err != nil {
				t.Fatal(err)
			}
			called := false
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if got := r.Header.Get("X-User"); got != "alice" {
					t.Errorf("verified user = %q", got)
				}
				if got := r.Header.Get("X-Role"); got != "" {
					t.Errorf("unverified role reached backend: %q", got)
				}
				if got := r.Header.Get("X-Trace"); got != "trace" {
					t.Errorf("unselected header = %q", got)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("X-User", "mallory")
			r.Header.Set("X-Role", "admin")
			r.Header.Set("X-Trace", "trace")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, r)
			if !called || response.Code != http.StatusNoContent {
				t.Fatalf("called=%v status=%d", called, response.Code)
			}
		})
	}
}
