package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"janus/internal/config"
	janusruntime "janus/internal/runtime"
	"janus/internal/store"
)

func testVersionedConfig() config.Config {
	return config.Config{
		Version: config.CurrentConfigVersion,
		Limens: map[string]config.LimenConfig{
			"default": {Address: "127.0.0.1:8080", Protocols: []string{"http1"}},
		},
		Services: map[string]config.Service{
			"example": {Upstreams: []string{"http://127.0.0.1:9000"}},
		},
		Routes: []config.Route{{
			Name:   "example-api",
			Limen:  "default",
			Match:  "PathPrefix(`/api`)",
			Action: &config.RouteAction{Forward: &config.ForwardAction{Service: "example"}},
		}},
	}
}

func openTestLibrary(t *testing.T) *store.Store {
	t.Helper()
	library, err := store.Open(filepath.Join(t.TempDir(), "janus-configs.db"))
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	t.Cleanup(func() { _ = library.Close() })
	return library
}

type libraryFixture struct {
	handler    http.Handler
	cookie     *http.Cookie
	published  *config.Config
	onDisk     *config.Config
	revision   uint64
	publishErr error
}

func newLibraryFixture(t *testing.T) *libraryFixture {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	current := testVersionedConfig()
	current.Settings.Admin = config.AdminSettings{Username: "admin", PasswordHash: string(hash)}
	persisted := current
	fixture := &libraryFixture{published: &config.Config{}, onDisk: &persisted, revision: 1}
	library := openTestLibrary(t)
	fixture.handler = NewHandlerWithOptions(Options{
		State:    NewState(),
		Current:  func() config.Config { return current },
		Revision: func() uint64 { return fixture.revision },
		Publish: func(candidate config.Config, expectedRevision uint64) error {
			if expectedRevision != fixture.revision {
				return janusruntime.ErrRevisionConflict
			}
			if fixture.publishErr != nil {
				return fixture.publishErr
			}
			if err := candidate.Validate(); err != nil {
				return err
			}
			*fixture.published = candidate
			current = candidate
			fixture.revision++
			return nil
		},
		PublishStored: func(running, persisted config.Config, expectedRevision uint64) error {
			if expectedRevision != fixture.revision {
				return janusruntime.ErrRevisionConflict
			}
			if fixture.publishErr != nil {
				return fixture.publishErr
			}
			if err := running.Validate(); err != nil {
				return err
			}
			if err := persisted.Validate(); err != nil {
				return err
			}
			*fixture.onDisk = persisted
			*fixture.published = running
			current = running
			fixture.revision++
			return nil
		},
		Library: library,
		SaveActive: func(updated config.Config) error {
			updated.Settings.Admin.PasswordHash = current.Settings.Admin.PasswordHash
			*fixture.onDisk = updated
			return nil
		},
	})
	login := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	fixture.handler.ServeHTTP(login, loginReq)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d", login.Code)
	}
	fixture.cookie = login.Result().Cookies()[0]
	return fixture
}

func (f *libraryFixture) do(t *testing.T, method, path string, body string) *httptest.ResponseRecorder {
	return f.doWithRevision(t, method, path, body, strconv.FormatUint(f.revision, 10))
}

