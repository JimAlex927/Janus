package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"janus/internal/config"
)

// newStaticHandler serves one validated local asset directory. Route
// middleware has already run when this handler receives the request, so a
// configured strip_prefix can provide an explicit mount point.
func newStaticHandler(action config.StaticAction) (http.Handler, error) {
	action = action.WithDefaults()
	info, err := os.Stat(action.Root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "stat", Path: action.Root, Err: os.ErrNotExist}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		asset, ok := staticAssetPath(action.Root, r.URL)
		if !ok {
			http.NotFound(w, r)
			return
		}
		info, statErr := os.Stat(asset)
		requestedDirectory := statErr == nil && info.IsDir()
		if statErr == nil && info.IsDir() {
			asset = filepath.Join(asset, filepath.FromSlash(action.Index))
			info, statErr = os.Stat(asset)
		}
		if statErr == nil && !info.IsDir() {
			applyStaticCacheControl(w, action.CacheControl)
			http.ServeFile(w, r, asset)
			return
		}
		if requestedDirectory && action.DirectoryListing && os.IsNotExist(statErr) {
			applyStaticCacheControl(w, action.CacheControl)
			http.FileServer(http.Dir(action.Root)).ServeHTTP(w, r)
			return
		}
		if action.SPAFallback && !requestedDirectory && r.Method == http.MethodGet && os.IsNotExist(statErr) {
			fallback := filepath.Join(action.Root, filepath.FromSlash(action.Index))
			if fallbackInfo, fallbackErr := os.Stat(fallback); fallbackErr == nil && !fallbackInfo.IsDir() {
				applyStaticCacheControl(w, action.CacheControl)
				http.ServeFile(w, r, fallback)
				return
			}
		}
		http.NotFound(w, r)
	}), nil
}

func applyStaticCacheControl(w http.ResponseWriter, value string) {
	if value != "" {
		w.Header().Set("Cache-Control", value)
	}
}

// staticAssetPath decodes the URL path once, rejects platform path separators
// and resolves only clean relative names underneath root.
func staticAssetPath(root string, requestURL *url.URL) (string, bool) {
	if requestURL == nil {
		return "", false
	}
	raw := requestURL.EscapedPath()
	decoded, err := url.PathUnescape(raw)
	if err != nil || strings.ContainsAny(decoded, "\\\x00") {
		return "", false
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return "", false
		}
	}
	clean := path.Clean("/" + decoded)
	relative := strings.TrimPrefix(clean, "/")
	if relative == "." {
		relative = ""
	}
	asset := filepath.Join(root, filepath.FromSlash(relative))
	rootWithSeparator := root + string(os.PathSeparator)
	if asset != root && !strings.HasPrefix(asset, rootWithSeparator) {
		return "", false
	}
	return asset, true
}
