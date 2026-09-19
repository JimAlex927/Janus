// Package store provides SQLite-backed persistence for configuration drafts.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"janus/internal/config"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS configs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	content TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'draft',
	layout TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_configs_status ON configs(status);
`

// ErrNotFound is returned when a config id does not exist.
var ErrNotFound = errors.New("config not found")

// ErrActiveImmutable prevents an already published record from being edited
// in place. Callers that need an editable version should use ForkActive.
var ErrActiveImmutable = errors.New("active configuration is immutable; duplicate it before editing")

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := ensureLayoutColumn(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// parseStoreTime reads timestamps written by this package (RFC3339) and
// tolerates the legacy "2006-01-02 15:04:05" format of earlier databases.
func parseStoreTime(value string) time.Time {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.DateTime, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05 +0000 UTC", value); err == nil {
		return parsed
	}
	return time.Time{}
}

// ensureLayoutColumn migrates databases created before the canvas layout
// column existed. Fresh databases already contain it via schema.
func ensureLayoutColumn(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(configs)")
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("inspect schema: %w", err)
		}
		if name == "layout" {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	if _, err := db.Exec("ALTER TABLE configs ADD COLUMN layout TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("migrate layout column: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

type ConfigRecord struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Content   config.Config `json:"content"`
	Status    string        `json:"status"` // draft / active / archived
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// ConfigListOptions controls one status-filtered page. Time bounds use the
// same RFC3339 representation stored in the database.
type ConfigListOptions struct {
	Limit         int
	Offset        int
	CreatedAtFrom string
	CreatedAtTo   string
	UpdatedAtFrom string
	UpdatedAtTo   string
	SortBy        string
	SortOrder     string
}

// ListPage returns one status-filtered page using the historical default
// ordering. Callers that need filters or custom ordering should use
// ListPageWithOptions.
func (s *Store) ListPage(status string, limit, offset int) ([]ConfigRecord, int, error) {
	return s.ListPageWithOptions(status, ConfigListOptions{Limit: limit, Offset: offset})
}

// ListPageWithOptions returns one status-filtered page and the full count for
// that status after applying time filters. SortBy and SortOrder are strictly
// whitelisted before being interpolated into the SQL statement.
func (s *Store) ListPageWithOptions(status string, options ConfigListOptions) ([]ConfigRecord, int, error) {
	if status != "active" && status != "draft" && status != "archived" {
		return nil, 0, fmt.Errorf("invalid configuration status %q", status)
	}
	if options.Limit <= 0 || options.Limit > 100 || options.Offset < 0 {
		return nil, 0, fmt.Errorf("invalid configuration page")
	}
	sortColumn := "updated_at"
	if options.SortBy == "createdAt" || options.SortBy == "created_at" {
		sortColumn = "created_at"
	} else if options.SortBy != "" && options.SortBy != "updatedAt" && options.SortBy != "updated_at" {
		return nil, 0, fmt.Errorf("invalid configuration sort field %q", options.SortBy)
	}
	sortDirection := "DESC"
	if options.SortOrder == "asc" {
		sortDirection = "ASC"
	} else if options.SortOrder != "" && options.SortOrder != "desc" {
		return nil, 0, fmt.Errorf("invalid configuration sort order %q", options.SortOrder)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	where := []string{"status = ?"}
	args := []any{status}
	for _, bound := range []struct{ column, operator, value string }{
		{"created_at", ">=", options.CreatedAtFrom}, {"created_at", "<=", options.CreatedAtTo},
		{"updated_at", ">=", options.UpdatedAtFrom}, {"updated_at", "<=", options.UpdatedAtTo},
	} {
		if bound.value != "" {
			where = append(where, bound.column+" "+bound.operator+" ?")
			args = append(args, bound.value)
		}
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM configs WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := fmt.Sprintf("SELECT id, name, content, status, created_at, updated_at FROM configs WHERE %s ORDER BY %s %s, id %s LIMIT ? OFFSET ?", whereSQL, sortColumn, sortDirection, sortDirection)
	args = append(args, options.Limit, options.Offset)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]ConfigRecord, 0, options.Limit)
	for rows.Next() {
		var record ConfigRecord
		var raw, created, updated string
		if err := rows.Scan(&record.ID, &record.Name, &raw, &record.Status, &created, &updated); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(raw), &record.Content); err != nil {
			return nil, 0, fmt.Errorf("decode config %d: %w", record.ID, err)
		}
		record.CreatedAt = parseStoreTime(created)
		record.UpdatedAt = parseStoreTime(updated)
		result = append(result, record)
	}
	return result, total, rows.Err()
}

func (s *Store) List() ([]ConfigRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT id, name, content, status, created_at, updated_at FROM configs ORDER BY updated_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigRecord
	for rows.Next() {
		var r ConfigRecord
		var raw string
		var created, updated string
		if err := rows.Scan(&r.ID, &r.Name, &raw, &r.Status, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r.Content); err != nil {
			return nil, fmt.Errorf("decode config %d: %w", r.ID, err)
		}
		r.CreatedAt = parseStoreTime(created)
		r.UpdatedAt = parseStoreTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Get(id int64) (*ConfigRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

func (s *Store) getLocked(id int64) (*ConfigRecord, error) {
	var r ConfigRecord
	var raw, created, updated string
	err := s.db.QueryRow("SELECT id, name, content, status, created_at, updated_at FROM configs WHERE id = ?", id).
		Scan(&r.ID, &r.Name, &raw, &r.Status, &created, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &r.Content); err != nil {
		return nil, fmt.Errorf("decode config %d: %w", id, err)
	}
	r.CreatedAt = parseStoreTime(created)
	r.UpdatedAt = parseStoreTime(updated)
	return &r, nil
}

func (s *Store) Active() (*ConfigRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.db.QueryRow("SELECT id FROM configs WHERE status = 'active' LIMIT 1").Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.getLocked(id)
}

// ReconcileActive repairs the library marker after a crash in the narrow
// window between configuration-file publication and the status transaction.
// It only promotes an existing record whose normalized content matches the
// running startup snapshot; an unknown file is reported to the caller rather
// than silently creating a new history entry.
func (s *Store) ReconcileActive(content config.Config) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := content.WithDefaults()
	var activeID int64
	var activeRaw string
	err := s.db.QueryRow("SELECT id, content FROM configs WHERE status = 'active' LIMIT 1").Scan(&activeID, &activeRaw)
	if err == nil {
		var active config.Config
		if err := json.Unmarshal([]byte(activeRaw), &active); err != nil {
			return false, fmt.Errorf("decode active config %d: %w", activeID, err)
		}
		if reflect.DeepEqual(active.WithDefaults(), target) {
			return true, nil
		}
	} else if err != sql.ErrNoRows {
		return false, err
	}
	rows, err := s.db.Query("SELECT id, content FROM configs ORDER BY id DESC")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	matchID := int64(0)
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return false, err
		}
		var candidate config.Config
		if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
			return false, fmt.Errorf("decode config %d: %w", id, err)
		}
		if reflect.DeepEqual(candidate.WithDefaults(), target) {
			matchID = id
			break
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if matchID == 0 {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec("UPDATE configs SET status = 'archived', updated_at = ? WHERE status = 'active'", now); err != nil {
		return false, err
	}
	if _, err := tx.Exec("UPDATE configs SET status = 'active', updated_at = ? WHERE id = ?", now, matchID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Create(name string, content config.Config) (*ConfigRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec("INSERT INTO configs(name, content, status, created_at, updated_at) VALUES(?, ?, 'draft', ?, ?)",
		name, string(raw), now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getLocked(id)
}

// ForkActive creates an editable draft from the active record while keeping
// the published record immutable. Publishing the returned draft will archive
// the previous active record, preserving it as rollback history.
func (s *Store) ForkActive(id int64, name string, content config.Config, layout string) (*ConfigRecord, error) {
	if err := validateLayout(layout); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRow("SELECT status FROM configs WHERE id = ?", id).Scan(&status); err == sql.ErrNoRows {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	} else if status != "active" {
		return nil, ErrActiveImmutable
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)
	result, err := tx.Exec("INSERT INTO configs(name, content, status, layout, created_at, updated_at) VALUES(?, ?, 'draft', ?, ?, ?)",
		name, string(raw), layout, stamp, stamp)
	if err != nil {
		return nil, err
	}
	newID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ConfigRecord{ID: newID, Name: name, Content: content, Status: "draft", CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) Update(id int64, name string, content config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDraftLocked(id); err != nil {
		return err
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec("UPDATE configs SET name = ?, content = ?, updated_at = ? WHERE id = ?",
		name, string(raw), now, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateWithLayout replaces a draft's content and canvas sidecar in one
// transaction. The admin API uses this path so a malformed layout or a
// storage failure cannot leave the draft half-updated.
func (s *Store) UpdateWithLayout(id int64, name string, content config.Config, layout string) error {
	if err := validateLayout(layout); err != nil {
		return err
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRow("SELECT status FROM configs WHERE id = ?", id).Scan(&status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if status == "active" {
		return ErrActiveImmutable
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.Exec("UPDATE configs SET name = ?, content = ?, layout = ?, updated_at = ? WHERE id = ?",
		name, string(raw), layout, now, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) Publish(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int64
	if err := tx.QueryRow("SELECT id FROM configs WHERE id = ?", id).Scan(&exists); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec("UPDATE configs SET status = 'archived', updated_at = ? WHERE status = 'active'", now); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE configs SET status = 'active', updated_at = ? WHERE id = ?", now, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var status string
	err := s.db.QueryRow("SELECT status FROM configs WHERE id = ?", id).Scan(&status)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "active" {
		return fmt.Errorf("cannot delete active config")
	}
	_, err = s.db.Exec("DELETE FROM configs WHERE id = ?", id)
	return err
}

// Layout returns the raw canvas layout sidecar of one record. An empty
// layout is reported as "{}".
func (s *Store) Layout(id int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var layout sql.NullString
	err := s.db.QueryRow("SELECT layout FROM configs WHERE id = ?", id).Scan(&layout)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !layout.Valid || layout.String == "" {
		return "{}", nil
	}
	return layout.String, nil
}

// SaveLayout replaces the canvas layout sidecar of one record. Layouts only
// carry node coordinates, so validation is limited to a size bound and a
// JSON object shape check.
func (s *Store) SaveLayout(id int64, layout string) error {
	if err := validateLayout(layout); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDraftLocked(id); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec("UPDATE configs SET layout = ?, updated_at = ? WHERE id = ?", layout, now, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ensureDraftLocked(id int64) error {
	var status string
	err := s.db.QueryRow("SELECT status FROM configs WHERE id = ?", id).Scan(&status)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "active" {
		return ErrActiveImmutable
	}
	return nil
}

func validateLayout(layout string) error {
	if len(layout) > 256<<10 {
		return fmt.Errorf("layout exceeds %d bytes", 256<<10)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(layout), &probe); err != nil {
		return fmt.Errorf("invalid layout JSON: %w", err)
	}
	if probe == nil {
		return errors.New("invalid layout JSON: expected an object")
	}
	return nil
}
