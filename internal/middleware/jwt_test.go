package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

var testJWTSecret = []byte("01234567890123456789012345678901")

func testJWTOptions() JWTOptions {
	return JWTOptions{
		Secret: testJWTSecret, Algorithms: []string{"HS256"}, Issuer: "https://issuer.example",
		Audience: []string{"janus"}, RequiredClaims: []string{"sub"}, ClockSkew: 30 * time.Second,
	}
}

func signedTestToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	value, err := token.SignedString(testJWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestJWTValidatesBearerTokenAndClaims(t *testing.T) {
	middleware, err := JWT(testJWTOptions())
	if err != nil {
		t.Fatal(err)
	}
	token := signedTestToken(t, jwt.MapClaims{
		"iss": "https://issuer.example", "aud": "janus", "sub": "user-1",
		"exp": float64(time.Now().Add(time.Minute).Unix()),
	})
	called := false
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := JWTClaims(r)["sub"]; got != "user-1" {
			t.Errorf("sub claim = %v", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/orders", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d, want true/204", called, response.Code)
	}
}

func TestJWTForwardsExplicitClaimHeaders(t *testing.T) {
	options := testJWTOptions()
	options.ClaimHeaders = map[string]string{"User": "user", "X-User-ID": "sub", "X-User-Roles": "roles"}
	options.RemoveAuthorization = true
	mw, err := JWT(options)
	if err != nil {
		t.Fatal(err)
	}
	token := signedTestToken(t, jwt.MapClaims{
		"iss": "https://issuer.example", "aud": "janus", "sub": "user-1",
		"user": map[string]any{"id": "user-1", "name": "Jim"}, "roles": []string{"admin"},
		"exp": float64(time.Now().Add(time.Minute).Unix()),
	})
	request := httptest.NewRequest(http.MethodGet, "/orders", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("User", "attacker-value")
	response := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User") != `{"id":"user-1","name":"Jim"}` {
			t.Errorf("user header = %q", r.Header.Get("User"))
		}
		if r.Header.Get("X-User-ID") != "user-1" || r.Header.Get("X-User-Roles") != `["admin"]` {
			t.Errorf("claim headers = %v", r.Header)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization header was not removed")
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestJWTRejectsReservedClaimHeaders(t *testing.T) {
	options := testJWTOptions()
	options.ClaimHeaders = map[string]string{"Authorization": "sub"}
	if _, err := JWT(options); err == nil {
		t.Fatal("accepted Authorization claim header")
	}
}

func TestJWTRejectsInvalidTokensWithoutCallingNext(t *testing.T) {
	middleware, err := JWT(testJWTOptions())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		token string
	}{
		{name: "missing", token: ""},
		{name: "wrong issuer", token: signedTestToken(t, jwt.MapClaims{"iss": "other", "aud": "janus", "sub": "u", "exp": float64(time.Now().Add(time.Minute).Unix())})},
		{name: "expired", token: signedTestToken(t, jwt.MapClaims{"iss": "https://issuer.example", "aud": "janus", "sub": "u", "exp": float64(time.Now().Add(-time.Minute).Unix())})},
		{name: "wrong algorithm", token: strings.Join(strings.Split(signedTestToken(t, jwt.MapClaims{"iss": "https://issuer.example", "aud": "janus", "sub": "u", "exp": float64(time.Now().Add(time.Minute).Unix())}), ".")[:2], ".") + ".invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			request := httptest.NewRequest(http.MethodGet, "/orders", nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if called || response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("called=%v status=%d challenge=%q", called, response.Code, response.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestJWTRequiresCORSToRunBeforeIt(t *testing.T) {
	middleware, err := JWT(testJWTOptions())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodOptions, "/orders", nil)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", "GET")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if called || response.Code != http.StatusUnauthorized {
		t.Fatalf("called=%v status=%d", called, response.Code)
	}
}

func TestJWTLoadsRSAJWKS(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value []byte) string { return base64.RawURLEncoding.EncodeToString(value) }
	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "key-1", "use": "sig", "alg": "RS256",
		"n": encode(privateKey.N.Bytes()), "e": encode([]byte{1, 0, 1}),
	}}}
	data, err := json.Marshal(jwks)
	if err != nil {
		t.Fatal(err)
	}
	options := JWTOptions{JWKSURL: "https://issuer.example/keys", Algorithms: []string{"RS256"}, Issuer: "https://issuer.example", Audience: []string{"janus"}, ClockSkew: time.Second}
	verifier, err := newJWTVerifier(options)
	if err != nil {
		t.Fatal(err)
	}
	verifier.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://issuer.example", "aud": "janus", "sub": "u", "exp": float64(time.Now().Add(time.Minute).Unix())})
	token.Header["kid"] = "key-1"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.verify(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
