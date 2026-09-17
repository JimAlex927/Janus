package limen

import (
	"crypto/tls"
	"fmt"
	"io"
	"os"

	"janus/internal/config"
)

func loadTLSKeyPair(certFile, keyFile string) (tls.Certificate, error) {
	certPEM, err := readTLSAsset(certFile, "certificate")
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := readTLSAsset(keyFile, "private key")
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func readTLSAsset(path, kind string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", kind, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, config.MaxTLSAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", kind, err)
	}
	if len(data) > config.MaxTLSAssetBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", kind, config.MaxTLSAssetBytes)
	}
	return data, nil
}
