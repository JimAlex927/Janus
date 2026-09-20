package admin

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"janus/internal/config"
)

func TestLoginBudgetsIgnoreForwardedIdentity(t *testing.T) {
	h := protectLogin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
}

func TestLoginConcurrentVerificationIsBounded(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var wg sync.WaitGroup
	h := protectLogin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { started <- struct{}{}; <-release; w.WriteHeader(401) }))
	defer func() { close(release); wg.Wait() }()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/", nil)
			r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1", i)
			h.ServeHTTP(httptest.NewRecorder(), r)
		}(i)
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("login did not enter")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 429 {
		t.Fatalf("unbounded login %d", w.Code)
	}
}

func TestSessionCookieSecureContract(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	for _, tc := range []struct {
		name                             string
		configured, tls, forwarded, want bool
	}{{"private HTTP", false, false, false, false}, {"spoofed header", false, false, true, false}, {"direct TLS", false, true, false, true}, {"trusted TLS termination configured", true, false, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandlerWithOptions(Options{Current: func() config.Config {
				return config.Config{Settings: config.Settings{Admin: config.AdminSettings{Username: "admin", PasswordHash: string(hash), CookieSecure: tc.configured}}}
			}})
			for _, path := range []string{"login", "logout"} {
				r := httptest.NewRequest("POST", "/api/v1/auth/"+path, strings.NewReader(`{"username":"admin","password":"secret"}`))
				if tc.tls {
					r.TLS = &tls.ConnectionState{}
				}
				if tc.forwarded {
					r.Header.Set("X-Forwarded-Proto", "https")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				cookies := w.Result().Cookies()
				if w.Code != 200 || len(cookies) != 1 || cookies[0].Secure != tc.want {
					t.Fatalf("%s = %d %v", path, w.Code, cookies)
				}
			}
		})
	}
}

func TestStoredDraftRevisionPreventsLostUpdate(t *testing.T) {
	f := newLibraryFixture(t)
	created := f.do(t, "POST", "/api/v1/configs", `{"name":"draft"}`)
	var record struct {
		ID      int64  `json:"id"`
		Updated string `json:"updated_at"`
	}
	json.Unmarshal(created.Body.Bytes(), &record)
	path := fmt.Sprintf("/api/v1/configs/%d", record.ID)
	get := f.do(t, "GET", path, "")
	json.Unmarshal(get.Body.Bytes(), &record)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("PUT", path, strings.NewReader(fmt.Sprintf(`{"name":"editor-%d"}`, i)))
		r.AddCookie(f.cookie)
		r.Header.Set("X-Janus-Record-Revision", record.Updated)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		want := 200
		if i == 1 {
			want = 409
		}
		if w.Code != want {
			t.Fatalf("save %d = %d %s", i, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("POST", path+"/publish", nil)
	r.AddCookie(f.cookie)
	r.Header.Set("X-Janus-Revision", "1")
	r.Header.Set("X-Janus-Record-Revision", record.Updated)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 409 || f.revision != 1 {
		t.Fatalf("stale publish = %d revision=%d", w.Code, f.revision)
	}
}

func TestConcurrentAdminPublishesConflictWithoutReorderingMarker(t *testing.T) {
	f := newLibraryFixture(t)
	var paths []string
	for _, name := range []string{"one", "two"} {
		response := f.do(t, "POST", "/api/v1/configs", fmt.Sprintf(`{"name":%q}`, name))
		var record struct {
			ID int64 `json:"id"`
		}
		json.Unmarshal(response.Body.Bytes(), &record)
		paths = append(paths, fmt.Sprintf("/api/v1/configs/%d/publish", record.ID))
	}
	results := make(chan int, 2)
	for _, path := range paths {
		go func(path string) { results <- f.doWithRevision(t, "POST", path, "", "1").Code }(path)
	}
	a, b := <-results, <-results
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatalf("publish responses %d %d", a, b)
	}
	if f.revision != 2 {
		t.Fatalf("revision %d", f.revision)
	}
	response := f.do(t, "GET", "/api/v1/configs", "")
	var listing struct {
		Configs []configRecordView `json:"configs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, record := range listing.Configs {
		if record.Status == "active" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active markers = %d", active)
	}
}

func TestPublicationReturnsItsRecordRevision(t *testing.T) {
	f := newLibraryFixture(t)
	created := f.do(t, "POST", "/api/v1/configs", `{"name":"publication-token"}`)
	var record struct {
		ID      int64  `json:"id"`
		Updated string `json:"updated_at"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/configs/%d", record.ID)
	published := f.do(t, "POST", path+"/publish", "")
	if err := json.Unmarshal(published.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if published.Code != 200 || record.Updated == "" {
		t.Fatalf("publication token missing: %s", published.Body)
	}
	var current struct {
		Updated string `json:"updated_at"`
	}
	if err := json.Unmarshal(f.do(t, "GET", path, "").Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.Updated != record.Updated {
		t.Fatalf("token differs: %q / %q", current.Updated, record.Updated)
	}
}
