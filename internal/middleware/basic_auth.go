package middleware

import (
	"net/http"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type BasicAuthOptions struct {
	Realm        string
	Users        map[string]string
	RemoveHeader bool
}

// BasicAuthUsers returns usernames in deterministic order for diagnostics and
// tests without exposing password hashes.

func BasicAuth(options BasicAuthOptions) (Middleware, error) {
	if strings.TrimSpace(options.Realm) == "" || strings.ContainsAny(options.Realm, "\r\n") {
		return nil, errInvalidBasicAuth
	}
	if len(options.Users) == 0 {
		return nil, errInvalidBasicAuth
	}
	users := make(map[string][]byte, len(options.Users))
	for username, hash := range options.Users {
		if username == "" || strings.ContainsAny(username, "\r\n:") || hash == "" {
			return nil, errInvalidBasicAuth
		}
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			return nil, errInvalidBasicAuth
		}
		users[username] = []byte(hash)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()
			hash, exists := users[username]
			if !ok || !exists || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+escapeRealm(options.Realm)+`"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if options.RemoveHeader {
				r = r.Clone(r.Context())
				r.Header = r.Header.Clone()
				r.Header.Del("Authorization")
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

var errInvalidBasicAuth = &basicAuthError{}

type basicAuthError struct{}

func (*basicAuthError) Error() string { return "invalid basic_auth configuration" }

func escapeRealm(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}

func BasicAuthUsers(options BasicAuthOptions) []string {
	result := make([]string, 0, len(options.Users))
	for username := range options.Users {
		result = append(result, username)
	}
	sort.Strings(result)
	return result
}
