package store

import (
	"errors"
	"path/filepath"
	"testing"

	"janus/internal/config"
)

func testConfig(name string) config.Config {
	return config.Config{
		Limens:  map[string]config.LimenConfig{"default": {Address: "127.0.0.1:8080", Protocols: []string{"http1"}}},
		Services: map[string]config.Service{"example": {Upstreams: []string{"http://127.0.0.1:9000"}}},
		Routes:   []config.Route{{Name: name, Limen: "default", PathPrefix: "/"}},
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "janus-configs.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreCreateAndGet(t *testing.T) {
	s := openTestStore(t)
	created, err := s.Create("initial", testConfig("a"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.Status != "draft" || created.Name != "initial" {
		t.Fatalf("created = %+v", created)
	}
	got, err := s.Get(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.Content.Routes[0].Name != "a" {
		t.Fatalf("got = %+v", got)
	}
}

func TestStoreUpdatePersistsContent(t *testing.T) {
	s := openTestStore(t)
	created, _ := s.Create("initial", testConfig("a"))
	if err := s.Update(created.ID, "renamed", testConfig("b")); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.Get(created.ID)
	if got.Name != "renamed" || got.Content.Routes[0].Name != "b" {
		t.Fatalf("got = %+v", got)
	}
}

func TestStorePublishMakesSingleActive(t *testing.T) {
	s := openTestStore(t)
	first, _ := s.Create("first", testConfig("a"))
	second, _ := s.Create("second", testConfig("b"))

	if err := s.Publish(first.ID); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	active, err := s.Active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active == nil || active.ID != first.ID {
		t.Fatalf("active = %+v, want id %d", active, first.ID)
	}

	if err := s.Publish(second.ID); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	active, _ = s.Active()
	if active == nil || active.ID != second.ID {
		t.Fatalf("active = %+v, want id %d", active, second.ID)
	}

	list, _ := s.List()
	for _, item := range list {
		if item.ID == first.ID && item.Status != "archived" {
			t.Fatalf("previous active status = %q, want archived", item.Status)
		}
		if item.ID == second.ID && item.Status != "active" {
			t.Fatalf("new active status = %q, want active", item.Status)
		}
	}
}

func TestStoreDeleteRejectsActive(t *testing.T) {
	s := openTestStore(t)
	rec, _ := s.Create("only", testConfig("a"))
	if err := s.Publish(rec.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := s.Delete(rec.ID); err == nil {
		t.Fatal("delete active config should fail")
	}
	draft, _ := s.Create("draft", testConfig("b"))
	if err := s.Delete(draft.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if got, _ := s.Get(draft.ID); got != nil {
		t.Fatal("deleted config still present")
	}
}

func TestStoreUnknownIDReportsNotFound(t *testing.T) {
	s := openTestStore(t)
	if err := s.Update(404, "x", testConfig("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown = %v, want ErrNotFound", err)
	}
	if err := s.Delete(404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete unknown = %v, want ErrNotFound", err)
	}
	if err := s.Publish(404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publish unknown = %v, want ErrNotFound", err)
	}
	got, err := s.Get(404)
	if err != nil || got != nil {
		t.Fatalf("get unknown = %+v, %v", got, err)
	}
}

func TestStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "janus-configs.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rec, _ := first.Create("persisted", testConfig("a"))
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = second.Close() }()
	got, err := second.Get(rec.ID)
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if got == nil || got.Name != "persisted" {
		t.Fatalf("got = %+v", got)
	}
}
