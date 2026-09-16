package middleware

import "net/http"

// RejectUnsupportedProtocols keeps tunnel and upgrade traffic out of the
// bounded HTTP API handler. It is a global protocol policy and should be
// constructed once outside replaceable route generations.
func RejectUnsupportedProtocols(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
			http.Error(w, "protocol upgrades are not supported", http.StatusNotImplemented)
			return
		}
		next.ServeHTTP(w, r)
	})
}
