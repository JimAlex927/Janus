package limen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"janus/internal/config"
)

type clientTestCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	path string
}

func newClientTestCA(t *testing.T) clientTestCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test client CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return clientTestCA{cert, key, path}
}

func (ca clientTestCA) issue(t *testing.T, usage x509.ExtKeyUsage, from, until time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test device"}, NotBefore: from, NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestMTLSHandshakesAcrossProtocols(t *testing.T) {
	serverCert, serverKey, roots := writeTestCertificate(t)
	ca, other := newClientTestCA(t), newClientTestCA(t)
	now := time.Now()
	valid := ca.issue(t, x509.ExtKeyUsageClientAuth, now.Add(-time.Minute), now.Add(time.Hour))
	wrongCA := other.issue(t, x509.ExtKeyUsageClientAuth, now.Add(-time.Minute), now.Add(time.Hour))
	expired := ca.issue(t, x509.ExtKeyUsageClientAuth, now.Add(-time.Hour), now.Add(-time.Minute))
	future := ca.issue(t, x509.ExtKeyUsageClientAuth, now.Add(time.Minute), now.Add(time.Hour))
	serverOnly := ca.issue(t, x509.ExtKeyUsageServerAuth, now.Add(-time.Minute), now.Add(time.Hour))
	for _, proto := range []string{"h1-tls12", "h1-tls13", "h2", "h3"} {
		t.Run(proto, func(t *testing.T) {
			var calls atomic.Int32
			l, listener := startTestLimen(t, config.LimenConfig{Address: "127.0.0.1:0", Protocols: []string{"http1", "http2", "http3"}, TLS: &config.TLSSettings{CertFile: serverCert, KeyFile: serverKey, ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: ca.path}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "test device" {
					t.Error("missing verified client identity")
				}
				io.WriteString(w, "ok")
			}))
			for _, tc := range []struct {
				name string
				cert *tls.Certificate
				ok   bool
			}{
				{"valid", &valid, true}, {"missing", nil, false}, {"wrong CA", &wrongCA, false}, {"expired", &expired, false}, {"not yet valid", &future, false}, {"server EKU", &serverOnly, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := calls.Load()
					tlsConfig := &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}
					if proto == "h1-tls12" {
						tlsConfig.MaxVersion = tls.VersionTLS12
					} else {
						tlsConfig.MinVersion = tls.VersionTLS13
					}
					// Force transmission even for a wrong issuer/EKU, so rejection
					// is verified on the server, not only in client selection.
					tlsConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
						if tc.cert == nil {
							return &tls.Certificate{}, nil
						}
						return tc.cert, nil
					}
					client := &http.Client{Timeout: 3 * time.Second}
					address := listener.Addr().String()
					wantMajor := 1
					if proto == "h3" {
						address = l.packet.LocalAddr().String()
						tr := &http3.Transport{TLSClientConfig: tlsConfig}
						defer tr.Close()
						client.Transport = tr
						wantMajor = 3
					} else {
						protocols := new(http.Protocols)
						protocols.SetHTTP1(true)
						protocols.SetHTTP2(proto == "h2")
						tr := &http.Transport{TLSClientConfig: tlsConfig, Protocols: protocols}
						defer tr.CloseIdleConnections()
						client.Transport = tr
						if proto == "h2" {
							wantMajor = 2
						}
					}
					response, err := client.Get("https://" + address)
					if !tc.ok {
						if err == nil {
							response.Body.Close()
							t.Fatal("untrusted client received HTTP response")
						}
						if calls.Load() != before {
							t.Fatal("untrusted client reached handler")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					_, err = io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					if response.ProtoMajor != wantMajor {
						t.Fatalf("protocol = %s", response.Proto)
					}
					if proto != "h3" {
						var reused bool
						req, _ := http.NewRequest("GET", "https://"+address, nil)
						req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}))
						res, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						io.Copy(io.Discard, res.Body)
						res.Body.Close()
						if !reused {
							t.Fatal("mTLS prevented keep-alive reuse")
						}
					}
				})
			}
		})
	}
}

func TestMTLSRejectsInvalidTrustAtStartup(t *testing.T) {
	cert, key, _ := writeTestCertificate(t)
	ca := newClientTestCA(t)
	valid, _ := os.ReadFile(ca.path)
	leaf := ca.issue(t, x509.ExtKeyUsageClientAuth, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	for name, data := range map[string][]byte{
		"empty": nil, "garbage": []byte("not PEM"), "trailing garbage": append(append([]byte{}, valid...), []byte("oops")...),
		"malformed before valid": append([]byte("-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----\n"), valid...),
		"nested before valid":    append([]byte("-----BEGIN CERTIFICATE-----\ninvalid\n"), valid...),
		"leaf":                   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}),
		"oversized":              []byte(strings.Repeat("x", config.MaxTLSAssetBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := NewBinding("bad", config.LimenConfig{Protocols: []string{"http1"}, TLS: &config.TLSSettings{CertFile: cert, KeyFile: key, ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: path}}, http.NotFoundHandler(), config.DefaultSettings())
			if err == nil {
				t.Fatal("invalid client trust accepted")
			}
		})
	}
	for _, settings := range []config.TLSSettings{
		{ClientAuth: "unknown"}, {ClientAuth: config.ClientAuthRequireAndVerify},
		{ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: filepath.Join(t.TempDir(), "missing")}, {ClientCAFile: ca.path},
	} {
		settings.CertFile, settings.KeyFile = cert, key
		if _, err := NewBinding("bad", config.LimenConfig{Protocols: []string{"http1"}, TLS: &settings}, http.NotFoundHandler(), config.DefaultSettings()); err == nil {
			t.Fatal("invalid client auth settings accepted")
		}
	}
	if _, err := loadClientCAs(ca.path); err != nil {
		t.Fatal(err)
	}
	// A trust bundle may contain several CAs for a controlled migration.
	other := newClientTestCA(t)
	second, _ := os.ReadFile(other.path)
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(bundle, append(valid, second...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadClientCAs(bundle); err != nil {
		t.Fatal(err)
	}
}

func TestMTLSClientTrustIsImmutableUntilRestart(t *testing.T) {
	cert, key, _ := writeTestCertificate(t)
	ca := newClientTestCA(t)
	binding := config.LimenConfig{Protocols: []string{"http1", "http3"}, TLS: &config.TLSSettings{CertFile: cert, KeyFile: key, ClientAuth: config.ClientAuthRequireAndVerify, ClientCAFile: ca.path}}
	l, err := NewBinding("public", binding, http.NotFoundHandler(), config.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ca.path, []byte("corrupt replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.RotateCertificate(cert, key); err != nil {
		t.Fatal(err)
	}
	if l.tls.ClientAuth != tls.RequireAndVerifyClientCert || !l.tls.ClientCAs.Equal(l.http3.TLSConfig.ClientCAs) {
		t.Fatal("server rotation changed client policy")
	}
	client := ca.issue(t, x509.ExtKeyUsageClientAuth, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	leaf, err := x509.ParseCertificate(client.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: l.tls.ClientCAs, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("running CA snapshot changed: %v", err)
	}
	if _, err := NewBinding("public", binding, http.NotFoundHandler(), config.DefaultSettings()); err == nil {
		t.Fatal("new binding ignored corrupt CA file")
	}
}
