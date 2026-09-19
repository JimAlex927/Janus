package middleware

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"

	"janus/internal/telemetry"
)

const (
	maxJWTTokenBytes = 16 << 10
	maxJWKSBytes     = 1 << 20
	jwtKeyTTL        = 5 * time.Minute
	jwtStaleTTL      = 15 * time.Minute
)

// JWTOptions describes an immutable JWT authentication policy. Exactly one key
// source must be set and Algorithms must be an explicit allowlist.
type JWTOptions struct {
	JWKSURL        string
	PublicKeyFile  string
	Secret         []byte
	Algorithms     []string
	Issuer         string
	Audience       []string
	RequiredClaims []string
	ClockSkew      time.Duration
}

// JWTClaims returns verified claims installed by JWT middleware. Claims are
// deliberately not copied into upstream headers; callers must make that trust
// decision explicitly in a separate policy.
func JWTClaims(r *http.Request) map[string]any {
	if r == nil {
		return nil
	}
	claims, _ := r.Context().Value(jwtClaimsKey{}).(map[string]any)
	return claims
}

type jwtClaimsKey struct{}

type jwtVerifier struct {
	opts       JWTOptions
	staticKey  any
	client     *http.Client
	cacheMu    sync.Mutex
	keys       map[string]jwkKey
	fetchedAt  time.Time
	staleUntil time.Time
}

// JWT constructs a verifier once per routing generation. JWKS retrieval is
// lazy so a temporary identity-provider outage does not prevent unrelated
// static routes from being validated during process startup.
func JWT(options JWTOptions) (Middleware, error) {
	v, err := newJWTVerifier(options)
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, ok := bearerToken(r.Header)
			if !ok || len(tokenString) > maxJWTTokenBytes {
				telemetry.MarkError(r.Context(), "jwt_unauthorized")
				rejectJWT(w)
				return
			}
			claims, err := v.verify(tokenString)
			if err != nil {
				telemetry.MarkError(r.Context(), "jwt_unauthorized")
				rejectJWT(w)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), jwtClaimsKey{}, claims)))
		})
	}, nil
}

func bearerToken(header http.Header) (string, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func rejectJWT(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func newJWTVerifier(options JWTOptions) (*jwtVerifier, error) {
	if options.ClockSkew < 0 || options.ClockSkew > 5*time.Minute {
		return nil, errors.New("jwt clock_skew must be between 0 and 5m")
	}
	if len(options.Algorithms) == 0 || len(options.Algorithms) > 8 {
		return nil, errors.New("jwt requires 1 to 8 allowed algorithms")
	}
	allowed := make(map[string]struct{}, len(options.Algorithms))
	for _, algorithm := range options.Algorithms {
		if !supportedJWTAlgorithm(algorithm) {
			return nil, fmt.Errorf("jwt algorithm %q is not supported", algorithm)
		}
		if _, exists := allowed[algorithm]; exists {
			return nil, fmt.Errorf("jwt algorithm %q is duplicated", algorithm)
		}
		allowed[algorithm] = struct{}{}
	}
	if strings.TrimSpace(options.Issuer) == "" || strings.ContainsAny(options.Issuer, "\r\n") {
		return nil, errors.New("jwt issuer is required")
	}
	if len(options.Audience) == 0 {
		return nil, errors.New("jwt requires at least one audience")
	}
	if len(options.JWKSURL) > 0 && (options.PublicKeyFile != "" || len(options.Secret) > 0) {
		return nil, errors.New("jwt key sources are mutually exclusive")
	}
	if options.PublicKeyFile != "" && len(options.Secret) > 0 {
		return nil, errors.New("jwt key sources are mutually exclusive")
	}
	if options.JWKSURL == "" && options.PublicKeyFile == "" && len(options.Secret) == 0 {
		return nil, errors.New("jwt requires one key source")
	}
	for _, algorithm := range options.Algorithms {
		if len(options.Secret) > 0 && !strings.HasPrefix(algorithm, "HS") {
			return nil, errors.New("jwt secret key source only supports HS algorithms")
		}
		if len(options.Secret) == 0 && strings.HasPrefix(algorithm, "HS") {
			return nil, errors.New("jwt HS algorithms require secret_env")
		}
	}
	v := &jwtVerifier{opts: options, client: &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("jwks redirect must remain HTTPS")
			}
			if len(via) > 0 && request.URL.Host != via[0].URL.Host {
				return errors.New("jwks redirect cannot change host")
			}
			return nil
		},
	}}
	if options.PublicKeyFile != "" {
		data, err := os.ReadFile(options.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read jwt public key: %w", err)
		}
		key, err := parsePublicKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse jwt public key: %w", err)
		}
		v.staticKey = key
	}
	if len(options.Secret) > 0 {
		if len(options.Secret) < 32 {
			return nil, errors.New("jwt HMAC secret must be at least 32 bytes")
		}
		v.staticKey = append([]byte(nil), options.Secret...)
	}
	return v, nil
}

