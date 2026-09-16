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
