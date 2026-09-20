package middleware

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestCompressInformationalHeadersAndFrozenFinal(t *testing.T) {
	h := Compress(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(103)
		w.Header().Set("X-Frozen", "before")
		w.Header().Set("Vary", "Origin")
		w.WriteHeader(201)
		w.Header().Set("X-Frozen", "after")
		io.WriteString(w, "plain body")
	}))
	s := httptest.NewServer(h)
	defer s.Close()
	for _, encoding := range []string{"identity", "gzip"} {
		informational := 0
		r, _ := http.NewRequest("GET", s.URL, nil)
		r.Header.Set("Accept-Encoding", encoding)
		r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error { informational = code; return nil }}))
		res, err := s.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if informational != 103 || res.StatusCode != 201 || res.Header.Get("X-Frozen") != "before" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") || !strings.Contains(strings.Join(res.Header.Values("Vary"), ","), "Accept-Encoding") {
			t.Fatalf("%s: informational=%d status=%d headers=%v", encoding, informational, res.StatusCode, res.Header)
		}
	}
}

type failingFlushWriter struct {
	*httptest.ResponseRecorder
	failure error
}

func (w failingFlushWriter) FlushError() error { return w.failure }

func TestCompressNoTransformAndFlushError(t *testing.T) {
	failure := errors.New("flush failure")
	w := failingFlushWriter{httptest.NewRecorder(), failure}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	Compress(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, no-transform")
		io.WriteString(w, "original")
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, failure) {
			t.Errorf("flush: %v", err)
		}
	})).ServeHTTP(w, r)
	if w.Header().Get("Content-Encoding") != "" || w.Body.String() != "original" {
		t.Fatal("no-transform ignored")
	}
}

func TestResponseBufferPreservesTrailers(t *testing.T) {
	for _, outside := range []bool{true, false} {
		t.Run(fmt.Sprint(outside), func(t *testing.T) {
			var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Trailer", "X-Checksum")
				w.WriteHeader(200)
				if r.URL.Path != "/empty" {
					io.WriteString(w, "body")
				}
				w.Header().Set("X-Checksum", "complete")
			})
			if outside {
				h = Compress(CompressOptions{})(Buffer(1024)(h))
			} else {
				h = Buffer(1024)(Compress(CompressOptions{})(h))
			}
			s := httptest.NewServer(h)
			defer s.Close()
			for _, path := range []string{"/body", "/empty"} {
				res, err := s.Client().Get(s.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if err != nil || res.Trailer.Get("X-Checksum") != "complete" {
					t.Fatalf("trailers=%v error=%v", res.Trailer, err)
				}
			}
		})
	}
}

func TestCompressFirstFinalStatusAndRange(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		rangeRequest bool
	}{
		{"first final", 201, false}, {"partial", 206, false}, {"range request", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Compress(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 206 {
					w.Header().Set("Content-Range", "bytes 0-2/10")
				}
				w.WriteHeader(tc.status)
				w.WriteHeader(500)
				_, _ = io.WriteString(w, "abc")
			}))
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Accept-Encoding", "gzip")
			if tc.rangeRequest {
				r.Header.Set("Range", "bytes=0-2")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d want %d", w.Code, tc.status)
			}
			if (tc.status == 206 || tc.rangeRequest) && w.Header().Get("Content-Encoding") != "" {
				t.Fatal("range representation compressed")
			}
		})
	}
}

func TestResponsePolicyCombinationsOverTCP(t *testing.T) {
	for _, order := range []string{"compress-buffer", "buffer-compress", "headers-outside", "cors-outside"} {
		for _, status := range []int{200, 201, 204, 206, 304} {
			t.Run(fmt.Sprintf("%s/%d", order, status), func(t *testing.T) {
				base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Content-Length", "3")
					if status == 206 {
						w.Header().Set("Content-Range", "bytes 0-2/10")
					}
					w.WriteHeader(103)
					w.WriteHeader(status)
					w.WriteHeader(500)
					if status != 204 && status != 304 {
						io.WriteString(w, "abc")
						w.(http.Flusher).Flush()
					}
				})
				gzipMW := Compress(CompressOptions{})
				buffer := Buffer(1024)
				headers := Headers(nil, nil, map[string]string{"X-Test": "set"}, nil)
				cors := CORS(CORSOptions{AllowOrigins: []string{"https://client.test"}})
				var h http.Handler
				switch order {
				case "compress-buffer":
					h = gzipMW(buffer(cors(headers(base))))
				case "buffer-compress":
					h = buffer(gzipMW(cors(headers(base))))
				case "headers-outside":
					h = headers(gzipMW(buffer(cors(base))))
				default:
					h = cors(buffer(gzipMW(headers(base))))
				}
				s := httptest.NewServer(h)
				defer s.Close()
				client := s.Client()
				client.Timeout = time.Second
				for _, method := range []string{"GET", "HEAD"} {
					req, _ := http.NewRequest(method, s.URL, nil)
					req.Header.Set("Accept-Encoding", "gzip")
					req.Header.Set("Origin", "https://client.test")
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					var reader io.Reader = resp.Body
					if resp.Header.Get("Content-Encoding") == "gzip" && method != "HEAD" {
						zr, err := gzip.NewReader(resp.Body)
						if err != nil {
							t.Fatal(err)
						}
						defer zr.Close()
						reader = zr
					}
					body, err := io.ReadAll(reader)
					resp.Body.Close()
					if err != nil || resp.StatusCode != status || resp.Header.Get("X-Test") != "set" || resp.Header.Get("Access-Control-Allow-Origin") != "https://client.test" {
						t.Fatalf("%s status=%d body=%s headers=%v error=%v", method, resp.StatusCode, body, resp.Header, err)
					}
					want := "abc"
					if method == "HEAD" || status == 204 || status == 304 {
						want = ""
					}
					if string(body) != want {
						t.Fatalf("body=%q want %q", body, want)
					}
					if (status == 206 || status == 204 || status == 304 || method == "HEAD") && resp.Header.Get("Content-Encoding") != "" {
						t.Fatal("forbidden compression")
					}
				}
			})
		}
	}
}

func TestResponseBufferTimeoutAbortAndOverflow(t *testing.T) {
	for _, mode := range []string{"timeout", "abort", "overflow"} {
		h := Timeout(15 * time.Millisecond)(Buffer(3)(Compress(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Partial", "must-not-leak")
			io.WriteString(w, "partial")
			if mode == "timeout" {
				<-r.Context().Done()
			}
			if mode == "abort" {
				panic(http.ErrAbortHandler)
			}
		}))))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		want := 500
		if mode == "timeout" {
			want = 504
		}
		if mode == "abort" {
			want = 502
		}
		if w.Code != want || strings.Contains(w.Body.String(), "partial") || w.Header().Get("X-Partial") != "" {
			t.Fatalf("%s = %d %s %v", mode, w.Code, w.Body, w.Header())
		}
	}
}

func TestCompressIdentityResponseVaries(t *testing.T) {
	w := httptest.NewRecorder()
	Compress(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatal("identity variant missing Vary")
	}
}
