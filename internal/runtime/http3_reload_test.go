package runtime

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"janus/internal/config"
	"janus/internal/limen"
	"janus/internal/middleware"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"go.uber.org/zap"
)

func TestHTTP3ReloadKeepsOldStreamOnOldGeneration(t *testing.T) {
	certFile, keyFile, roots := writeRuntimeTestCertificate(t)
	configOne := http3ReloadConfig(certFile, keyFile, "first")
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finishRelease := func() { releaseOnce.Do(func() { close(release) }) }
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		name := c.Routes[0].Name
		return &testGeneration{
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "first" && r.URL.Path == "/slow" {
					close(started)
					<-release
				}
				_, _ = fmt.Fprint(w, name)
			}),
			closed: make(chan struct{}),
		}, nil
	}
	r, err := NewWithBuilder(configOne, zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	binding := configOne.LimenBindings()["public"]
	binding.Address = "127.0.0.1:0"
	l, err := limen.NewBinding("public", binding, r.Handler(), configOne.Settings)
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := l.ListenPacket()
	if err != nil {
		_ = tcp.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- l.Serve(tcp) }()
	defer func() {
		finishRelease()
		_ = l.Close()
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("limen stopped: %v", err)
		}
	}()

	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"},
		QUICConfig:      &quic.Config{Allow0RTT: false},
	}
	defer transport.Close()
	client := &http.Client{Transport: transport}
	slowDone := make(chan string, 1)
	slowErr := make(chan error, 1)
	go func() {
		response, requestErr := client.Get("https://" + udp.LocalAddr().String() + "/slow")
		if requestErr != nil {
			slowErr <- requestErr
			return
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			slowErr <- readErr
			return
		}
		slowDone <- string(body)
	}()
	select {
	case <-started:
	case err := <-slowErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("old HTTP/3 stream did not start")
	}

	configTwo := configOne
	configTwo.Routes = []config.Route{{Name: "second", Limen: "public", PathPrefix: "/", Service: "service"}}
	if err := r.Replace(configTwo); err != nil {
		t.Fatal(err)
	}
	fastResponse, err := client.Get("https://" + udp.LocalAddr().String() + "/fast")
	if err != nil {
		t.Fatal(err)
	}
	fastBody, err := io.ReadAll(fastResponse.Body)
	fastResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if fastResponse.ProtoMajor != 3 || string(fastBody) != "second" {
		t.Fatalf("new HTTP/3 stream = proto %s body %q", fastResponse.Proto, fastBody)
	}

	finishRelease()
	select {
	case body := <-slowDone:
		if body != "first" {
			t.Fatalf("old HTTP/3 stream body = %q, want first", body)
		}
	case err := <-slowErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("old HTTP/3 stream did not finish")
	}
}

func http3ReloadConfig(certFile, keyFile, route string) config.Config {
	return config.Config{
		Version: 1,
		Limens: map[string]config.LimenConfig{
			"public": {
				Address:   "127.0.0.1:8443",
				Protocols: []string{config.ProtocolHTTP1, config.ProtocolHTTP3},
				TLS:       &config.TLSSettings{CertFile: certFile, KeyFile: keyFile},
			},
		},
		Services: map[string]config.Service{
			"service": {Upstreams: []string{"http://127.0.0.1:9000"}},
		},
		Routes: []config.Route{{Name: route, Limen: "public", PathPrefix: "/", Service: "service"}},
	}
}

func writeRuntimeTestCertificate(t *testing.T) (certFile, keyFile string, roots *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(cert)
	return certFile, keyFile, roots
}
