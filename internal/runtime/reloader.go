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

	mu       sync.Mutex
	lastHash [sha256.Size]byte
	hasHash  bool
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
		lastHash: hash, hasHash: true,
	}, nil
}

// ReloadOnce checks the file once. Invalid or startup-changing content is
// reported and ignored; the last good generation remains active. The changed
// content hash is remembered so an unchanged bad file does not spam logs.
func (f *FileReloader) ReloadOnce() error {
	c, hash, err := config.LoadFileSnapshot(f.path)
	if err != nil {
		if f.remember(hash) {
			f.logger.Error("configuration reload rejected", zap.String("path", f.path), zap.Error(err))
		}
		return err
	}
	if !f.remember(hash) {
		return nil
	}
	if err := f.runtime.Replace(c); err != nil {
		f.logger.Error("configuration reload rejected", zap.String("path", f.path), zap.Error(err))
		return err
	}
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

func (f *FileReloader) remember(hash [sha256.Size]byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasHash && f.lastHash == hash {
		return false
	}
	f.lastHash = hash
	f.hasHash = true
	return true
}
