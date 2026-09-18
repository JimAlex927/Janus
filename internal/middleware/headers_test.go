package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeadersMutatesRequestAndResponseWithoutChangingOriginalRequest(t *testing.T) {
	h := Headers(
		map[string]string{"X-Request-Set": "new"}, []string{"X-Request-Remove"},
		map[string]string{"X-Response-Set": "new"}, []string{"X-Response-Remove"},
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Request-Set"); got != "new" {
			t.Errorf("request set header = %q", got)
		}
		if got := r.Header.Get("X-Request-Remove"); got != "" {
			t.Errorf("request removed header = %q", got)
		}
		w.Header().Set("X-Response-Set", "old")
		w.Header().Set("X-Response-Remove", "old")
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Set", "old")
	req.Header.Set("X-Request-Remove", "old")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated || w.Header().Get("X-Response-Set") != "new" || w.Header().Get("X-Response-Remove") != "" {
		t.Fatalf("response = %d, headers = %#v", w.Code, w.Header())
	}
	if req.Header.Get("X-Request-Set") != "old" || req.Header.Get("X-Request-Remove") != "old" {
		t.Fatalf("original request was mutated: %#v", req.Header)
	}
}

func TestHeadersAppliesResponseRulesOnImplicitWriteHeader(t *testing.T) {
	h := Headers(nil, nil, map[string]string{"X-Test": "yes"}, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Header().Get("X-Test") != "yes" || w.Body.String() != "ok" {
		t.Fatalf("response headers = %#v, body = %q", w.Header(), w.Body.String())
	}
}

func TestHeadersAppliesResponseRulesWhenHandlerWritesNothing(t *testing.T) {
	h := Headers(nil, nil, map[string]string{"X-Test": "yes"}, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Header().Get("X-Test") != "yes" {
		t.Fatalf("response headers = %#v", w.Header())
	}
}

func TestHeadersAppliesResponseRulesBeforeFlush(t *testing.T) {
	h := Headers(nil, nil, map[string]string{"X-Test": "yes"}, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NewResponseController(w).Flush()
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Header().Get("X-Test") != "yes" || !w.Flushed {
		t.Fatalf("response headers = %#v, flushed = %v", w.Header(), w.Flushed)
	}
}

func TestHeadersDoesNotModifySwitchingProtocolsHandshake(t *testing.T) {
	h := Headers(nil, nil, map[string]string{"X-Janus-Response": "managed"}, []string{"Server"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "upgrade-owner")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/socket", nil))
	if w.Code != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", w.Code)
	}
	if got := w.Header().Get("X-Janus-Response"); got != "" {
		t.Fatalf("upgrade response unexpectedly gained policy header %q", got)
	}
	if got := w.Header().Get("Server"); got != "upgrade-owner" {
		t.Fatalf("upgrade response unexpectedly changed Server header to %q", got)
	}
}
