// Package certutil provides offline certificate provisioning. It has no
// dependency on Janus configuration, listeners or application runtime.
package certutil

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"software.sslmate.com/src/go-pkcs12"
)

type Result struct {
	CertFile, KeyFile, P12File, PasswordFile string
	Fingerprint                              string
	NotAfter                                 time.Time
}

type outputFile struct {
	path string
	data []byte
}

// CreateCA creates only a self-signed root CA. No leaf identities are created.
func CreateCA(out, name string, days int) (Result, error) {
	if err := validateOptions(out, name, days); err != nil {
		return Result{}, err
	}
	template, key, err := newTemplate(name, days)
	if err != nil {
		return Result{}, err
	}
	template.IsCA, template.MaxPathLenZero = true, true
	template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return Result{}, err
	}
	return saveIdentity(out, "ca", der, key, nil)
}

// IssueServer requires explicit SANs: it never infers a listen address.
func IssueServer(out, caFile, caKeyFile, hostList string, days int) (Result, error) {
	hosts, err := parseHosts(hostList)
	if err != nil {
		return Result{}, err
	}
	return issue(out, caFile, caKeyFile, hosts[0], hosts, days, x509.ExtKeyUsageServerAuth)
}

// IssueClient records name as the subject CN for operator identification.
// It does not grant application permissions or bind a physical device.
func IssueClient(out, caFile, caKeyFile, name string, days int) (Result, error) {
	return issue(out, caFile, caKeyFile, name, nil, days, x509.ExtKeyUsageClientAuth)
}

func issue(out, caFile, caKeyFile, name string, hosts []string, days int, usage x509.ExtKeyUsage) (Result, error) {
	if err := validateOptions(out, name, days); err != nil {
		return Result{}, err
	}
	ca, caKey, err := loadCA(caFile, caKeyFile)
	if err != nil {
		return Result{}, err
	}
	template, key, err := newTemplate(name, days)
	if err != nil {
		return Result{}, err
	}
	template.KeyUsage = x509.KeyUsageDigitalSignature
	template.ExtKeyUsage = []x509.ExtKeyUsage{usage}
	if ca.NotAfter.Before(template.NotAfter) {
		template.NotAfter = ca.NotAfter
	}
	if ca.NotBefore.After(template.NotBefore) {
		template.NotBefore = ca.NotBefore
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, key.Public(), caKey)
	if err != nil {
		return Result{}, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Result{}, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	options := x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{usage}}
	if _, err := cert.Verify(options); err != nil {
		return Result{}, fmt.Errorf("verify issued certificate: %w", err)
	}
	for _, host := range hosts {
		if err := cert.VerifyHostname(host); err != nil {
			return Result{}, err
		}
	}
	if usage == x509.ExtKeyUsageClientAuth {
		return saveIdentity(out, "client", der, key, ca)
	}
	return saveIdentity(out, "server", der, key, nil)
}

func validateOptions(out, name string, days int) error {
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("--out is required")
	}
	if strings.TrimSpace(name) == "" || len(name) > 253 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Errorf("name must be nonempty, at most 253 bytes and contain no control characters")
	}
	if days < 1 || days > 36500 {
		return fmt.Errorf("--days must be between 1 and 36500")
	}
	return nil
}

func newTemplate(name string, days int) (*x509.Certificate, crypto.Signer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	serial.Add(serial, big.NewInt(1))
	now := time.Now()
	return &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(time.Duration(days) * 24 * time.Hour), BasicConstraintsValid: true}, key, nil
}

func loadCA(certFile, keyFile string) (*x509.Certificate, crypto.Signer, error) {
	if certFile == "" || keyFile == "" {
		return nil, nil, fmt.Errorf("--ca and --ca-key are required; create the CA first with 'janus-cert ca'")
	}
	read := func(path string) ([]byte, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("CA asset must be a regular file")
		}
		const limit = 1 << 20
		data, err := io.ReadAll(io.LimitReader(f, limit+1))
		if err != nil {
			return nil, err
		}
		if len(data) > limit {
			return nil, fmt.Errorf("CA asset exceeds 1 MiB")
		}
		return data, nil
	}
	certPEM, err := read(certFile)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := read(keyFile)
	if err != nil {
		return nil, nil, err
	}
	if err := singlePEM(certPEM, true); err != nil {
		return nil, nil, err
	}
	if err := singlePEM(keyPEM, false); err != nil {
		return nil, nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, err
	}
	if len(pair.Certificate) != 1 {
		return nil, nil, fmt.Errorf("signing CA must contain one root certificate, not a bundle")
	}
	ca, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	key, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, fmt.Errorf("CA private key cannot sign")
	}
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 || ca.CheckSignatureFrom(ca) != nil {
		return nil, nil, fmt.Errorf("CA must be a self-signed root with certificate-signing usage")
	}
	now := time.Now()
	if now.Before(ca.NotBefore) || !now.Before(ca.NotAfter) {
		return nil, nil, fmt.Errorf("CA is not currently valid")
	}
	return ca, key, nil
}

