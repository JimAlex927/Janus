// Package middleware contains the small, built-in HTTP middleware pipeline.
package middleware

import "net/http"

// Middleware transforms an HTTP handler. Middleware are applied in declaration
// order: Chain(final, a, b) enters a, then b, then final.
type Middleware func(http.Handler) http.Handler

// Chain builds an immutable handler chain once at startup or reload.
func Chain(final http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i] == nil {
			continue
		}
		final = middlewares[i](final)
	}
	return final
}

// BodyLimit bounds the request body before it is sent to the backend. Known
// content lengths are rejected without invoking next; chunked and otherwise
// unknown-length bodies are limited while they are read by the downstream
// handler. The standard library error is preserved so the proxy can map it to
// 413 when the body exceeds the limit after forwarding has started.
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 && r.ContentLength > maxBytes {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if maxBytes <= 0 || r.Body == nil || r.Body == http.NoBody {
				next.ServeHTTP(w, r)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
