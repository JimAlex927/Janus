package limen

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"janus/internal/config"

	"go.uber.org/zap"
)

type certificateEntry struct {
	name     string
	limen    *Limen
	settings config.TLSSettings
	last     certificateFingerprint
	hasLast  bool
}

type certificateFingerprint struct {
	cert [sha256.Size]byte
	key  [sha256.Size]byte
}

// CertificateReloader polls the certificate and key files for TLS Limens.
// Paths and TLS policy are startup-owned; only a validated pair's contents
// are published to the existing Limen.
type CertificateReloader struct {
	mu       sync.Mutex
	entries  []certificateEntry
	interval time.Duration
	logger   *zap.Logger
}

// NewCertificateReloader records the current certificate hashes as the
// already-applied baseline. NewCertificateReloader expects startup to have
// already loaded and validated each pair through NewBinding.
func NewCertificateReloader(bindings map[string]config.LimenConfig, servers map[string]*Limen, interval time.Duration, logger *zap.Logger) (*CertificateReloader, error) {
	if interval <= 0 {
		interval = time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	names := make([]string, 0, len(bindings))
	for name, binding := range bindings {
		if binding.TLS != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	entries := make([]certificateEntry, 0, len(names))
	for _, name := range names {
		server := servers[name]
		if server == nil {
			return nil, fmt.Errorf("TLS limen %q has no server", name)
		}
		settings := *bindings[name].TLS
		fingerprint, err := fingerprintCertificate(settings)
		if err != nil {
			return nil, fmt.Errorf("limen %q: %w", name, err)
		}
		entries = append(entries, certificateEntry{
			name: name, limen: server, settings: settings,
			last: fingerprint, hasLast: true,
		})
	}
	return &CertificateReloader{entries: entries, interval: interval, logger: logger}, nil
}

// ReloadOnce checks every configured certificate pair once. A malformed or
// incomplete pair leaves the current certificate active and is retried after
// the files change again.
func (r *CertificateReloader) ReloadOnce() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for i := range r.entries {
		entry := &r.entries[i]
		fingerprint, err := fingerprintCertificate(entry.settings)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("limen %q: %w", entry.name, err)
			}
			r.logger.Error("certificate reload rejected", zap.String("limen", entry.name), zap.Error(err))
			continue
		}
		if entry.hasLast && entry.last == fingerprint {
			continue
		}
		// Remember the content before validation so an unchanged bad pair is
		// not repeatedly published or logged by callers that poll frequently.
		entry.last, entry.hasLast = fingerprint, true
		if err := entry.limen.RotateCertificate(entry.settings.CertFile, entry.settings.KeyFile); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("limen %q: %w", entry.name, err)
			}
			r.logger.Error("certificate reload rejected", zap.String("limen", entry.name), zap.Error(err))
			continue
		}
		r.logger.Info("certificate reloaded", zap.String("limen", entry.name))
	}
	return firstErr
}

// Run polls until ctx is cancelled. Certificate errors are non-fatal and do
// not interrupt serving with the last valid identity.
func (r *CertificateReloader) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = r.ReloadOnce()
		}
	}
}

func fingerprintCertificate(settings config.TLSSettings) (certificateFingerprint, error) {
	cert, err := os.ReadFile(settings.CertFile)
	if err != nil {
		return certificateFingerprint{}, fmt.Errorf("read certificate: %w", err)
	}
	key, err := os.ReadFile(settings.KeyFile)
	if err != nil {
		return certificateFingerprint{}, fmt.Errorf("read private key: %w", err)
	}
	return certificateFingerprint{cert: sha256.Sum256(cert), key: sha256.Sum256(key)}, nil
}
