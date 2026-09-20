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
			appendVary(w.Header(), "Accept-Encoding")
			if isUpgrade(r) {
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
	request     *http.Request
	status      int
	finalHeader http.Header
	started     bool
	compress    bool
	gz          *gzip.Writer
}

func (w *compressResponseWriter) WriteHeader(status int) {
	if w.started || w.status != 0 {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.finalHeader = w.Header().Clone()
	if status == http.StatusNoContent || status == http.StatusNotModified || status < 200 {
		w.start(false)
		return
	}
	// Delay the actual header write until the first body byte so content type
	// and pre-existing Content-Encoding can be inspected.
}

func (w *compressResponseWriter) Write(p []byte) (int, error) {
	if !w.started {
		w.restoreHeader()
		if len(p) > 0 && w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
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

func (w *compressResponseWriter) restoreHeader() {
	if w.finalHeader == nil {
		return
	}
	// Declared trailers (and TrailerPrefix fields) may legally be filled in
	// after WriteHeader, including for an empty response finalized by close.
	for _, declaration := range w.finalHeader.Values("Trailer") {
		for _, name := range strings.Split(declaration, ",") {
			name = http.CanonicalHeaderKey(strings.TrimSpace(name))
			w.finalHeader[name] = append([]string(nil), w.Header().Values(name)...)
		}
	}
	for name, values := range w.Header() {
		if strings.HasPrefix(name, http.TrailerPrefix) {
			w.finalHeader[name] = append([]string(nil), values...)
		}
	}
	clear(w.Header())
	for name, values := range w.finalHeader {
		w.Header()[name] = append([]string(nil), values...)
	}
	w.finalHeader = nil
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
	appendVary(w.Header(), "Accept-Encoding")
	if compress {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Accept-Ranges")
		w.Header().Del("Content-MD5")
		if etag := w.Header().Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
			w.Header().Set("ETag", "W/"+etag)
		}
		appendVary(w.Header(), "Accept-Encoding")
		w.gz = gzip.NewWriter(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *compressResponseWriter) shouldCompress() bool {
	if w.status == http.StatusNoContent || w.status == http.StatusNotModified || w.status == http.StatusPartialContent || w.Header().Get("Content-Range") != "" || w.status < 200 {
		return false
	}
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}
	for _, directive := range strings.Split(w.Header().Get("Cache-Control"), ",") {
		if strings.EqualFold(strings.TrimSpace(directive), "no-transform") {
			return false
		}
	}
	if strings.EqualFold(strings.TrimSpace(strings.Split(w.Header().Get("Content-Type"), ";")[0]), "text/event-stream") {
		return false
	}
	if w.request != nil && (w.request.Method == http.MethodHead || w.request.Header.Get("Range") != "" || !acceptsGzip(w.request) || protocolWantsSSE(w.request) || isUpgrade(w.request)) {
		return false
	}
	return true
}

func (w *compressResponseWriter) Flush() {
	_ = w.FlushError()
}

func (w *compressResponseWriter) FlushError() error {
	if !w.started {
		w.restoreHeader()
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.start(w.shouldCompress())
	}
	if w.gz != nil {
		if err := w.gz.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *compressResponseWriter) close() {
	if !w.started {
		w.restoreHeader()
		w.start(false)
	}
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

func (w *compressResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func acceptsGzip(r *http.Request) bool {
	var gzipListed, gzipAllowed, wildcardAllowed bool
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
			if coding == "gzip" {
				gzipListed = true
				gzipAllowed = quality > 0
			} else {
				wildcardAllowed = quality > 0
			}
		}
	}
	// A wildcard only applies to codings that were not explicitly listed.
	if gzipListed {
		return gzipAllowed
	}
	return wildcardAllowed
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
