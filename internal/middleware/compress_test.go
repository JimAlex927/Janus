package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompressNegotiationAndSkip(t *testing.T) {
	mw := Compress(CompressOptions{})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rr.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("compression headers: %d %v", rr.Code, rr.Header())
	}
	zr, err := gzip.NewReader(strings.NewReader(rr.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != "hello" {
		t.Fatalf("body %q", body)
	}
	stream := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: hi\n\n"))
	}))
	rr = httptest.NewRecorder()
	stream.ServeHTTP(rr, req)
	if rr.Header().Get("Content-Encoding") != "" {
		t.Fatal("SSE was compressed")
	}
	noGzip := httptest.NewRequest("GET", "/", nil)
	noGzip.Header.Set("Accept-Encoding", "gzip;q=0")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, noGzip)
	if rr.Header().Get("Content-Encoding") != "" || rr.Body.String() != "hello" {
		t.Fatalf("q=0 was compressed: %v %q", rr.Header(), rr.Body.String())
	}
}
