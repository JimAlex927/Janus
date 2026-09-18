package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func testCORSOptions() CORSOptions {
	return CORSOptions{
		AllowOrigins:     []string{"https://app.example.com"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "X-Request-ID"},
		ExposeHeaders:    []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAgeSeconds:    600,
	}
}

func TestCORSCompletesAllowedPreflightWithoutCallingNext(t *testing.T) {
	called := false
	handler := CORS(testCORSOptions())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodOptions, "/api", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "content-type, x-request-id")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if called || response.Code != http.StatusNoContent {
		t.Fatalf("preflight called=%v status=%d, want false/204", called, response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
		t.Fatalf("allow methods = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, X-Request-ID" {
		t.Fatalf("allow headers = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := response.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("max age = %q", got)
	}
	if vary := response.Header().Values("Vary"); len(vary) != 3 {
		t.Fatalf("vary = %q, want origin/method/headers", vary)
	}
}

func TestCORSRejectsDisallowedPreflightAndDecoratesAllowedResponse(t *testing.T) {
	called := 0
	handler := CORS(testCORSOptions())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.Header().Set("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	rejectedRequest := httptest.NewRequest(http.MethodOptions, "/api", nil)
	rejectedRequest.Header.Set("Origin", "https://app.example.com")
	rejectedRequest.Header.Set("Access-Control-Request-Method", "DELETE")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, rejectedRequest)
	if rejected.Code != http.StatusForbidden || called != 0 {
		t.Fatalf("rejected preflight status=%d called=%d, want 403/0", rejected.Code, called)
	}

	allowedRequest := httptest.NewRequest(http.MethodGet, "/api", nil)
	allowedRequest.Header.Set("Origin", "https://app.example.com")
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusCreated || called != 1 || allowed.Body.String() != "ok" {
		t.Fatalf("response status=%d called=%d body=%q", allowed.Code, called, allowed.Body.String())
	}
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("response allow origin = %q", got)
	}
	if got := allowed.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-ID" {
		t.Fatalf("expose headers = %q", got)
	}
	if got := allowed.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("first vary value = %q", got)
	}
	if values := allowed.Header().Values("Vary"); len(values) != 2 || values[1] != "Origin" {
		t.Fatalf("vary values = %q", values)
	}
}

func TestCORSDecoratesImplicitEmptyResponse(t *testing.T) {
	handler := CORS(testCORSOptions())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := httptest.NewRequest(http.MethodGet, "/api", nil)
	request.Header.Set("Origin", "https://app.example.com")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("implicit response allow origin = %q", got)
	}
}

func TestCORSSkipsWebSocketHandshake(t *testing.T) {
	handler := CORS(testCORSOptions())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	request := httptest.NewRequest(http.MethodGet, "/socket", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSwitchingProtocols || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("websocket response status=%d headers=%#v", response.Code, response.Header())
	}
}