func (f *libraryFixture) doWithRevision(t *testing.T, method, path string, body, revision string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost && strings.HasSuffix(path, "/publish") {
		req.Header.Set("X-Janus-Revision", revision)
	}
	req.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func TestConfigLibraryCRUD(t *testing.T) {
	f := newLibraryFixture(t)

	created := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"first"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || createdBody.ID == 0 {
		t.Fatalf("create response = %s, err = %v", created.Body.String(), err)
	}

	listed := f.do(t, http.MethodGet, "/api/v1/configs", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"name":"first"`) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}

	fetched := f.do(t, http.MethodGet, "/api/v1/configs/1", "")
	if fetched.Code != http.StatusOK || !strings.Contains(fetched.Body.String(), `"example-api"`) {
		t.Fatalf("get = %d %s", fetched.Code, fetched.Body.String())
	}

	updated := f.do(t, http.MethodPut, "/api/v1/configs/1",
		"{\"content\":{\"version\":1,\"limens\":{\"default\":{\"address\":\"127.0.0.1:8080\",\"protocols\":[\"http1\"]}},\"settings\":{},\"services\":{\"example\":{\"upstreams\":[\"http://127.0.0.1:9000\"]}},\"routes\":[{\"name\":\"renamed\",\"limen\":\"default\",\"match\":\"PathPrefix(`/api`)\",\"action\":{\"forward\":{\"service\":\"example\"}}}]}}")
	if updated.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", updated.Code, updated.Body.String())
	}

	invalid := f.do(t, http.MethodPut, "/api/v1/configs/1", `{"content":{"version":1,"routes":[]}}`)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid save status = %d, want 422", invalid.Code)
	}

	invalidLayout := f.do(t, http.MethodPut, "/api/v1/configs/1", `{"name":"should-not-stick","layout":[]}`)
	if invalidLayout.Code != http.StatusBadRequest {
		t.Fatalf("invalid layout status = %d, want 400", invalidLayout.Code)
	}
	unchanged := f.do(t, http.MethodGet, "/api/v1/configs/1", "")
	if unchanged.Code != http.StatusOK || strings.Contains(unchanged.Body.String(), "should-not-stick") {
		t.Fatalf("invalid layout partially updated config: %d %s", unchanged.Code, unchanged.Body.String())
	}

	missing := f.do(t, http.MethodGet, "/api/v1/configs/999", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing get status = %d, want 404", missing.Code)
	}

	deleted := f.do(t, http.MethodDelete, "/api/v1/configs/1", "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
}

func TestConfigLibraryListsIndependentStatusPages(t *testing.T) {
	f := newLibraryFixture(t)
	for _, name := range []string{"first", "second", "third"} {
		created := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"`+name+`"}`)
		if created.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", name, created.Code, created.Body.String())
		}
	}
	page := f.do(t, http.MethodGet, "/api/v1/configs?status=draft&pageNum=1&pageSize=2", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"total":3`) || !strings.Contains(page.Body.String(), `"pageNum":1`) || strings.Count(page.Body.String(), `"status":"draft"`) != 3 {
		t.Fatalf("draft page = %d %s", page.Code, page.Body.String())
	}
	next := f.do(t, http.MethodGet, "/api/v1/configs?status=draft&pageNum=2&pageSize=2", "")
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), `"pageNum":2`) || strings.Count(next.Body.String(), `"status":"draft"`) != 2 {
		t.Fatalf("second draft page = %d %s", next.Code, next.Body.String())
	}
	invalid := f.do(t, http.MethodGet, "/api/v1/configs?status=all", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestConfigLibrarySupportsTimeFiltersAndOrdering(t *testing.T) {
	f := newLibraryFixture(t)
	for _, name := range []string{"first", "second"} {
		created := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"`+name+`"}`)
		if created.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", name, created.Code, created.Body.String())
		}
	}
	from := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	path := "/api/v1/configs?status=draft&pageNum=1&pageSize=1&createdAtFrom=" + url.QueryEscape(from) + "&createdAtTo=" + url.QueryEscape(to) + "&sortBy=createdAt&sortOrder=asc"
	page := f.do(t, http.MethodGet, path, "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"total":2`) || !strings.Contains(page.Body.String(), `"pageSize":1`) || !strings.Contains(page.Body.String(), `"sortBy":"createdAt"`) {
		t.Fatalf("filtered page = %d %s", page.Code, page.Body.String())
	}
	invalid := f.do(t, http.MethodGet, "/api/v1/configs?status=draft&pageNum=1&pageSize=1&sortBy=name", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid sort = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestConfigLibraryPublishWritesStartupDiff(t *testing.T) {
	f := newLibraryFixture(t)

	created := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"with-limen"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d", created.Code)
	}
	saved := f.do(t, http.MethodPut, "/api/v1/configs/1",
		"{\"content\":{\"version\":1,\"limens\":{\"default\":{\"address\":\"127.0.0.1:8080\",\"protocols\":[\"http1\"]},\"extra\":{\"address\":\"127.0.0.1:8081\",\"protocols\":[\"http1\"]}},\"settings\":{},\"services\":{\"example\":{\"upstreams\":[\"http://127.0.0.1:9000\"]}},\"routes\":[{\"name\":\"example-api\",\"limen\":\"default\",\"match\":\"PathPrefix(`/api`)\",\"action\":{\"forward\":{\"service\":\"example\"}}}]}}")
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", saved.Code, saved.Body.String())
	}
	published := f.do(t, http.MethodPost, "/api/v1/configs/1/publish", "")
	if published.Code != http.StatusOK {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}
	if !strings.Contains(published.Body.String(), `"restart_required":true`) || !strings.Contains(published.Body.String(), `"hot_applied":true`) {
		t.Fatalf("publish result: %s", published.Body.String())
	}
	if len(f.published.Routes) != 1 {
		t.Fatalf("published routes = %+v", f.published.Routes)
	}
	if _, ok := f.onDisk.Limens["extra"]; !ok {
		t.Fatalf("published file lost extra limen: %+v", f.onDisk.Limens)
	}
	if _, ok := f.published.Limens["extra"]; ok {
		t.Fatal("new listener became active before restart")
	}
}

