package gateway

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"janus/internal/config"
	"janus/internal/forwarding"
)

var errStaticPathOutsideRoot = errors.New("static path resolves outside root")

// newStaticHandler serves one validated local asset directory. Route
// middleware has already run when this handler receives the request, so a
// configured strip_prefix can provide an explicit mount point.
func newStaticHandler(action config.StaticAction) (http.Handler, error) {
	action = action.WithDefaults()
	root, err := filepath.EvalSymlinks(action.Root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "stat", Path: root, Err: os.ErrNotExist}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		asset, ok := staticAssetPath(root, r.URL)
		if !ok {
			http.NotFound(w, r)
			return
		}
		asset, info, statErr := resolveStaticPath(root, asset)
		requestedDirectory := statErr == nil && info.IsDir()
		if statErr == nil && info.IsDir() {
			asset = filepath.Join(asset, filepath.FromSlash(action.Index))
			asset, info, statErr = resolveStaticPath(root, asset)
		}
		if statErr == nil && !info.IsDir() {
			applyStaticCacheControl(w, action.CacheControl)
			serveStaticFile(w, r, asset, info)
			return
		}
		if requestedDirectory && action.DirectoryListing && os.IsNotExist(statErr) {
			applyStaticCacheControl(w, action.CacheControl)
			listingRequest := staticListingRequest(r)
			http.FileServer(staticFileSystem{
				root:   root,
				prefix: forwarding.ForwardedPrefix(r),
			}).ServeHTTP(w, listingRequest)
			return
		}
		if action.SPAFallback && !requestedDirectory && r.Method == http.MethodGet && os.IsNotExist(statErr) {
			fallback := filepath.Join(root, filepath.FromSlash(action.Index))
			if fallback, fallbackInfo, fallbackErr := resolveStaticPath(root, fallback); fallbackErr == nil && !fallbackInfo.IsDir() {
				applyStaticCacheControl(w, action.CacheControl)
				serveStaticFile(w, r, fallback, fallbackInfo)
				return
			}
		}
		http.NotFound(w, r)
	}), nil
}

// staticListingRequest restores the externally visible URI for the standard
// file server. Route middleware intentionally rewrites r.URL.Path for the
// action, but directory redirects and relative listing links must still use
// the public mount path (for example /test/subdir/).
func staticListingRequest(r *http.Request) *http.Request {
	prefix := forwarding.ForwardedPrefix(r)
	if prefix == "" {
		return r
	}
	listingRequest := r.Clone(r.Context())
	u := *r.URL
	if r.RequestURI != "" {
		if external, err := url.ParseRequestURI(r.RequestURI); err == nil && external.Path != "" {
			u.Path = external.Path
			u.RawPath = external.RawPath
			u.RawQuery = external.RawQuery
			listingRequest.URL = &u
			return listingRequest
		}
	}
	u.Path = prefix + u.Path
	if u.RawPath != "" {
		u.RawPath = (&url.URL{Path: prefix}).EscapedPath() + u.RawPath
	}
	listingRequest.URL = &u
	return listingRequest
}

func serveStaticFile(w http.ResponseWriter, r *http.Request, asset string, info os.FileInfo) {
	file, err := os.Open(asset)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	// ServeContent reads from the already-open file, preventing a later path
	// replacement from changing the resource after confinement was checked.
	http.ServeContent(w, r, filepath.Base(asset), info.ModTime(), file)
}

// staticFileSystem keeps http.FileServer's directory formatting while mapping
// the public mount prefix back to the configured root and ensuring that every
// file it opens still resolves beneath that root.
type staticFileSystem struct {
	root   string
	prefix string
}

func (fs staticFileSystem) Open(name string) (http.File, error) {
	clean := path.Clean("/" + name)
	if fs.prefix != "" {
		prefix := path.Clean(fs.prefix)
		switch {
		case clean == prefix:
			clean = "/"
		case strings.HasPrefix(clean, prefix+"/"):
			clean = strings.TrimPrefix(clean, prefix)
		default:
			return nil, os.ErrNotExist
		}
	}
	asset := filepath.Join(fs.root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	resolved, _, err := resolveStaticPath(fs.root, asset)
	if err != nil {
		return nil, err
	}
	return os.Open(resolved)
}

func resolveStaticPath(root, asset string) (string, os.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(asset)
	if err != nil {
		return "", nil, err
	}
	if !staticPathWithinRoot(root, resolved) {
		return "", nil, errStaticPathOutsideRoot
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	return resolved, info, nil
}

func staticPathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
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
	if !staticPathWithinRoot(root, asset) {
		return "", false
	}
	return asset, true
}
