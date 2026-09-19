package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBasicAuth(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	mw, err := BasicAuth(BasicAuthOptions{Realm: "Janus", Users: map[string]string{"alice": string(hash)}, RemoveHeader: true})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization was not removed")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("GET", "/", nil))
	if unauth.Code != http.StatusUnauthorized || unauth.Header().Get("WWW-Authenticate") != `Basic realm="Janus"` || called {
		t.Fatalf("unexpected unauthorized response: %d %v", unauth.Code, unauth.Header())
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("alice", "secret")
	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, req)
	if !called || ok.Code != http.StatusNoContent {
		t.Fatalf("authorized request failed: %d", ok.Code)
	}
}
