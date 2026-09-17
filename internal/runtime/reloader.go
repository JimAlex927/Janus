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

// NewFileReloader optionally accepts the hash of the exact snapshot used to
// build the runtime. Without that evidence the first poll must publish the
// current file, even if it has not changed since watcher construction.
func NewFileReloader(r *Runtime, path string, interval time.Duration, logger *zap.Logger, appliedHash ...[sha256.Size]byte) (*FileReloader, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime is nil")
	}
	if len(appliedHash) > 1 {
		return nil, fmt.Errorf("at most one applied snapshot hash is allowed")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	f := &FileReloader{
		runtime: r, path: path, interval: interval, logger: logger,
	}
	if len(appliedHash) == 1 {
		f.lastAppliedHash, f.hasAppliedHash = appliedHash[0], true
	}
	return f, nil
}

// ReloadOnce checks the file once. Invalid or startup-changing content is
// reported and ignored; the last good generation remains active. Only a
// successfully published hash is skipped on later polls, so transient
// candidate failures can recover without an operator rewriting the file.
func (f *FileReloader) ReloadOnce() error {
	f.reloadMu.Lock()
	defer f.reloadMu.Unlock()
	//这里会加载文件
	c, hash, err := config.LoadFileSnapshot(f.path)
	if err != nil {
		f.runtime.metrics.RecordReload("rejected")
		if f.shouldReport(hash) {
			f.logger.Error("configuration reload rejected", zap.String("path", f.path), zap.Error(err))
		}
		return err
	}
	//如果当前hash已然加载就不需要重新构建runtime
	if f.isApplied(hash) {
		return nil
	}
	//否则用新的配置 构建generation 完成替换
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
			//每隔interval 重置一次
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
