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

func TestCompressExplicitGzipExclusionOverridesWildcard(t *testing.T) {
	for _, value := range []string{"gzip;q=0, *;q=1", "*;q=1, gzip;q=0"} {
		t.Run(value, func(t *testing.T) {
			h := Compress(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "hello")
			}))
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Accept-Encoding", value)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Header().Get("Content-Encoding") != "" || w.Body.String() != "hello" {
				t.Fatalf("explicitly forbidden gzip used: headers=%v body=%q", w.Header(), w.Body.String())
			}
		})
	}
}

func TestGzipNegotiationAcrossHeaderValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   bool
	}{
		{"absent", nil, false},
		{"wildcard", []string{"*;q=0.5"}, true},
		{"explicit allows", []string{"*;q=0", "gzip;q=0.5"}, true},
		{"explicit forbids", []string{"*;q=1", "gzip;q=0"}, false},
		{"explicit forbids reversed", []string{"gzip;q=0", "*;q=1"}, false},
		{"other coding", []string{"br"}, false},
		{"invalid quality", []string{"gzip;q=invalid", "*"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, value := range tc.values {
				r.Header.Add("Accept-Encoding", value)
			}
			if got := acceptsGzip(r); got != tc.want {
				t.Fatalf("acceptsGzip(%v) = %v, want %v", tc.values, got, tc.want)
			}
		})
	}
}
