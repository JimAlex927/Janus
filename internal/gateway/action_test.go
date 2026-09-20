package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"janus/internal/config"
)

func TestDirectRouteActions(t *testing.T) {
	g, err := New(config.Config{
		Listen: "127.0.0.1:8080",
		Routes: []config.Route{
			{
				Name:  "health",
				Match: "Path(`/healthz`) && Method(`GET`)",
				Action: &config.RouteAction{Respond: &config.RespondAction{
					Status:  http.StatusOK,
					Body:    "ok\n",
					Headers: map[string]string{"Content-Type": "text/plain"},
				}},
			},
			{
				Name:  "redirect",
				Match: "Path(`/old`)",
				Action: &config.RouteAction{Redirect: &config.RedirectAction{
					Status:   http.StatusPermanentRedirect,
					Location: "/new",
				}},
			},
		},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	response := httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" || response.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("respond action = %d %q %#v", response.Code, response.Body.String(), response.Header())
	}

	response = httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/old", nil))
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "/new" {
		t.Fatalf("redirect action = %d location %q", response.Code, response.Header().Get("Location"))
	}
}

func TestStaticRouteAction(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>Janus SPA</main>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log('janus')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	g, err := New(config.Config{
		Listen: "127.0.0.1:8080",
		Routes: []config.Route{{
			Name: "frontend", PathPrefix: "/", Action: &config.RouteAction{Static: &config.StaticAction{
				Root: root, SPAFallback: true, CacheControl: "public, max-age=60",
			}},
		}},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	for _, test := range []struct {
		method, target, wantBody string
		wantCode                 int
		wantCache                bool
	}{
		{http.MethodGet, "/app.js", "console.log('janus')", http.StatusOK, true},
		{http.MethodGet, "/client/route", "<main>Janus SPA</main>", http.StatusOK, true},
		{http.MethodGet, "/directory/", "404 page not found\n", http.StatusNotFound, false},
		{http.MethodPost, "/app.js", "method not allowed\n", http.StatusMethodNotAllowed, false},
		{http.MethodGet, "/%2e%2e/app.js", "404 page not found\n", http.StatusNotFound, false},
	} {
		response := httptest.NewRecorder()
		g.ServeHTTP(response, httptest.NewRequest(test.method, "http://gateway"+test.target, nil))
		if response.Code != test.wantCode || response.Body.String() != test.wantBody {
			t.Fatalf("%s %s = %d %q, want %d %q", test.method, test.target, response.Code, response.Body.String(), test.wantCode, test.wantBody)
		}
		wantCache := ""
		if test.wantCache {
			wantCache = "public, max-age=60"
		}
		if response.Header().Get("Cache-Control") != wantCache {
			t.Logf("%s headers = %#v", test.target, response.Header())
			t.Fatalf("%s cache-control = %q", test.target, response.Header().Get("Cache-Control"))
		}
	}

	response := httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "http://gateway/app.js", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD static = %d %q", response.Code, response.Body.String())
	}
}

func TestStaticDirectoryListing(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "files", "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "files", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "files", "nested", "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := New(config.Config{
		Listen: "127.0.0.1:8080",
		Routes: []config.Route{{
			Name: "files", PathPrefix: "/", Action: &config.RouteAction{Static: &config.StaticAction{
				Root: root, DirectoryListing: true,
			}},
		}},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	response := httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/files/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "readme.txt") {
		t.Fatalf("directory listing = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/files/nested/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "child.txt") {
		t.Fatalf("nested directory listing = %d %q", response.Code, response.Body.String())
	}
}

func TestStaticDirectoryListingPreservesStripPrefixOnRedirect(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := New(config.Config{
		Listen: "127.0.0.1:8080",
		Middlewares: map[string]config.Middleware{
			"strip-test": {
				Scope:       config.MiddlewareScopeRoute,
				StripPrefix: &config.StripPrefixSettings{Prefix: "/test"},
			},
		},
		Routes: []config.Route{{
			Name:        "files",
			PathPrefix:  "/test",
			Middlewares: []string{"strip-test"},
			Action: &config.RouteAction{Static: &config.StaticAction{
				Root:             root,
				DirectoryListing: true,
			}},
		}},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://gateway/test", nil)
	g.ServeHTTP(response, request)
	location, locationErr := request.URL.Parse(response.Header().Get("Location"))
	if response.Code != http.StatusMovedPermanently || locationErr != nil || location.String() != "http://gateway/test/" {
		t.Fatalf("mount root redirect = %d %q, want 301 /test/", response.Code, response.Header().Get("Location"))
	}

	response = httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/test/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "nested") {
		t.Fatalf("mount root directory listing = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "http://gateway/test/nested", nil)
	g.ServeHTTP(response, request)
	location, locationErr = request.URL.Parse(response.Header().Get("Location"))
	if response.Code != http.StatusMovedPermanently || locationErr != nil || location.String() != "http://gateway/test/nested/" {
		t.Fatalf("directory redirect = %d %q, want 301 /test/nested/", response.Code, response.Header().Get("Location"))
	}

	response = httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/test/nested/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "child.txt") {
		t.Fatalf("redirected directory listing = %d %q", response.Code, response.Body.String())
	}
}

func TestStaticAssetPathAllowsFilesystemRoot(t *testing.T) {
	asset, ok := staticAssetPath(string(filepath.Separator), httptest.NewRequest(http.MethodGet, "http://gateway/etc/hosts", nil).URL)
	if !ok || asset != filepath.Join(string(filepath.Separator), "etc", "hosts") {
		t.Fatalf("filesystem-root asset = %q, %v", asset, ok)
	}
}

func TestStaticRouteActionRejectsEscapingSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "secret.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	g, err := New(config.Config{
		Listen: "127.0.0.1:8080",
		Routes: []config.Route{{
			Name: "frontend", PathPrefix: "/", Action: &config.RouteAction{Static: &config.StaticAction{
				Root: root, DirectoryListing: true,
			}},
		}},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	for _, target := range []string{"/secret.txt", "/outside/secret.txt"} {
		response := httptest.NewRecorder()
		g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway"+target, nil))
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "outside root") {
			t.Fatalf("escaped symlink %s = %d %q", target, response.Code, response.Body.String())
		}
	}
}

func TestStaticRouteActionAllowsSymlinkedRoot(t *testing.T) {
	realRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(realRoot, "index.html"), []byte("inside root"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "site")
	if err := os.Symlink(realRoot, root); err != nil {
		t.Fatal(err)
	}
	g, err := New(config.Config{
		Listen: "127.0.0.1:8080",
		Routes: []config.Route{{
			Name: "frontend", PathPrefix: "/", Action: &config.RouteAction{Static: &config.StaticAction{Root: root}},
		}},
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	response := httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/", nil))
	if response.Code != http.StatusOK || response.Body.String() != "inside root" {
		t.Fatalf("symlinked root = %d %q", response.Code, response.Body.String())
	}
}
