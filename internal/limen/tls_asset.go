package limen

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"time"

	"janus/internal/config"
)

// loadClientCAs accepts only a nonempty bundle of currently valid CA
// certificates. Reject partially corrupt bundles instead of trusting a subset.
// The immutable pool is shared by TCP TLS and HTTP/3 until process restart.
func loadClientCAs(path string) (*x509.CertPool, error) {
	data, err := readTLSAsset(path, "client CA bundle")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	count := 0
	now := time.Now()
	for len(bytes.TrimSpace(data)) > 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, fmt.Errorf("client CA bundle must contain only PEM CERTIFICATE blocks")
		}
		// Decode one block at a time so pem.Decode cannot skip corruption
		// and silently accept a later certificate.
		end := bytes.Index(data, []byte("-----END CERTIFICATE-----"))
		if end < 0 {
			return nil, fmt.Errorf("invalid client CA PEM bundle")
		}
		end += len("-----END CERTIFICATE-----")
		if bytes.Count(data[:end], []byte("-----BEGIN")) != 1 {
			return nil, fmt.Errorf("nested client CA PEM block")
		}
		block, rest := pem.Decode(data[:end])
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return nil, fmt.Errorf("invalid client CA PEM block")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse client CA certificate: %w", err)
		}
		if !cert.BasicConstraintsValid || !cert.IsCA || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
			return nil, fmt.Errorf("client CA bundle contains a non-signing CA certificate")
		}
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, fmt.Errorf("client CA certificate is not currently valid")
		}
		pool.AddCert(cert)
		count++
		data = data[end:]
	}
	if count == 0 {
		return nil, fmt.Errorf("client CA bundle is empty")
	}
	return pool, nil
}

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
