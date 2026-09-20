package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishStorageFailureRollsBackAndBackupRestores(t *testing.T) {
	s := openTestStore(t)
	old, err := s.Create("old", testConfig("old"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(old.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.Create("next", testConfig("next"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_publish BEFORE UPDATE OF status ON configs WHEN NEW.status = 'active' BEGIN SELECT RAISE(ABORT, 'injected disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if s.Publish(next.ID) == nil {
		t.Fatal("storage failure ignored")
	}
	active, err := s.Active()
	if err != nil || active.ID != old.ID {
		t.Fatalf("lost old active: %v %v", active, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_publish`); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.db")
	if err = s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if s.Backup(backup) == nil {
		t.Fatal("backup overwrote existing snapshot")
	}
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("backup permissions %v %v", info, err)
	}
	if err = s.Publish(next.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	active, err = restored.Active()
	if err != nil || active.ID != old.ID {
		t.Fatalf("backup not point-in-time %v %v", active, err)
	}
	if records, err := restored.List(); err != nil || len(records) != 2 {
		t.Fatalf("backup lost history %v %v", records, err)
	}
}

func TestTimestampNormalizationPreservesFractionalOrderAndBounds(t *testing.T) {
	s := openTestStore(t)
	for _, stamp := range []string{"2026-09-20T12:00:00Z", "2026-09-20T12:00:00.1Z", "2026-09-20T12:00:00.12Z"} {
		record, err := s.Create(stamp, testConfig("test"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec("UPDATE configs SET created_at = ?, updated_at = ? WHERE id = ?", stamp, stamp, record.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := normalizeStoreTimes(s.db); err != nil {
		t.Fatal(err)
	}
	records, total, err := s.ListPageWithOptions("draft", ConfigListOptions{Limit: 10, SortOrder: "asc", UpdatedAtFrom: "2026-09-20T12:00:00Z", UpdatedAtTo: "2026-09-20T12:00:00.12Z"})
	if err != nil || total != 3 || len(records) != 3 {
		t.Fatalf("records=%v total=%d error=%v", records, total, err)
	}
	for i, record := range records {
		if record.ID != int64(i+1) {
			t.Fatalf("wrong fractional order: %v", records)
		}
	}
}
