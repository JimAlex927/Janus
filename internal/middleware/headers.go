package middleware

import (
	"net/http"
	"sync"
)

// Headers applies validated end-to-end header mutations. Request headers are
// changed on a clone so sibling handlers cannot observe the mutation. Response
// rules are applied exactly once immediately before headers are committed.
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
	set    map[string]string
	remove []string
	once   sync.Once
}

func (w *headerResponseWriter) apply() {
	w.once.Do(func() { mutateHeaders(w.Header(), w.set, w.remove) })
}

func (w *headerResponseWriter) WriteHeader(status int) {
	w.apply()
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