func TestConfigLibraryPublishNewLimenRouteWaitsForRestart(t *testing.T) {
	f := newLibraryFixture(t)
	if res := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"new-listener"}`); res.Code != http.StatusCreated {
		t.Fatal(res.Body.String())
	}
	content := testVersionedConfig()
	content.Limens["extra"] = config.LimenConfig{Address: "127.0.0.1:8081", Protocols: []string{"http1"}}
	content.Routes[0].Limen = "extra"
	content.Routes[0].Name = "on-extra"
	body, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		t.Fatal(err)
	}
	if res := f.do(t, http.MethodPut, "/api/v1/configs/1", string(body)); res.Code != http.StatusOK {
		t.Fatal(res.Body.String())
	}
	published := f.do(t, http.MethodPost, "/api/v1/configs/1/publish", "")
	if published.Code != http.StatusOK || !strings.Contains(published.Body.String(), `"hot_applied":false`) || !strings.Contains(published.Body.String(), `"restart_required":true`) {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}
	if f.onDisk.Routes[0].Name != "on-extra" || f.onDisk.Routes[0].Limen != "extra" {
		t.Fatalf("file lost new route: %+v", f.onDisk.Routes)
	}
	if f.published.Routes[0].Name != "example-api" {
		t.Fatalf("current generation changed prematurely: %+v", f.published.Routes)
	}
}

func TestConfigLibraryPublishFileErrorKeepsRuntimeAndMarker(t *testing.T) {
	f := newLibraryFixture(t)
	if res := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"new-listener"}`); res.Code != http.StatusCreated {
		t.Fatal(res.Body.String())
	}
	f.publishErr = errors.New("disk full")
	published := f.do(t, http.MethodPost, "/api/v1/configs/1/publish", "")
	if published.Code != http.StatusUnprocessableEntity || f.revision != 1 || len(f.published.Routes) != 0 {
		t.Fatalf("failed publish changed runtime: %d %s", published.Code, published.Body.String())
	}
	config := f.do(t, http.MethodGet, "/api/v1/configs/1", "")
	if !strings.Contains(config.Body.String(), `"status":"draft"`) {
		t.Fatalf("failed publish changed library marker: %s", config.Body.String())
	}
}

func TestConfigLibraryStageLimens(t *testing.T) {
	f := newLibraryFixture(t)

	created := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"with-limen"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d", created.Code)
	}
	saved := f.do(t, http.MethodPut, "/api/v1/configs/1",
		"{\"content\":{\"version\":1,\"limens\":{\"default\":{\"address\":\"127.0.0.1:8080\",\"protocols\":[\"http1\"]},\"extra\":{\"address\":\"127.0.0.1:8081\",\"protocols\":[\"http1\"]}},\"settings\":{},\"services\":{\"example\":{\"upstreams\":[\"http://127.0.0.1:9000\"]}},\"routes\":[{\"name\":\"example-api\",\"limen\":\"default\",\"match\":\"PathPrefix(`/api`)\",\"action\":{\"forward\":{\"service\":\"example\"}}}]}}")
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", saved.Code, saved.Body.String())
	}
	staged := f.do(t, http.MethodPost, "/api/v1/configs/1/stage-limens", "")
	if staged.Code != http.StatusOK || !strings.Contains(staged.Body.String(), "restart_required") {
		t.Fatalf("stage = %d %s", staged.Code, staged.Body.String())
	}
	if _, ok := f.onDisk.Limens["extra"]; !ok {
		t.Fatalf("staged limen missing from active file: %+v", f.onDisk.Limens)
	}
}

