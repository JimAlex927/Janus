package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"janus/internal/config"
	janusruntime "janus/internal/runtime"
	"janus/internal/store"
)

func publicationConfig(name string) config.Config {
	return (config.Config{Version: config.CurrentConfigVersion, Limens: map[string]config.LimenConfig{"private": {Address: "127.0.0.1:8080", Protocols: []string{config.ProtocolHTTP1}}}, Routes: []config.Route{{Name: name, Limen: "private", PathPrefix: "/", Action: &config.RouteAction{Respond: &config.RespondAction{Status: 200, Body: name}}}}}).WithDefaults()
}

func TestPublicationCrashBeforeLibraryMarkerRecoversFromFile(t *testing.T) {
	if directory := os.Getenv("JANUS_TEST_PUBLICATION_CRASH"); directory != "" {
		path := filepath.Join(directory, "config.json")
		old := publicationConfig("old")
		next := publicationConfig("new")
		if err := writeConfigAtomically(path, old); err != nil {
			t.Fatal(err)
		}
		library, err := store.Open(filepath.Join(directory, "configs.db"))
		if err != nil {
			t.Fatal(err)
		}
		a, err := library.Create("old", old)
		if err != nil {
			t.Fatal(err)
		}
		if err = library.Publish(a.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = library.Create("new", next); err != nil {
			t.Fatal(err)
		}
		r, err := janusruntime.New(old, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		if err = publishConfig(path, r, next, r.Revision()); err != nil {
			t.Fatal(err)
		}
		// Abrupt process exit: no Store.Close/checkpoint, no library.Publish,
		// and no runtime drain. The file is the durable startup authority.
		os.Exit(23)
	}
	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestPublicationCrashBeforeLibraryMarkerRecoversFromFile$")
	child.Env = append(os.Environ(), "JANUS_TEST_PUBLICATION_CRASH="+dir)
	output, err := child.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 23 {
		t.Fatalf("child: %v %s", err, output)
	}
	loaded, err := config.LoadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Routes[0].Name != "new" {
		t.Fatal("published file lost")
	}
	library := openConfigLibrary(filepath.Join(dir, "configs.db"), loaded, zap.NewNop())
	if library == nil {
		t.Fatal("recovery failed")
	}
	defer library.Close()
	active, err := library.Active()
	if err != nil || active.Content.Routes[0].Name != "new" {
		t.Fatalf("marker not reconciled %v %v", active, err)
	}
	r, err := janusruntime.New(loaded, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.ConfigSnapshot().Routes[0].Name != "new" {
		t.Fatal("runtime differs from file")
	}
}

func TestPublicationFileFailureKeepsRuntime(t *testing.T) {
	old := publicationConfig("old")
	next := publicationConfig("new")
	r, err := janusruntime.New(old, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// A directory at the final filename deterministically makes rename fail,
	// without depending on chmod behavior under elevated test runners.
	path := filepath.Join(t.TempDir(), "config.json")
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = publishConfig(path, r, next, r.Revision()); err == nil {
		t.Fatal("file error ignored")
	}
	if r.Revision() != 1 || r.ConfigSnapshot().Routes[0].Name != "old" {
		t.Fatal("failed persistence activated candidate")
	}
}

func TestStoredPublicationPersistsLimenForNextStart(t *testing.T) {
	old := publicationConfig("old")
	r, err := janusruntime.New(old, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := writeConfigAtomically(path, old); err != nil {
		t.Fatal(err)
	}
	running := publicationConfig("new")
	onDisk := publicationConfig("new")
	onDisk.Limens["private"] = config.LimenConfig{Address: "127.0.0.1:8181", Protocols: []string{config.ProtocolHTTP1}}
	if err := publishStoredConfig(path, r, running, onDisk, r.Revision()); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Limens["private"].Address != "127.0.0.1:8181" || loaded.Routes[0].Name != "new" {
		t.Fatalf("published file lost draft: %+v", loaded)
	}
	if r.ConfigSnapshot().Limens["private"].Address != "127.0.0.1:8080" || r.ConfigSnapshot().Routes[0].Name != "new" {
		t.Fatalf("running snapshot changed listener or lost hot route: %+v", r.ConfigSnapshot())
	}
}

func TestStoredPublicationWriteFailureKeepsRuntime(t *testing.T) {
	old := publicationConfig("old")
	r, err := janusruntime.New(old, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	running := publicationConfig("new")
	onDisk := publicationConfig("new")
	onDisk.Limens["private"] = config.LimenConfig{Address: "127.0.0.1:8181", Protocols: []string{config.ProtocolHTTP1}}
	if err := publishStoredConfig(path, r, running, onDisk, r.Revision()); err == nil {
		t.Fatal("file error ignored")
	}
	if r.Revision() != 1 || r.ConfigSnapshot().Routes[0].Name != "old" || r.ConfigSnapshot().Limens["private"].Address != "127.0.0.1:8080" {
		t.Fatal("failed persistence activated candidate")
	}
}

func TestStoredPublicationRejectsMissingClientCA(t *testing.T) {
	old := publicationConfig("old")
	r, err := janusruntime.New(old, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := writeConfigAtomically(path, old); err != nil {
		t.Fatal(err)
	}
	onDisk := publicationConfig("new")
	binding := onDisk.Limens["private"]
	binding.TLS = &config.TLSSettings{CertFile: "server.crt", KeyFile: "server.key", ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: "missing/ca.crt"}
	onDisk.Limens["private"] = binding
	err = publishStoredConfig(path, r, old, onDisk, r.Revision())
	if err == nil || !strings.Contains(err.Error(), "client CA") {
		t.Fatalf("missing client CA accepted: %v", err)
	}
	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Limens["private"].TLS != nil || loaded.Routes[0].Name != "old" || r.Revision() != 1 {
		t.Fatal("invalid certificate path changed the file or runtime")
	}
}
