package middleware

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"janus/internal/protocol"
	"janus/internal/telemetry"

	"go.uber.org/zap"
)

var fallbackRequestID uint64

// Observe creates a request ID, records bounded request/response metadata and
// emits one access entry after the wrapped handler returns. Client-supplied
// request IDs are deliberately overwritten at the gateway boundary.
func Observe(logger *zap.Logger) Middleware {
	return ObserveWithMetrics(logger, nil)
}

// ObserveWithMetrics is the process-level observer with optional metrics
// recording. Metrics are recorded after the same bounded outcome used for the
// access log, so both surfaces share classification and labels.
func ObserveWithMetrics(logger *zap.Logger, metrics *telemetry.Metrics) Middleware {
	if logger == nil {
		logger = zap.NewNop()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			requestID := newRequestID()
			observation := telemetry.New(requestID, started)
			ctx := telemetry.WithObservation(r.Context(), observation)
			r = r.WithContext(ctx)
			if r.Header == nil {
				r.Header = make(http.Header)
			}
			r.Header.Set("X-Request-ID", requestID)
			w.Header().Set("X-Request-ID", requestID)
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = &countingBody{ReadCloser: r.Body, observation: observation}
			}

			defer func() {
				recovered := recover()
				if recovered != nil {
					if recovered == http.ErrAbortHandler {
						observation.MarkError("response_copy")
					} else {
						observation.MarkError("handler_panic")
					}
				}
				logOutcome(logger, r, observation, metrics)
				if recovered != nil {
					panic(recovered)
				}
			}()

			next.ServeHTTP(wrapResponseWriter(w, observation, protocol.IsWebSocketRequest(r)), r)
		})
	}
}

func logOutcome(logger *zap.Logger, r *http.Request, observation *telemetry.Observation, metrics *telemetry.Metrics) {
	outcome := observation.Outcome()
	if outcome.ErrorClass == "" {
		outcome.ErrorClass = defaultErrorClass(outcome)
	}
	if metrics != nil {
		metrics.RecordRequest(outcome, outcome.ErrorClass, time.Since(outcome.Started))
	}
	fields := []zap.Field{
		zap.String("request_id", outcome.RequestID),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("route", outcome.Route),
		zap.String("service", outcome.Service),
		zap.Int("status", outcome.Status),
		zap.Duration("duration", time.Since(outcome.Started)),
		zap.Int64("request_bytes", outcome.RequestBytes),
		zap.Int64("response_bytes", outcome.ResponseBytes),
		zap.String("error_class", outcome.ErrorClass),
	}
	if outcome.Status >= 500 {
		logger.Warn("request completed", fields...)
	} else {
		logger.Info("request completed", fields...)
	}
}

func RouteMetadata(route, service string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if observation := telemetry.FromContext(r.Context()); observation != nil {
				observation.SetRoute(route, service)
			}
			next.ServeHTTP(w, r)
		})
	}
}

type countingBody struct {
	io.ReadCloser
	observation *telemetry.Observation
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.observation.AddRequestBytes(n)
	return n, err
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	sequence := atomic.AddUint64(&fallbackRequestID, 1)
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), sequence)
}

type observingResponseWriter struct {
	http.ResponseWriter
	observation *telemetry.Observation
	requestID   string
	webSocket   bool
	mu          atomic.Bool
}

func (w *observingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *observingResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if !w.mu.CompareAndSwap(false, true) {
		return
	}
	if w.requestID != "" {
		// The gateway-generated ID must be the same value in the access log and
		// in the final client response. Restore it immediately before commit so
		// a backend or nested middleware cannot replace it.
		w.ResponseWriter.Header().Set("X-Request-ID", w.requestID)
	}
	w.observation.RecordResponse(status, 0)
	w.ResponseWriter.WriteHeader(status)
}

func (w *observingResponseWriter) Write(p []byte) (int, error) {
	if !w.mu.Load() {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.observation.AddResponseBytes(n)
	return n, err
}

func (w *observingResponseWriter) flush() {
	if !w.mu.Load() {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *observingResponseWriter) hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	conn, brw, err := hijacker.Hijack()
	if err != nil {
		return conn, brw, err
	}
	if w.webSocket && !w.mu.Load() {
		w.mu.Store(true)
		w.observation.RecordResponse(http.StatusSwitchingProtocols, 0)
	}
	return conn, brw, nil
}

func (w *observingResponseWriter) push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func wrapResponseWriter(w http.ResponseWriter, observation *telemetry.Observation, webSocket bool) http.ResponseWriter {
	requestID := ""
	if observation != nil {
		requestID = observation.Outcome().RequestID
	}
	base := &observingResponseWriter{ResponseWriter: w, observation: observation, requestID: requestID, webSocket: webSocket}
	_, flush := w.(http.Flusher)
	_, hijack := w.(http.Hijacker)
	_, push := w.(http.Pusher)
	switch {
	case flush && hijack && push:
		return &flushHijackPushWriter{observingResponseWriter: base}
	case flush && hijack:
		return &flushHijackWriter{observingResponseWriter: base}
	case flush && push:
		return &flushPushWriter{observingResponseWriter: base}
	case hijack && push:
		return &hijackPushWriter{observingResponseWriter: base}
	case flush:
		return &flushWriter{observingResponseWriter: base}
	case hijack:
		return &hijackWriter{observingResponseWriter: base}
	case push:
		return &pushWriter{observingResponseWriter: base}
	default:
		return base
	}
}

type flushWriter struct{ *observingResponseWriter }

func (w *flushWriter) Flush() { w.flush() }

type hijackWriter struct{ *observingResponseWriter }

func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

type pushWriter struct{ *observingResponseWriter }

func (w *pushWriter) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type flushHijackWriter struct{ *observingResponseWriter }

func (w *flushHijackWriter) Flush()                                       { w.flush() }
func (w *flushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

type flushPushWriter struct{ *observingResponseWriter }

func (w *flushPushWriter) Flush() { w.flush() }
func (w *flushPushWriter) Push(target string, opts *http.PushOptions) error {
	return w.push(target, opts)
}

type hijackPushWriter struct{ *observingResponseWriter }

func (w *hijackPushWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }
func (w *hijackPushWriter) Push(target string, opts *http.PushOptions) error {
	return w.push(target, opts)
}

type flushHijackPushWriter struct{ *observingResponseWriter }

func (w *flushHijackPushWriter) Flush()                                       { w.flush() }
func (w *flushHijackPushWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }
func (w *flushHijackPushWriter) Push(target string, opts *http.PushOptions) error {
	return w.push(target, opts)
}

func defaultErrorClass(outcome telemetry.Outcome) string {
	if outcome.Status == http.StatusNotFound && outcome.Route == "" {
		return "route_not_found"
	}
	switch outcome.Status {
	case http.StatusRequestEntityTooLarge:
		return "request_body_too_large"
	case http.StatusNotImplemented:
		return "unsupported_protocol"
	case http.StatusBadGateway:
		return "upstream"
	case http.StatusGatewayTimeout:
		return "timeout"
	case http.StatusServiceUnavailable:
		return "admission_rejected"
	default:
		if outcome.Status >= 400 {
			return "http_error"
		}
		return ""
	}
}