// Don't let PEM decoders skip damaged blocks or extra material silently.
func singlePEM(data []byte, certificate bool) error {
	data = bytes.TrimSpace(data)
	block, rest := pem.Decode(data)
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.HasPrefix(data, []byte("-----BEGIN ")) || bytes.Count(data, []byte("-----BEGIN ")) != 1 {
		return fmt.Errorf("CA assets must contain exactly one valid PEM block each")
	}
	if certificate && block.Type != "CERTIFICATE" {
		return fmt.Errorf("CA file must contain a PEM CERTIFICATE")
	}
	if !certificate && block.Type != "PRIVATE KEY" && block.Type != "EC PRIVATE KEY" && block.Type != "RSA PRIVATE KEY" {
		return fmt.Errorf("CA key must be an unencrypted PEM private key")
	}
	return nil
}

func parseHosts(list string) ([]string, error) {
	if strings.TrimSpace(list) == "" {
		return nil, fmt.Errorf("--hosts is required (DNS names/IPs used in the client URL, without scheme, port or path)")
	}
	var hosts []string
	seen := map[string]bool{}
	for _, item := range strings.Split(list, ",") {
		host := strings.TrimSpace(item)
		if ip := net.ParseIP(host); ip != nil {
			if ip.IsUnspecified() {
				return nil, fmt.Errorf("use an actual access IP, not wildcard bind address %q", host)
			}
			host = ip.String()
		} else {
			host = strings.ToLower(host)
			if len(host) == 0 || len(host) > 253 {
				return nil, fmt.Errorf("invalid host %q", host)
			}
			for _, label := range strings.Split(host, ".") {
				if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
					return nil, fmt.Errorf("invalid host %q", host)
				}
				for _, c := range label {
					if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
						return nil, fmt.Errorf("invalid host %q; use DNS names or IPs, not URLs or host:port", host)
					}
				}
			}
		}
		if !seen[host] {
			hosts = append(hosts, host)
			seen[host] = true
		}
	}
	return hosts, nil
}

func saveIdentity(out, kind string, der []byte, key crypto.Signer, ca *x509.Certificate) (Result, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Result{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Result{}, err
	}
	result := Result{CertFile: filepath.Join(out, kind+".crt"), KeyFile: filepath.Join(out, kind+".key"), Fingerprint: fmt.Sprintf("%X", sha256.Sum256(der)), NotAfter: cert.NotAfter}
	files := []outputFile{{result.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, {result.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}}
	if kind == "client" {
		var secret [24]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return Result{}, err
		}
		password := hex.EncodeToString(secret[:])
		p12, err := pkcs12.Modern2023.Encode(key, cert, []*x509.Certificate{ca}, password)
		if err != nil {
			return Result{}, err
		}
		result.P12File = filepath.Join(out, "client.p12")
		result.PasswordFile = filepath.Join(out, "client-password.txt")
		files = append(files, outputFile{result.P12File, p12}, outputFile{result.PasswordFile, []byte(password + "\n")})
	}
	if err := writeNewFiles(files); err != nil {
		return Result{}, err
	}
	return result, nil
}

func writeNewFiles(files []outputFile) error {
	for _, file := range files {
		if _, err := os.Lstat(file.path); err == nil {
			return fmt.Errorf("refusing to overwrite %s; choose another --out directory", file.path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	var created []string
	rollback := func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
	}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0700); err != nil {
			rollback()
			return err
		}
		f, err := os.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			rollback()
			return err
		}
		created = append(created, file.path)
		_, writeErr := f.Write(file.data)
		closeErr := f.Close()
		if writeErr != nil {
			rollback()
			return writeErr
		}
		if closeErr != nil {
			rollback()
			return closeErr
		}
	}
	return nil
}
