package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"janus/internal/config"

	"go.uber.org/zap"
)

// FileReloader polls one versioned configuration file. The Runtime remains
// responsible for validating startup-owned fields and publishing generations;
// this type only supplies a serialized, hash-deduplicated trigger.
type FileReloader struct {
	runtime  *Runtime
	path     string
	interval time.Duration
	logger   *zap.Logger

	reloadMu         sync.Mutex
	mu               sync.Mutex
	lastAppliedHash  [sha256.Size]byte
	hasAppliedHash   bool
	lastReportedHash [sha256.Size]byte
	hasReportedHash  bool
}

// NewFileReloader validates the current file and records its hash as the
// already-applied baseline. Later polls only attempt changed content.
func NewFileReloader(r *Runtime, path string, interval time.Duration, logger *zap.Logger) (*FileReloader, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime is nil")
	}
	_, hash, err := config.LoadFileSnapshot(path)
	if err != nil {
		return nil, err
	}
	if interval <= 0 {
		interval = time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &FileReloader{
		runtime: r, path: path, interval: interval, logger: logger,
		lastAppliedHash: hash, hasAppliedHash: true,
	}, nil
}

// ReloadOnce checks the file once. Invalid or startup-changing content is
// reported and ignored; the last good generation remains active. Only a
// successfully published hash is skipped on later polls, so transient
// candidate failures can recover without an operator rewriting the file.
func (f *FileReloader) ReloadOnce() error {
	f.reloadMu.Lock()
	defer f.reloadMu.Unlock()

	c, hash, err := config.LoadFileSnapshot(f.path)
	if err != nil {
		f.runtime.metrics.RecordReload("rejected")
		if f.shouldReport(hash) {
			f.logger.Error("configuration reload rejected", zap.String("path", f.path), zap.Error(err))
		}
		return err
	}
	if f.isApplied(hash) {
		return nil
	}
	if err := f.runtime.Replace(c); err != nil {
		f.runtime.metrics.RecordReload("rejected")
		if f.shouldReport(hash) {
			f.logger.Error("configuration reload rejected", zap.String("path", f.path), zap.Error(err))
		}
		return err
	}
	f.markApplied(hash)
	f.runtime.metrics.RecordReload("success")
	f.logger.Info("configuration reloaded", zap.String("path", f.path))
	return nil
}

// Run polls until ctx is cancelled. Reload errors are intentionally non-fatal:
// an operator may be in the middle of an atomic replacement or may have
// published an invalid candidate, and the prior generation must continue.
func (f *FileReloader) Run(ctx context.Context) error {
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = f.ReloadOnce()
		}
	}
}

func (f *FileReloader) isApplied(hash [sha256.Size]byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hasAppliedHash && f.lastAppliedHash == hash
}

func (f *FileReloader) markApplied(hash [sha256.Size]byte) {
	f.mu.Lock()
	f.lastAppliedHash = hash
	f.hasAppliedHash = true
	// A successful publication starts a new reporting window. If this hash
	// later fails after a future source change, it should be observable again.
	f.hasReportedHash = false
	f.mu.Unlock()
}

func (f *FileReloader) shouldReport(hash [sha256.Size]byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasReportedHash && f.lastReportedHash == hash {
		return false
	}
	f.lastReportedHash = hash
	f.hasReportedHash = true
	return true
}
