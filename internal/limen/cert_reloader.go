package limen

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"sort"
	"sync"
	"time"

	"janus/internal/config"

	"go.uber.org/zap"
)

type certificateEntry struct {
	name        string
	limen       *Limen
	settings    config.TLSSettings
	last        certificateFingerprint
	hasLast     bool
	rejected    certificateFingerprint
	hasRejected bool
	lastError   string
	hasError    bool
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

// NewCertificateReloader starts without an assumed applied fingerprint.
// NewBinding's active certificate may differ from files changed during startup.
// The first poll validates and publishes its own complete snapshot.
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
		entries = append(entries, certificateEntry{
			name: name, limen: server, settings: settings,
		})
	}
	return &CertificateReloader{entries: entries, interval: interval, logger: logger}, nil
}

// ReloadOnce checks every configured certificate pair once. A malformed or
// incomplete pair leaves the current certificate active and is retried on a
// later poll; repeated errors for the same fingerprint are log-deduplicated.
func (r *CertificateReloader) ReloadOnce() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for i := range r.entries {
		entry := &r.entries[i]
		certPEM, keyPEM, fingerprint, err := readCertificateSnapshot(entry.settings)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("limen %q: %w", entry.name, err)
			}
			r.logRejected(entry, err)
			continue
		}
		if entry.hasLast && entry.last == fingerprint {
			entry.hasError = false
			continue
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err == nil {
			err = entry.limen.publishCertificate(pair)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("limen %q: %w", entry.name, err)
			}
			if !entry.hasRejected || entry.rejected != fingerprint || entry.hasError && entry.lastError != err.Error() {
				r.logger.Error("certificate reload rejected", zap.String("limen", entry.name), zap.Error(err))
			}
			entry.rejected, entry.hasRejected = fingerprint, true
			entry.lastError, entry.hasError = err.Error(), true
			continue
		}
		entry.last, entry.hasLast = fingerprint, true
		entry.hasRejected = false
		entry.hasError = false
		r.logger.Info("certificate reloaded", zap.String("limen", entry.name))
	}
	return firstErr
}

func (r *CertificateReloader) logRejected(entry *certificateEntry, err error) {
	message := err.Error()
	if !entry.hasError || entry.lastError != message {
		r.logger.Error("certificate reload rejected", zap.String("limen", entry.name), zap.Error(err))
	}
	entry.lastError, entry.hasError = message, true
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

// Hash and parse the same bounded bytes so a second read cannot publish a
// different identity than the fingerprint recorded as applied.
func readCertificateSnapshot(settings config.TLSSettings) ([]byte, []byte, certificateFingerprint, error) {
	cert, err := readTLSAsset(settings.CertFile, "certificate")
	if err != nil {
		return nil, nil, certificateFingerprint{}, err
	}
	key, err := readTLSAsset(settings.KeyFile, "private key")
	if err != nil {
		return nil, nil, certificateFingerprint{}, err
	}
	return cert, key, certificateFingerprint{cert: sha256.Sum256(cert), key: sha256.Sum256(key)}, nil
}
