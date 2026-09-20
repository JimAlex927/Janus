package store

import (
	"fmt"
	"os"
)

// Backup makes a consistent SQLite snapshot including WAL contents. The
// destination must not exist. Callers must quiesce publication when pairing
// this with a configuration-file backup; the two files are not one transaction.
func (s *Store) Backup(destination string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("backup SQLite (empty destination may remain): %w", err)
	}
	return nil
}
