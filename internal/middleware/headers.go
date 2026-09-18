package middleware

import (
	"net/http"
)

// Headers applies validated end-to-end header mutations. Request headers are
// changed on a clone so sibling handlers cannot observe the mutation. Response
// rules are applied exactly once immediately before a normal HTTP response is
// committed. A 101 Switching Protocols response is deliberately left alone:
// its handshake headers are owned by the upgrade implementation.
func Headers(requestSet map[string]string, requestRemove []string, responseSet map[string]string, responseRemove []string) Middleware {
	requestSet = cloneStringMap(requestSet)
	requestRemove = append([]string(nil), requestRemove...)
	responseSet = cloneStringMap(responseSet)
	responseRemove = append([]string(nil), responseRemove...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(requestSet) > 0 || len(requestRemove) > 0 {
				r = r.Clone(r.Context())
				r.Header = r.Header.Clone()
				mutateHeaders(r.Header, requestSet, requestRemove)
			}
			if len(responseSet) == 0 && len(responseRemove) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			wrapped := &headerResponseWriter{ResponseWriter: w, set: responseSet, remove: responseRemove}
			next.ServeHTTP(wrapped, r)
			// A handler may return without writing. Apply now so net/http's implicit
			// 200 response still carries the configured response headers.
			wrapped.apply()
		})
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func mutateHeaders(header http.Header, set map[string]string, remove []string) {
	for _, name := range remove {
		header.Del(name)
	}
	for name, value := range set {
		header.Set(name, value)
	}
}

type headerResponseWriter struct {
	http.ResponseWriter
	set     map[string]string
	remove  []string
	applied bool
}

func (w *headerResponseWriter) apply() {
	if w.applied {
		return
	}
	w.applied = true
	mutateHeaders(w.Header(), w.set, w.remove)
}

func (w *headerResponseWriter) WriteHeader(status int) {
	// A WebSocket (or other HTTP/1 upgrade) handshake has protocol-defined
	// response fields such as Sec-WebSocket-Accept. Do not let a generic
	// header policy alter it, and consume once so the deferred apply after the
	// handler returns cannot mutate a response that has already been committed.
	if status == http.StatusSwitchingProtocols {
		w.applied = true
		w.ResponseWriter.WriteHeader(status)
		return
	}
	// Informational responses are not the final response. Keep the policy for
	// the later final WriteHeader/Write/Flush call instead.
	if status >= http.StatusOK {
		w.apply()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *headerResponseWriter) Write(body []byte) (int, error) {
	w.apply()
	return w.ResponseWriter.Write(body)
}

func (w *headerResponseWriter) Flush() {
	w.apply()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController retain streaming, hijacking and deadline
// support offered by the original server ResponseWriter.
func (w *headerResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
