package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"janus/internal/config"
)

type tlsInitResult struct {
	Limen, CACertFile, CAKeyFile, ServerCertFile, ServerKeyFile, CAFingerprint string
}

// initializeTLS is an explicit, one-time operation. Normal startup never
// creates or replaces TLS identities when a configured file is missing.
func initializeTLS(configPath, limenName, hostList string) (tlsInitResult, error) {
	c, _, err := config.LoadFileSnapshot(configPath)
	if err != nil {
		return tlsInitResult{}, err
	}
	bindings := c.LimenBindings()
	if limenName == "" {
		for name, binding := range bindings {
			if binding.TLS != nil {
				if limenName != "" {
					return tlsInitResult{}, fmt.Errorf("multiple TLS limens configured; select one with -tls-limen")
				}
				limenName = name
			}
		}
	}
	binding, ok := bindings[limenName]
	if !ok || binding.TLS == nil {
		return tlsInitResult{}, fmt.Errorf("TLS limen %q is not configured", limenName)
	}
	hosts, err := certificateHosts(binding.Address, hostList)
	if err != nil {
		return tlsInitResult{}, err
	}
	result := tlsInitResult{
		Limen:          limenName,
		CACertFile:     filepath.Join(filepath.Dir(binding.TLS.CertFile), "rootCA.crt"),
		CAKeyFile:      filepath.Join(filepath.Dir(binding.TLS.CertFile), "rootCA.key"),
		ServerCertFile: binding.TLS.CertFile,
		ServerKeyFile:  binding.TLS.KeyFile,
	}
	paths := []string{result.CACertFile, result.CAKeyFile, result.ServerCertFile, result.ServerKeyFile}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if seen[path] {
			return tlsInitResult{}, fmt.Errorf("TLS output paths must be distinct: %s", path)
		}
		seen[path] = true
		if _, err := os.Lstat(path); err == nil {
			return tlsInitResult{}, fmt.Errorf("refusing to overwrite existing TLS file %s", path)
		} else if !os.IsNotExist(err) {
			return tlsInitResult{}, fmt.Errorf("inspect TLS file %s: %w", path, err)
		}
	}
	caCert, caKey, serverCert, serverKey, fingerprint, err := createTLSCertificates(hosts)
	if err != nil {
		return tlsInitResult{}, err
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{result.CAKeyFile, caKey, 0o600},
		{result.ServerKeyFile, serverKey, 0o600},
		{result.CACertFile, caCert, 0o644},
		{result.ServerCertFile, serverCert, 0o644},
	}
	var created []string
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o700); err != nil {
			removeCreatedTLSFiles(created)
			return tlsInitResult{}, fmt.Errorf("create TLS directory: %w", err)
		}
		f, err := os.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.mode)
		if err != nil {
			removeCreatedTLSFiles(created)
			return tlsInitResult{}, fmt.Errorf("create TLS file %s: %w", file.path, err)
		}
		created = append(created, file.path)
		_, writeErr := f.Write(file.data)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			removeCreatedTLSFiles(created)
			return tlsInitResult{}, fmt.Errorf("write TLS file %s: %w", file.path, firstTLSError(writeErr, closeErr))
		}
	}
	result.CAFingerprint = fingerprint
	return result, nil
}

func removeCreatedTLSFiles(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

func firstTLSError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func certificateHosts(address, list string) ([]string, error) {
	if strings.TrimSpace(list) == "" {
		host, _, err := net.SplitHostPort(address)
		if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
			return nil, fmt.Errorf("cannot infer a certificate name from %q; specify -tls-hosts", address)
		}
		list = host
		if host == "127.0.0.1" || host == "::1" {
			list += ",localhost"
		}
	}
	var hosts []string
	seen := map[string]bool{}
	for _, item := range strings.Split(list, ",") {
		host := strings.TrimSpace(item)
		if host == "" {
			return nil, fmt.Errorf("invalid TLS host %q", host)
		}
		if net.ParseIP(host) == nil {
			if len(host) > 253 || strings.ContainsAny(host, "/\\:\x00 \t\r\n") {
				return nil, fmt.Errorf("invalid TLS host %q", host)
			}
			for _, label := range strings.Split(host, ".") {
				if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
					return nil, fmt.Errorf("invalid TLS host %q", host)
				}
				for _, ch := range label {
					if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
						return nil, fmt.Errorf("invalid TLS host %q", host)
					}
				}
			}
		}
		if !seen[host] {
			hosts = append(hosts, host)
			seen[host] = true
		}
	}
	sort.Strings(hosts)
	return hosts, nil
}

func createTLSCertificates(hosts []string) (caCertPEM, caKeyPEM, serverCertPEM, serverKeyPEM []byte, fingerprint string, err error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	now := time.Now()
	caSerial, err := randomTLSSerial()
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial, Subject: pkix.Name{CommonName: "Janus local CA"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	serverSerial, err := randomTLSSerial()
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial, Subject: pkix.Name{CommonName: hosts[0]},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			serverTemplate.IPAddresses = append(serverTemplate.IPAddresses, ip)
		} else {
			serverTemplate.DNSNames = append(serverTemplate.DNSNames, host)
		}
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	serverCert, err := x509.ParseCertificate(serverDER)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	for _, host := range hosts {
		if _, err := serverCert.Verify(x509.VerifyOptions{Roots: roots, DNSName: host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return nil, nil, nil, nil, "", fmt.Errorf("verify generated certificate for %s: %w", host, err)
		}
	}
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	fingerprint = fmt.Sprintf("%X", sha256.Sum256(caDER))
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: serverKeyDER}), fingerprint, nil
}

func randomTLSSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}
