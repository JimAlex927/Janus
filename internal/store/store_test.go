package store

import (
	"errors"
	"path/filepath"
	"testing"

	"janus/internal/config"
)

func testConfig(name string) config.Config {
	return config.Config{
		Limens:   map[string]config.LimenConfig{"default": {Address: "127.0.0.1:8080", Protocols: []string{"http1"}}},
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

func TestStoreUpdateWithLayoutIsAtomicAndRequiresObject(t *testing.T) {
	s := openTestStore(t)
	record, err := s.Create("initial", testConfig("before"))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateWithLayout(record.ID, "changed", testConfig("after"), "[]"); err == nil {
		t.Fatal("array layout should be rejected")
	}
	got, err := s.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "initial" || got.Content.Routes[0].Name != "before" {
		t.Fatalf("invalid layout partially updated record: %+v", got)
	}
	layout, err := s.Layout(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if layout != "{}" {
		t.Fatalf("layout after rejected update = %q, want {}", layout)
	}

	if err := s.UpdateWithLayout(record.ID, "changed", testConfig("after"), `{"route":{"x":12}}`); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(record.ID)
	if err != nil || got.Name != "changed" || got.Content.Routes[0].Name != "after" {
		t.Fatalf("updated record = %+v, %v", got, err)
	}
	layout, err = s.Layout(record.ID)
	if err != nil || layout != `{"route":{"x":12}}` {
		t.Fatalf("updated layout = %q, %v", layout, err)
	}
}

func TestStoreSaveLayoutRejectsNull(t *testing.T) {
	s := openTestStore(t)
	record, err := s.Create("initial", testConfig("before"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLayout(record.ID, "null"); err == nil {
		t.Fatal("null layout should be rejected")
	}
}

func TestStorePublishedRecordsAreImmutable(t *testing.T) {
	s := openTestStore(t)
	record, err := s.Create("published", testConfig("before"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(record.ID, "changed", testConfig("after")); !errors.Is(err, ErrActiveImmutable) {
		t.Fatalf("active update = %v, want ErrActiveImmutable", err)
	}
	if err := s.UpdateWithLayout(record.ID, "changed", testConfig("after"), `{"x":1}`); !errors.Is(err, ErrActiveImmutable) {
		t.Fatalf("active update with layout = %v, want ErrActiveImmutable", err)
	}
	if err := s.SaveLayout(record.ID, `{"x":1}`); !errors.Is(err, ErrActiveImmutable) {
		t.Fatalf("active layout update = %v, want ErrActiveImmutable", err)
	}
	got, err := s.Get(record.ID)
	if err != nil || got.Name != "published" || got.Content.Routes[0].Name != "before" {
		t.Fatalf("active record changed after rejected writes: %+v, %v", got, err)
	}
}

func TestStoreForkActivePreservesRollbackHistory(t *testing.T) {
	s := openTestStore(t)
	active, err := s.Create("production", testConfig("before"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(active.ID); err != nil {
		t.Fatal(err)
	}
	fork, err := s.ForkActive(active.ID, "production", testConfig("after"), `{"route":{"x":12}}`)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Status != "draft" || fork.ID == active.ID {
		t.Fatalf("fork = %#v, want a new draft", fork)
	}
	current, _ := s.Active()
	if current == nil || current.ID != active.ID || current.Content.Routes[0].Name != "before" {
		t.Fatalf("active changed during fork: %#v", current)
	}
	if err := s.Publish(fork.ID); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Active()
	if current == nil || current.ID != fork.ID || current.Content.Routes[0].Name != "after" {
		t.Fatalf("published fork = %#v", current)
	}
	history, _ := s.Get(active.ID)
	if history == nil || history.Status != "archived" || history.Content.Routes[0].Name != "before" {
		t.Fatalf("rollback history = %#v", history)
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

func TestStoreListPageSeparatesStatusAndOffsets(t *testing.T) {
	s := openTestStore(t)
	first, _ := s.Create("first", testConfig("first"))
	second, _ := s.Create("second", testConfig("second"))
	third, _ := s.Create("third", testConfig("third"))
	draft, _ := s.Create("draft", testConfig("draft"))
	if err := s.Publish(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(second.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(third.ID); err != nil {
		t.Fatal(err)
	}

	history, total, err := s.ListPage("archived", 1, 0)
	if err != nil || total != 2 || len(history) != 1 {
		t.Fatalf("history page = %#v, total=%d, err=%v", history, total, err)
	}
	secondPage, total, err := s.ListPage("archived", 1, 1)
	if err != nil || total != 2 || len(secondPage) != 1 || secondPage[0].ID == history[0].ID {
		t.Fatalf("second history page = %#v, total=%d, err=%v", secondPage, total, err)
	}
	drafts, total, err := s.ListPage("draft", 12, 0)
	if err != nil || total != 1 || len(drafts) != 1 || drafts[0].ID != draft.ID {
		t.Fatalf("draft page = %#v, total=%d, err=%v", drafts, total, err)
	}
	active, total, err := s.ListPage("active", 1, 0)
	if err != nil || total != 1 || len(active) != 1 || active[0].ID != third.ID {
		t.Fatalf("active page = %#v, total=%d, err=%v", active, total, err)
	}
	if _, _, err := s.ListPage("unknown", 1, 0); err == nil {
		t.Fatal("unknown status was accepted")
	}
}

func TestStoreReconcileActivePromotesMatchingHistory(t *testing.T) {
	s := openTestStore(t)
	first := testConfig("first")
	firstRecord, err := s.Create("first", first)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(firstRecord.ID); err != nil {
		t.Fatal(err)
	}
	second := testConfig("second")
	secondRecord, err := s.Create("second", second)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := s.ReconcileActive(second)
	if err != nil || !matched {
		t.Fatalf("reconcile = %v, %v", matched, err)
	}
	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.ID != secondRecord.ID || active.Status != "active" {
		t.Fatalf("active after reconcile = %#v", active)
	}
}

func TestStoreReconcileActiveLeavesUnknownFileUnchanged(t *testing.T) {
	s := openTestStore(t)
	first := testConfig("first")
	record, err := s.Create("first", first)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(record.ID); err != nil {
		t.Fatal(err)
	}
	matched, err := s.ReconcileActive(testConfig("untracked"))
	if err != nil || matched {
		t.Fatalf("unknown reconcile = %v, %v", matched, err)
	}
	active, err := s.Active()
	if err != nil || active == nil || active.ID != record.ID {
		t.Fatalf("active after unknown reconcile = %#v, %v", active, err)
	}
}

func TestStoreReconcileActiveKeepsMatchingActiveDuplicate(t *testing.T) {
	s := openTestStore(t)
	content := testConfig("same")
	active, err := s.Create("active", content)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(active.ID); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Create("duplicate", content)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := s.ReconcileActive(content)
	if err != nil || !matched {
		t.Fatalf("duplicate reconcile = %v, %v", matched, err)
	}
	got, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != active.ID || got.ID == duplicate.ID {
		t.Fatalf("active duplicate reconciliation selected %#v, want id %d", got, active.ID)
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