func supportedJWTAlgorithm(algorithm string) bool {
	switch algorithm {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA", "HS256", "HS384", "HS512":
		return true
	default:
		return false
	}
}

func (v *jwtVerifier) verify(raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(parts[0]) == 0 || len(parts[1]) == 0 || len(parts[2]) == 0 {
		return nil, errors.New("invalid jwt compact form")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(headerBytes) > 4<<10 || hasDuplicateJSONKeys(headerBytes) {
		return nil, errors.New("invalid jwt header")
	}
	var header struct {
		Algorithm string   `json:"alg"`
		KeyID     string   `json:"kid"`
		Critical  []string `json:"crit"`
		JWKURL    string   `json:"jku"`
		X5U       string   `json:"x5u"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm == "" || len(header.Critical) > 0 || header.JWKURL != "" || header.X5U != "" {
		return nil, errors.New("invalid jwt header")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payloadBytes) > maxJWTTokenBytes || hasDuplicateJSONKeys(payloadBytes) {
		return nil, errors.New("invalid jwt claims")
	}
	claims := jwt.MapClaims{}
	parserOptions := []jwt.ParserOption{
		jwt.WithValidMethods(v.opts.Algorithms),
		jwt.WithIssuer(v.opts.Issuer),
		jwt.WithAudience(v.opts.Audience...),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.opts.ClockSkew),
	}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != header.Algorithm {
			return nil, errors.New("jwt algorithm mismatch")
		}
		return v.key(header.KeyID, header.Algorithm)
	}, parserOptions...)
	if err != nil || token == nil || !token.Valid {
		return nil, errors.New("jwt verification failed")
	}
	for _, name := range v.opts.RequiredClaims {
		value, exists := claims[name]
		if !exists || value == nil || (valueString(value) == "" && name != "exp") {
			return nil, errors.New("jwt required claim missing")
		}
	}
	result := make(map[string]any, len(claims))
	for name, value := range claims {
		result[name] = value
	}
	return result, nil
}

func valueString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func (v *jwtVerifier) key(kid, algorithm string) (any, error) {
	if v.staticKey != nil {
		if !keyMatchesAlgorithm(v.staticKey, algorithm) {
			return nil, errors.New("jwt key type does not match algorithm")
		}
		return v.staticKey, nil
	}
	if err := v.refresh(false); err != nil {
		return nil, err
	}
	key, ok := v.cachedKey(kid, algorithm)
	if !ok {
		if err := v.refresh(true); err != nil {
			return nil, err
		}
		key, ok = v.cachedKey(kid, algorithm)
	}
	if !ok {
		return nil, fmt.Errorf("jwt key %q not found", kid)
	}
	if !keyMatchesAlgorithm(key.key, algorithm) {
		return nil, errors.New("jwt key type does not match algorithm")
	}
	return key.key, nil
}

func (v *jwtVerifier) cachedKey(kid, algorithm string) (jwkKey, bool) {
	v.cacheMu.Lock()
	defer v.cacheMu.Unlock()
	key, ok := v.keys[kid]
	if !ok && kid == "" && len(v.keys) == 1 {
		for _, candidate := range v.keys {
			key = candidate
		}
		ok = true
	}
	if key.algorithm != "" && key.algorithm != algorithm {
		return jwkKey{}, false
	}
	return key, ok
}

func keyMatchesAlgorithm(key any, algorithm string) bool {
	switch {
	case strings.HasPrefix(algorithm, "RS"), strings.HasPrefix(algorithm, "PS"):
		_, ok := key.(*rsa.PublicKey)
		return ok
	case strings.HasPrefix(algorithm, "ES"):
		_, ok := key.(*ecdsa.PublicKey)
		return ok
	case algorithm == "EdDSA":
		_, ok := key.(ed25519.PublicKey)
		return ok
	case strings.HasPrefix(algorithm, "HS"):
		_, ok := key.([]byte)
		return ok
	default:
		return false
	}
}

type jwkDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwkKey struct {
	key       any
	algorithm string
}

func (v *jwtVerifier) refresh(force bool) error {
	v.cacheMu.Lock()
	defer v.cacheMu.Unlock()
	now := time.Now()
	if !force && len(v.keys) > 0 && now.Before(v.fetchedAt.Add(jwtKeyTTL)) {
		return nil
	}
	if force && len(v.keys) > 0 && now.Sub(v.fetchedAt) < 30*time.Second {
		return nil
	}
	request, err := http.NewRequest(http.MethodGet, v.opts.JWKSURL, nil)
	if err != nil {
		return err
	}
	response, err := v.client.Do(request)
	if err != nil {
		if len(v.keys) > 0 && now.Before(v.staleUntil) {
			return nil
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if len(v.keys) > 0 && now.Before(v.staleUntil) {
			return nil
		}
		return fmt.Errorf("jwks endpoint returned status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxJWKSBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > maxJWKSBytes || hasDuplicateJSONKeys(data) {
		if len(v.keys) > 0 && now.Before(v.staleUntil) {
			return nil
		}
		return errors.New("invalid jwks response")
	}
	var document jwkDocument
	if err := json.Unmarshal(data, &document); err != nil || len(document.Keys) == 0 {
		if len(v.keys) > 0 && now.Before(v.staleUntil) {
			return nil
		}
		return errors.New("invalid jwks response")
	}
	keys := make(map[string]jwkKey, len(document.Keys))
	for _, item := range document.Keys {
		if item.Kid == "" || item.Use != "" && item.Use != "sig" {
			continue
		}
		key, err := parseJWK(item)
		if err != nil {
			continue
		}
		if _, exists := keys[item.Kid]; exists {
			return errors.New("jwks contains duplicate key ids")
		}
		keys[item.Kid] = jwkKey{key: key, algorithm: item.Alg}
	}
	if len(keys) == 0 {
		if len(v.keys) > 0 && now.Before(v.staleUntil) {
			return nil
		}
		return errors.New("jwks contains no usable signing keys")
	}
	v.keys, v.fetchedAt, v.staleUntil = keys, now, now.Add(jwtStaleTTL)
	return nil
}

func parseJWK(item jwk) (any, error) {
	decode := func(value string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(value) }
	switch item.Kty {
	case "RSA":
		n, err := decode(item.N)
		if err != nil || len(n) == 0 {
			return nil, errors.New("invalid rsa modulus")
		}
		e, err := decode(item.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("invalid rsa exponent")
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent<<8 | int(b)
		}
		if exponent < 3 || exponent%2 == 0 {
			return nil, errors.New("invalid rsa exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}, nil
	case "EC":
		x, err := decode(item.X)
		if err != nil {
			return nil, err
		}
		y, err := decode(item.Y)
		if err != nil {
			return nil, err
		}
		var curve elliptic.Curve
		switch item.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, errors.New("unsupported ec curve")
		}
		if !curve.IsOnCurve(new(big.Int).SetBytes(x), new(big.Int).SetBytes(y)) {
			return nil, errors.New("ec point is not on curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	case "OKP":
		if item.Crv != "Ed25519" {
			return nil, errors.New("unsupported okp curve")
		}
		x, err := decode(item.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, errors.New("invalid ed25519 key")
		}
		return ed25519.PublicKey(x), nil
	default:
		return nil, errors.New("unsupported jwk type")
	}
}

func parsePublicKey(data []byte) (any, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("public key is not PEM encoded")
	}
	if block.Type == "CERTIFICATE" {
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return certificate.PublicKey, nil
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported public key encoding")
}

func hasDuplicateJSONKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !walkJSON(decoder) {
		return true
	}
	var extra any
	return decoder.Decode(&extra) != io.EOF
}

func walkJSON(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			keys := map[string]struct{}{}
			for decoder.More() {
				rawKey, err := decoder.Token()
				key, ok := rawKey.(string)
				if err != nil {
					return false
				}
				if !ok {
					return false
				}
				if _, exists := keys[key]; exists {
					return false
				}
				keys[key] = struct{}{}
				if !walkJSON(decoder) {
					return false
				}
			}
			rawEnd, err := decoder.Token()
			end, ok := rawEnd.(json.Delim)
			if err != nil {
				return false
			}
			return ok && end == '}'
		case '[':
			for decoder.More() {
				if !walkJSON(decoder) {
					return false
				}
			}
			rawEnd, err := decoder.Token()
			end, ok := rawEnd.(json.Delim)
			if err != nil {
				return false
			}
			return ok && end == ']'
		}
	}
	return true
}
