package middleware

import (
	"net/http"
	"net/url"
)

// AddPrefix prepends a validated absolute path prefix while preserving an
// explicitly escaped request path. The original request remains unchanged.
func AddPrefix(prefix string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.Clone(r.Context())
			u := *r.URL
			path := u.Path
			if path == "" {
				path = "/"
			}
			u.Path = prefix + path
			if u.RawPath != "" {
				escapedPrefix := (&url.URL{Path: prefix}).EscapedPath()
				u.RawPath = escapedPrefix + u.RawPath
			}
			r.URL = &u
			next.ServeHTTP(w, r)
		})
	}
}
