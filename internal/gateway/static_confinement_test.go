package gateway

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"janus/internal/config"
)

func TestStaticRootPinnedAcrossPathReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	outside := filepath.Join(parent, "outside")
	os.Mkdir(root, 0700)
	os.Mkdir(outside, 0700)
	os.WriteFile(filepath.Join(root, "index.html"), []byte("inside"), 0600)
	os.WriteFile(filepath.Join(outside, "index.html"), []byte("SECRET"), 0600)
	h, err := newStaticHandler(config.StaticAction{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer h.(*staticHandler).Close()
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || w.Body.String() != "inside" {
		t.Fatalf("root replaced: %d %s", w.Code, w.Body)
	}
}

func TestStaticSymlinkSwapCannotEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.Mkdir(filepath.Join(root, "safe"), 0700)
	os.WriteFile(filepath.Join(root, "safe", "index.html"), []byte("inside"), 0600)
	os.WriteFile(filepath.Join(outside, "index.html"), []byte("SECRET"), 0600)
	os.Symlink("safe", filepath.Join(root, "link"))
	h, err := newStaticHandler(config.StaticAction{Root: root, DirectoryListing: true, SPAFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.(*staticHandler).Close()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, target := range []string{outside, "safe"} {
				tmp := filepath.Join(root, "replacement")
				if err := os.Symlink(target, tmp); err != nil {
					return
				}
				if err := os.Rename(tmp, filepath.Join(root, "link")); err != nil {
					return
				}
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	for range 500 {
		for _, path := range []string{"/link/index.html", "/link/", "/link/missing"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if strings.Contains(w.Body.String(), "SECRET") {
				t.Fatal("escaped static root during rename")
			}
		}
	}
}
