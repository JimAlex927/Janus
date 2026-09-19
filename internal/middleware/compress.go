package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type CompressOptions struct{}

// Compress negotiates gzip for ordinary buffered responses. Streaming,
// upgraded, empty, and already encoded responses are deliberately untouched.
func Compress(_ CompressOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead || isUpgrade(r) || acceptsGzip(r) == false {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressResponseWriter{ResponseWriter: w, request: r}
			next.ServeHTTP(cw, r)
			cw.close()
		})
	}
}

type compressResponseWriter struct {
	http.ResponseWriter
	request  *http.Request
	status   int
	started  bool
	compress bool
	gz       *gzip.Writer
}

func (w *compressResponseWriter) WriteHeader(status int) {
	if w.started {
		return
	}
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	if status == http.StatusNoContent || status == http.StatusNotModified || status < 200 {
		w.start(false)
		return
	}
	// Delay the actual header write until the first body byte so content type
	// and pre-existing Content-Encoding can be inspected.
}

func (w *compressResponseWriter) Write(p []byte) (int, error) {
	if !w.started {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.start(w.shouldCompress())
	}
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *compressResponseWriter) start(compress bool) {
	if w.started {
		return
	}
	w.started = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.compress = compress
	if compress {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		appendVary(w.Header(), "Accept-Encoding")
		w.gz = gzip.NewWriter(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *compressResponseWriter) shouldCompress() bool {
	if w.status == http.StatusNoContent || w.status == http.StatusNotModified || w.status < 200 {
		return false
	}
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(strings.Split(w.Header().Get("Content-Type"), ";")[0]), "text/event-stream") {
		return false
	}
	if w.request != nil && (protocolWantsSSE(w.request) || isUpgrade(w.request)) {
		return false
	}
	return true
}

func (w *compressResponseWriter) Flush() {
	if !w.started {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.start(w.shouldCompress())
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *compressResponseWriter) close() {
	if !w.started {
		w.start(false)
	}
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

func (w *compressResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func acceptsGzip(r *http.Request) bool {
	for _, value := range r.Header.Values("Accept-Encoding") {
		for _, part := range strings.Split(value, ",") {
			pieces := strings.Split(strings.TrimSpace(part), ";")
			coding := strings.ToLower(strings.TrimSpace(pieces[0]))
			if coding != "gzip" && coding != "*" {
				continue
			}
			quality := 1.0
			for _, parameter := range pieces[1:] {
				key, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				if ok && strings.EqualFold(key, "q") {
					if parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
						if parsed < 0 || parsed > 1 {
							quality = 0
						} else {
							quality = parsed
						}
					} else {
						quality = 0
					}
				}
			}
			if quality > 0 {
				return true
			}
		}
	}
	return false
}

func isUpgrade(r *http.Request) bool {
	return r != nil && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func protocolWantsSSE(r *http.Request) bool {
	for _, value := range r.Header.Values("Accept") {
		if strings.Contains(strings.ToLower(value), "text/event-stream") {
			return true
		}
	}
	return false
}

var _ io.Writer = (*compressResponseWriter)(nil)
