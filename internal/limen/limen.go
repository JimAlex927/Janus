// Package limen owns Janus's inbound protocol boundary and HTTP server
// lifecycle. It currently serves plaintext HTTP/1.x and TLS HTTP/1.x/HTTP/2;
// HTTP/3 is added later without changing the handler it serves.
package limen

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"

	"janus/internal/config"
	"janus/internal/protocol"
)

// Limen is the inbound protocol boundary for one listener. It deliberately
// accepts an http.Handler so the protocol layer remains independent from
// routing, middleware, and service construction.
type Limen struct {
	address string
	server  *http.Server
	tls     *tls.Config
	cert    *atomic.Pointer[tls.Certificate]
}

// New creates a Limen using the server budgets from settings. The caller must
// validate the full configuration before publishing the Limen.
func New(address string, handler http.Handler, settings config.Settings) *Limen {
	l, err := NewBinding("default", config.LimenConfig{
		Address:   address,
		Protocols: []string{config.ProtocolHTTP1},
	}, handler, settings)
	if err != nil {
		// The legacy constructor has a fixed, valid plaintext HTTP/1 profile.
		panic(err)
	}
	return l
}

// NewBinding creates a named inbound Limen with explicit protocol and TLS
// settings. Certificate files are loaded before the listener is opened.
func NewBinding(name string, binding config.LimenConfig, handler http.Handler, settings config.Settings) (*Limen, error) {
	settings = settings.WithDefaults()
	protocols, err := serverProtocols(binding)
	if err != nil {
		return nil, err
	}
	tlsConfig, certificates, err := serverTLSConfig(binding, protocols)
	if err != nil {
		return nil, err
	}
	if name != "" {
		next := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, protocol.WithLimenID(r, name))
		})
	}
	return &Limen{
		address: binding.Address,
		tls:     tlsConfig,
		cert:    certificates,
		server: &http.Server{
			Addr: binding.Address, Handler: handler,
			Protocols:         protocols,
			ReadHeaderTimeout: settings.Server.ReadHeaderTimeout.Duration(),
			ReadTimeout:       settings.Request.ReadTimeout.Duration(),
			WriteTimeout:      settings.Server.WriteTimeout.Duration(),
			IdleTimeout:       settings.Server.IdleTimeout.Duration(),
			MaxHeaderBytes:    int(settings.Server.MaxHeaderBytes),
		},
	}, nil
}

func serverProtocols(binding config.LimenConfig) (*http.Protocols, error) {
	if len(binding.Protocols) == 0 {
		return nil, fmt.Errorf("limen must enable at least one protocol")
	}
	protocols := new(http.Protocols)
	seen := map[string]bool{}
	for _, name := range binding.Protocols {
		if seen[name] {
			return nil, fmt.Errorf("limen enables protocol %q more than once", name)
		}
		seen[name] = true
		switch name {
		case config.ProtocolHTTP1:
			protocols.SetHTTP1(true)
		case config.ProtocolHTTP2:
			if binding.TLS == nil {
				return nil, fmt.Errorf("HTTP/2 requires TLS")
			}
			protocols.SetHTTP2(true)
		default:
			return nil, fmt.Errorf("unsupported protocol %q", name)
		}
	}
	return protocols, nil
}

func serverTLSConfig(binding config.LimenConfig, protocols *http.Protocols) (*tls.Config, *atomic.Pointer[tls.Certificate], error) {
	if binding.TLS == nil {
		return nil, nil, nil
	}
	cert, err := tls.LoadX509KeyPair(binding.TLS.CertFile, binding.TLS.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load limen TLS certificate: %w", err)
	}
	minVersion := uint16(tls.VersionTLS12)
	if binding.TLS.MinVersion == "1.3" || binding.TLS.MinVersion == "TLS1.3" {
		minVersion = tls.VersionTLS13
	}
	nextProtos := make([]string, 0, 2)
	if protocols.HTTP2() {
		nextProtos = append(nextProtos, "h2")
	}
	if protocols.HTTP1() {
		nextProtos = append(nextProtos, "http/1.1")
	}
	certificates := new(atomic.Pointer[tls.Certificate])
	certificates.Store(&cert)
	return &tls.Config{
		MinVersion: minVersion,
		NextProtos: nextProtos,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			current := certificates.Load()
			if current == nil {
				return nil, fmt.Errorf("limen TLS certificate is unavailable")
			}
			return current, nil
		},
	}, certificates, nil
}

// RotateCertificate validates and atomically publishes a new certificate/key
// pair. Existing TLS connections keep their negotiated identity; only future
// handshakes observe the replacement.
func (l *Limen) RotateCertificate(certFile, keyFile string) error {
	if l.cert == nil {
		return fmt.Errorf("limen does not use TLS")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load rotated limen TLS certificate: %w", err)
	}
	l.cert.Store(&cert)
	return nil
}

// Listen binds the configured TCP address. The caller should pass the returned
// listener to Serve so binding errors are reported before the serving goroutine
// starts.
func (l *Limen) Listen() (net.Listener, error) {
	return net.Listen("tcp", l.address)
}

// Serve runs the HTTP server on a listener. The listener is owned by the
// server after this call and is closed by Shutdown or Close.
func (l *Limen) Serve(listener net.Listener) error {
	if l.tls != nil {
		listener = tls.NewListener(listener, l.tls)
	}
	return l.server.Serve(listener)
}

// Shutdown stops accepting new connections and waits for active requests until
// ctx expires. It preserves net/http's graceful shutdown semantics.
func (l *Limen) Shutdown(ctx context.Context) error {
	return l.server.Shutdown(ctx)
}

// Close immediately closes the server's listeners and active connections.
func (l *Limen) Close() error {
	return l.server.Close()
}
