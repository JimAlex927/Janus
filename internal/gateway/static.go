package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"janus/internal/config"
	"janus/internal/forwarding"
)

type staticHandler struct {
	http.Handler
	root *os.Root
}

func (h *staticHandler) Close() error { return h.root.Close() }

// newStaticHandler serves one validated local asset directory. Route
// middleware has already run when this handler receives the request, so a
// configured strip_prefix can provide an explicit mount point.
func newStaticHandler(action config.StaticAction) (http.Handler, error) {
	action = action.WithDefaults()
	root, err := filepath.Abs(action.Root)
	if err != nil {
		return nil, err
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	return &staticHandler{root: confined, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		asset, _ = filepath.Rel(root, asset)
		file, info, statErr := openStaticFile(confined, asset)
		requestedDirectory := statErr == nil && info.IsDir()
		if statErr == nil && info.IsDir() {
			_ = file.Close()
			asset = filepath.Join(asset, filepath.FromSlash(action.Index))
			file, info, statErr = openStaticFile(confined, asset)
		}
		if statErr == nil {
			defer file.Close()
		}
		if statErr == nil && !info.IsDir() {
			applyStaticCacheControl(w, action.CacheControl)
			http.ServeContent(w, r, filepath.Base(asset), info.ModTime(), file)
			return
		}
		if requestedDirectory && action.DirectoryListing && os.IsNotExist(statErr) {
			applyStaticCacheControl(w, action.CacheControl)
			listingRequest := staticListingRequest(r)
			http.FileServer(staticFileSystem{
				root:   confined,
				prefix: forwarding.ForwardedPrefix(r),
			}).ServeHTTP(w, listingRequest)
			return
		}
		if action.SPAFallback && !requestedDirectory && r.Method == http.MethodGet && os.IsNotExist(statErr) {
			fallback := filepath.FromSlash(action.Index)
			if file, fallbackInfo, fallbackErr := openStaticFile(confined, fallback); fallbackErr == nil {
				defer file.Close()
				if fallbackInfo.IsDir() {
					http.NotFound(w, r)
					return
				}
				applyStaticCacheControl(w, action.CacheControl)
				http.ServeContent(w, r, filepath.Base(fallback), fallbackInfo.ModTime(), file)
				return
			}
		}
		http.NotFound(w, r)
	})}, nil
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

func openStaticFile(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

// staticFileSystem keeps http.FileServer's directory formatting while mapping
// the public mount prefix back to the configured root and ensuring that every
// file it opens still resolves beneath that root.
type staticFileSystem struct {
	root   *os.Root
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
	name = strings.TrimPrefix(clean, "/")
	if name == "" {
		name = "."
	}
	return fs.root.Open(filepath.FromSlash(name))
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
