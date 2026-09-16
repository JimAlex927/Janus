package middleware

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"janus/internal/telemetry"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestObserveAddsRequestIDAndRecordsOutcome(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	h := Observe(logger)(RouteMetadata("orders", "api")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") == "client-supplied" || r.Header.Get("X-Request-ID") == "" {
			t.Errorf("request ID was not regenerated: %q", r.Header.Get("X-Request-ID"))
		}
		if body, err := io.ReadAll(r.Body); err != nil || string(body) != "abc" {
			t.Errorf("request body = %q, err=%v", body, err)
		}
		w.WriteHeader(http.StatusEarlyHints)
		_, _ = io.WriteString(w, "ok")
	})))
	r := httptest.NewRequest(http.MethodPost, "http://gateway/orders?token=secret", strings.NewReader("abc"))
	r.Header.Set("X-Request-ID", "client-supplied")
	w := &informationalResponseWriter{header: make(http.Header)}
	h.ServeHTTP(w, r)
	if w.status != http.StatusOK || w.body.String() != "ok" || len(w.informational) != 1 || w.informational[0] != http.StatusEarlyHints {
		t.Fatalf("response = %d %q informational=%v", w.status, w.body.String(), w.informational)
	}
	requestID := w.Header().Get("X-Request-ID")
	if requestID == "" || requestID == "client-supplied" {
		t.Fatalf("response request ID = %q", requestID)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	for key, want := range map[string]any{
		"request_id":     requestID,
		"method":         "POST",
		"path":           "/orders",
		"route":          "orders",
		"service":        "api",
		"status":         int64(200),
		"request_bytes":  int64(3),
		"response_bytes": int64(2),
	} {
		if got := fields[key]; got != want {
			t.Errorf("field %s = %#v, want %#v", key, got, want)
		}
	}
	if _, ok := fields["query"]; ok {
		t.Fatal("access log contains a query field")
	}
}

func TestObserveClassifiesEarlyRouteNotFound(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	h := Observe(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://gateway/missing", nil))
	if got := logs.All()[0].ContextMap()["error_class"]; got != "route_not_found" {
		t.Fatalf("error class = %#v, want route_not_found", got)
	}
}

func TestObserveLogsResponseCopyAbort(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	h := Observe(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "prefix")
		panic(http.ErrAbortHandler)
	}))
	w := httptest.NewRecorder()
	panicked := false
	func() {
		defer func() {
			panicked = recover() == http.ErrAbortHandler
		}()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://gateway/stream", nil))
	}()
	if !panicked {
		t.Fatal("observer swallowed response-copy abort")
	}
	if got := logs.All()[0].ContextMap()["error_class"]; got != "response_copy" {
		t.Fatalf("error class = %#v, want response_copy", got)
	}
}

func TestObserveCountsPartialWrites(t *testing.T) {
	observation := newTestObservation()
	writer := wrapResponseWriter(&shortWriteResponseWriter{}, observation, false)
	if n, err := writer.Write([]byte("hello")); n != 2 || err == nil {
		t.Fatalf("write = %d, %v, want partial write with error", n, err)
	}
	outcome := observation.Outcome()
	if outcome.Status != http.StatusOK || outcome.ResponseBytes != 2 {
		t.Fatalf("outcome = %+v, want status 200 and 2 response bytes", outcome)
	}
}

func TestObservePreservesTrailersOverRealConnection(t *testing.T) {
	server := httptest.NewServer(Observe(zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "X-Result")
		_, _ = io.WriteString(w, "body")
		w.Header().Set("X-Result", "complete")
	})))
	defer server.Close()
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "body" || resp.Trailer.Get("X-Result") != "complete" {
		t.Fatalf("body=%q trailers=%v", body, resp.Trailer)
	}
}

func TestObserverPreservesResponseCapabilities(t *testing.T) {
	plain := &plainResponseWriter{header: make(http.Header)}
	wrapped := wrapResponseWriter(plain, nil, false)
	if _, ok := wrapped.(http.Flusher); ok {
		t.Fatal("observer advertised Flusher unsupported by the underlying writer")
	}
	if _, ok := wrapped.(http.Hijacker); ok {
		t.Fatal("observer advertised Hijacker unsupported by the underlying writer")
	}
	if unwrapped, ok := wrapped.(interface{ Unwrap() http.ResponseWriter }); !ok || unwrapped.Unwrap() != plain {
		t.Fatal("observer did not expose its underlying writer through Unwrap")
	}

	recorder := httptest.NewRecorder()
	flushing := wrapResponseWriter(recorder, nil, false)
	if _, ok := flushing.(http.Flusher); !ok {
		t.Fatal("observer dropped supported Flusher")
	}
	if err := http.NewResponseController(flushing).Flush(); err != nil {
		t.Fatalf("controller flush: %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("flush status = %d, want 200", recorder.Code)
	}
}

func TestObserverRecordsWebSocketSwitchOnHijack(t *testing.T) {
	underlying := &hijackResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	observation := newTestObservation()
	wrapped := wrapResponseWriter(underlying, observation, true)
	hijacker, ok := wrapped.(http.Hijacker)
	if !ok {
		t.Fatal("observer dropped supported Hijacker")
	}
	serverConn, clientConn := net.Pipe()
	underlying.conn = serverConn
	underlying.brw = bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn))
	conn, _, err := hijacker.Hijack()
	if err != nil || conn != serverConn {
		t.Fatalf("hijack = %v, %v", conn, err)
	}
	serverConn.Close()
	clientConn.Close()
	if got := observation.Outcome().Status; got != http.StatusSwitchingProtocols {
		t.Fatalf("observed status = %d, want 101", got)
	}
}

func newTestObservation() *telemetry.Observation {
	return telemetry.New("test", time.Now())
}

type plainResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

type shortWriteResponseWriter struct {
	header http.Header
}

func (w *shortWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *shortWriteResponseWriter) WriteHeader(int) {}
func (w *shortWriteResponseWriter) Write(p []byte) (int, error) {
	return 2, io.ErrShortWrite
}

type informationalResponseWriter struct {
	header        http.Header
	informational []int
	status        int
	body          bytes.Buffer
}

func (w *informationalResponseWriter) Header() http.Header { return w.header }
func (w *informationalResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.informational = append(w.informational, status)
		return
	}
	if w.status == 0 {
		w.status = status
	}
}
func (w *informationalResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *plainResponseWriter) Header() http.Header    { return w.header }
func (w *plainResponseWriter) WriteHeader(status int) { w.status = status }
func (w *plainResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

type hijackResponseWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
	brw  *bufio.ReadWriter
}

func (w *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, w.brw, nil
}
