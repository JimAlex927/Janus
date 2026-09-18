package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// StripPrefix removes prefix only on a path-segment boundary. Requests that do
// not carry the configured prefix pass through unchanged, which keeps the
// middleware safe when a route match is broader than the rewrite rule.
func StripPrefix(prefix string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
				next.ServeHTTP(w, r)
				return
			}
			r = r.Clone(r.Context())
			u := *r.URL
			u.Path = strings.TrimPrefix(u.Path, prefix)
			if u.Path == "" {
				u.Path = "/"
			}
			if u.RawPath != "" {
				escapedPrefix := (&url.URL{Path: prefix}).EscapedPath()
				switch {
				case u.RawPath == escapedPrefix:
					u.RawPath = "/"
				case strings.HasPrefix(u.RawPath, escapedPrefix+"/"):
					u.RawPath = strings.TrimPrefix(u.RawPath, escapedPrefix)
				default:
					// A non-canonical escape spelling cannot be rewritten safely.
					u.RawPath = ""
				}
			}
			r.URL = &u
			r.Header = r.Header.Clone()
			r.Header.Set("X-Forwarded-Prefix", prefix)
			next.ServeHTTP(w, r)
		})
	}
}