func TestConfigLibraryPublishAndDeleteGuard(t *testing.T) {
	f := newLibraryFixture(t)

	created := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"candidate"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d", created.Code)
	}

	published := f.do(t, http.MethodPost, "/api/v1/configs/1/publish", "")
	if published.Code != http.StatusOK {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}
	if len(f.published.Routes) != 1 || f.published.Routes[0].Name != "example-api" {
		t.Fatalf("published routes = %+v", f.published.Routes)
	}

	refused := f.do(t, http.MethodDelete, "/api/v1/configs/1", "")
	if refused.Code != http.StatusConflict {
		t.Fatalf("delete active status = %d, want 409", refused.Code)
	}
	editActive := f.do(t, http.MethodPut, "/api/v1/configs/1", `{"name":"mutated","layout":{"nodes":[]}}`)
	if editActive.Code != http.StatusOK || !strings.Contains(editActive.Body.String(), `"forked_from":1`) {
		t.Fatalf("edit active status = %d %s, want editable fork", editActive.Code, editActive.Body.String())
	}
	var forked struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(editActive.Body.Bytes(), &forked); err != nil || forked.ID == 1 {
		t.Fatalf("edit active response = %s, fork id = %d", editActive.Body.String(), forked.ID)
	}
	forkedPublish := f.do(t, http.MethodPost, "/api/v1/configs/"+strconv.FormatInt(forked.ID, 10)+"/publish", "")
	if forkedPublish.Code != http.StatusOK {
		t.Fatalf("publish fork = %d %s", forkedPublish.Code, forkedPublish.Body.String())
	}
	history := f.do(t, http.MethodGet, "/api/v1/configs/1", "")
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), `"status":"archived"`) {
		t.Fatalf("active history status = %d %s", history.Code, history.Body.String())
	}
}

func TestConfigLibraryPublishRejectsStaleRuntimeRevision(t *testing.T) {
	f := newLibraryFixture(t)
	created := f.do(t, http.MethodPost, "/api/v1/configs", `{"name":"candidate"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d", created.Code)
	}

	stale := f.doWithRevision(t, http.MethodPost, "/api/v1/configs/1/publish", "", "0")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale publish = %d %s, want 409", stale.Code, stale.Body.String())
	}
	if f.revision != 1 || len(f.published.Routes) != 0 {
		t.Fatalf("stale publish changed runtime: revision=%d published=%+v", f.revision, f.published.Routes)
	}

	// The header check is only a fast path. A concurrent publisher can advance
	// Runtime after that check, and the atomic publisher must still map the
	// resulting sentinel to the same HTTP conflict for a library publish.
	f.publishErr = janusruntime.ErrRevisionConflict
	raced := f.do(t, http.MethodPost, "/api/v1/configs/1/publish", "")
	if raced.Code != http.StatusConflict {
		t.Fatalf("raced publish = %d %s, want 409", raced.Code, raced.Body.String())
	}
	f.publishErr = nil

	published := f.do(t, http.MethodPost, "/api/v1/configs/1/publish", "")
	if published.Code != http.StatusOK || f.revision != 2 {
		t.Fatalf("current publish = %d %s revision=%d", published.Code, published.Body.String(), f.revision)
	}
}

func TestConfigSettingsRewrite(t *testing.T) {
	f := newLibraryFixture(t)
	saved := f.do(t, http.MethodPost, "/api/v1/config/settings",
		`{"request":{"read_timeout":"30s","maximum_duration":"30s","max_in_flight":1024},"stream":{"max_duration":"1h","idle_timeout":"5m"},"server":{"read_header_timeout":"5s","write_timeout":"35s","idle_timeout":"60s","max_header_bytes":32768},"backend":{"connect_timeout":"3s","keep_alive":"30s","tls_handshake_timeout":"5s","response_header_timeout":"10s","max_response_header_bytes":65536,"max_idle_conns":256,"max_idle_conns_per_host":32,"max_conns_per_host":128,"idle_conn_timeout":"90s","disable_compression":true},"admin":{"username":"admin"},"shutdown":{"drain_timeout":"35s","load_balancer_removal_delay":"0s"}}`)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), "restart_required") {
		t.Fatalf("settings save = %d %s", saved.Code, saved.Body.String())
	}

	invalid := f.do(t, http.MethodPost, "/api/v1/config/settings", `{"request":{"read_timeout":"500us","maximum_duration":"30s"}}`)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid settings status = %d, want 422", invalid.Code)
	}
}
