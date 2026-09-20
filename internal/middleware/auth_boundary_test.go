package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForwardAuthFailureBudgetsAndBodies(t *testing.T) {
	for _, mode := range []string{"slow headers", "slow denial", "oversize denial", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "oversize denial" {
					w.Header().Set("Content-Encoding", "bogus")
					w.WriteHeader(403)
					io.WriteString(w, strings.Repeat("x", 100))
					return
				}
				if mode == "slow denial" {
					w.WriteHeader(403)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer auth.Close()
			mw, err := ForwardAuth(ForwardAuthOptions{Address: auth.URL, Timeout: 40 * time.Millisecond, MaxResponseBodyBytes: 16, Client: auth.Client()})
			if err != nil {
				t.Fatal(err)
			}
			called := false
			r := httptest.NewRequest("GET", "/", nil)
			if mode == "cancel" {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			started := time.Now()
			mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
			if called || w.Code != 502 || time.Since(started) > time.Second || w.Header().Get("Content-Encoding") != "" {
				t.Fatalf("called=%v status=%d headers=%v duration=%s", called, w.Code, w.Header(), time.Since(started))
			}
		})
	}
}

func TestForwardAuthBodyAndHopHeaders(t *testing.T) {
	for _, denied := range []bool{false, true} {
		options := ForwardAuthOptions{Address: "http://auth.test", ForwardBody: true, PreserveRequestMethod: true, MaxBodyBytes: 8, HeaderField: "x-user", AuthResponseHeadersRegex: "^X-", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			if string(body) != "payload" || r.Method != "POST" || r.Header.Get("X-Hop") != "" || r.Header.Get("X-Forwarded-Method") != "POST" {
				t.Errorf("auth request %s %s %v", r.Method, body, r.Header)
			}
			status := 200
			if denied {
				status = 403
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Connection": {"X-Hop"}, "X-Hop": {"unsafe"}, "X-User": {"alice"}}, Body: io.NopCloser(strings.NewReader("denied"))}, nil
		})}}
		mw, err := ForwardAuth(options)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/", strings.NewReader("payload"))
		r.Header.Set("Connection", "X-Hop")
		r.Header.Set("X-Hop", "client")
		r.Header.Set("X-User", "forged")
		called := false
		w := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			body, _ := io.ReadAll(r.Body)
			if string(body) != "payload" || r.Header.Get("X-User") != "alice" || r.Header.Get("X-Hop") != "" {
				t.Errorf("forwarded body/identity: %s %v", body, r.Header)
			}
		})).ServeHTTP(w, r)
		if called == denied || w.Header().Get("X-Hop") != "" {
			t.Fatalf("denied=%v called=%v headers=%v", denied, called, w.Header())
		}
	}
}

func TestForwardAuthRejectsOversizedRequestBeforeAuth(t *testing.T) {
	called := false
	mw, _ := ForwardAuth(ForwardAuthOptions{Address: "http://auth.test", ForwardBody: true, MaxBodyBytes: 2, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, context.Canceled })}})
	w := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader("large")))
	if called || w.Code != 401 {
		t.Fatalf("called=%v status=%d", called, w.Code)
	}
}

func TestJWKSInvalidResponsesBackoffAndCancel(t *testing.T) {
	for _, body := range []string{`{"keys":[]}`, `not json`, strings.Repeat("x", maxJWKSBytes+1), `{"keys":[],"keys":[]}`} {
		v := refreshTestVerifier(t)
		calls := 0
		v.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		for range 3 {
			if v.refresh(context.Background(), false) == nil {
				t.Fatal("invalid keys accepted")
			}
		}
		if calls != 1 {
			t.Fatalf("calls=%d", calls)
		}
	}
	v := refreshTestVerifier(t)
	auth := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer auth.Close()
	v.opts.JWKSURL = auth.URL
	v.client = auth.Client()
	v.client.Timeout = 60 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if v.refresh(ctx, false) == nil || time.Since(started) > time.Second {
		t.Fatal("caller cancellation did not bound refresh wait")
	}
	if v.refresh(context.Background(), false) == nil {
		t.Fatal("slow provider accepted")
	}
}
