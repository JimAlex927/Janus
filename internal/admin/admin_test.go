package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpointsFollowState(t *testing.T) {
	state := NewState()
	h := NewHandler(state)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/livez", http.StatusOK},
		{"/readyz", http.StatusServiceUnavailable},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
		})
	}
	state.SetReady(true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Fatalf("ready response = %d %q", w.Code, w.Body.String())
	}
	state.SetLive(false)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-live status = %d, want 503", w.Code)
	}
}

func TestHealthEndpointsMethodAndHeadSemantics(t *testing.T) {
	h := NewHandler(NewState())
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/livez", nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST response = %d allow=%q", post.Code, post.Header().Get("Allow"))
	}
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/livez", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d body=%q", head.Code, head.Body.String())
	}
}
