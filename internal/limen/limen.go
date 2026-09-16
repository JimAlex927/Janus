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
	"sync"
	"sync/atomic"

	"janus/internal/config"
	"janus/internal/protocol"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Limen is the inbound protocol boundary for one listener. It deliberately
// accepts an http.Handler so the protocol layer remains independent from
// routing, middleware, and service construction.
type Limen struct {
	address string
	server  *http.Server
	tls     *tls.Config
	cert    *atomic.Pointer[tls.Certificate]
	http3   *http3.Server
	packet  net.PacketConn

	hijackedMu    sync.Mutex
	hijacked      map[net.Conn]struct{}
	hijackedEmpty chan struct{}
	shuttingDown  bool
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
	var h3Server *http3.Server
	if hasProtocol(binding.Protocols, config.ProtocolHTTP3) {
		h3TLS := tlsConfig.Clone()
		h3TLS.MinVersion = tls.VersionTLS13
		h3Server = &http3.Server{
			TLSConfig:      http3.ConfigureTLSConfig(h3TLS),
			Handler:        handler,
			MaxHeaderBytes: int(settings.Server.MaxHeaderBytes),
			IdleTimeout:    settings.Server.IdleTimeout.Duration(),
			// 0-RTT is intentionally disabled: Janus does not have a replay-safe
			// request policy for arbitrary backend operations.
			QUICConfig: &quic.Config{Allow0RTT: false},
		}
		// Advertise H3 only from the TCP response path after the UDP listener
		// has been bound and the QUIC server has started.
		next := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = h3Server.SetQUICHeaders(w.Header())
			next.ServeHTTP(w, r)
		})
	}
	hijackedEmpty := make(chan struct{})
	close(hijackedEmpty)
	l := &Limen{
		address:  binding.Address,
		tls:      tlsConfig,
		cert:     certificates,
		http3:    h3Server,
		hijacked: make(map[net.Conn]struct{}), hijackedEmpty: hijackedEmpty,
		server: &http.Server{
			Addr: binding.Address, Handler: handler,
			Protocols:         protocols,
			ReadHeaderTimeout: settings.Server.ReadHeaderTimeout.Duration(),
			ReadTimeout:       settings.Request.ReadTimeout.Duration(),
			WriteTimeout:      settings.Server.WriteTimeout.Duration(),
			IdleTimeout:       settings.Server.IdleTimeout.Duration(),
			MaxHeaderBytes:    int(settings.Server.MaxHeaderBytes),
		},
	}
	l.server.ConnState = func(conn net.Conn, state http.ConnState) {
		// net/http reports StateHijacked for the frontend connection after
		// ReverseProxy completes a WebSocket upgrade.
		if state == http.StateHijacked || state == http.StateClosed {
			l.trackHijacked(conn, state)
		}
	}
	return l, nil
}

func hasProtocol(protocols []string, wanted string) bool {
	for _, value := range protocols {
		if value == wanted {
			return true
		}
	}
	return false
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
		case config.ProtocolHTTP3:
			// HTTP/3 is served by the QUIC adapter below, not net/http.Server.
		default:
			return nil, fmt.Errorf("unsupported protocol %q", name)
		}
	}
	if hasProtocol(binding.Protocols, config.ProtocolHTTP3) {
		if binding.TLS == nil {
			return nil, fmt.Errorf("HTTP/3 requires TLS")
		}
		if !protocols.HTTP1() && !protocols.HTTP2() {
			return nil, fmt.Errorf("HTTP/3 requires an HTTP/1 or HTTP/2 TCP fallback")
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

// HTTP3Enabled reports whether this binding owns a UDP/QUIC listener in
// addition to its TCP listener.
func (l *Limen) HTTP3Enabled() bool { return l != nil && l.http3 != nil }

// ListenPacket binds the UDP address for HTTP/3. The returned packet
// connection is owned by the Limen after a successful call.
func (l *Limen) ListenPacket() (net.PacketConn, error) {
	if !l.HTTP3Enabled() {
		return nil, fmt.Errorf("limen does not enable HTTP/3")
	}
	if l.packet != nil {
		return nil, fmt.Errorf("limen HTTP/3 packet listener already bound")
	}
	packet, err := net.ListenPacket("udp", l.address)
	if err != nil {
		return nil, err
	}
	l.packet = packet
	// SetQUICHeaders uses Server.Port when a listener was bound to :0. The
	// advertised port must be the actual UDP port, not the independent TCP
	// ephemeral port.
	if addr, ok := packet.LocalAddr().(*net.UDPAddr); ok {
		l.http3.Port = addr.Port
	}
	return packet, nil
}

// Serve runs the HTTP server on a listener. The listener is owned by the
// server after this call and is closed by Shutdown or Close.
func (l *Limen) Serve(listener net.Listener) error {
	listener = &trackingListener{Listener: listener, owner: l}
	if l.tls != nil {
		listener = tls.NewListener(listener, l.tls)
	}
	if l.http3 == nil {
		return l.server.Serve(listener)
	}
	if l.packet == nil {
		return fmt.Errorf("limen HTTP/3 packet listener is not bound")
	}
	tcpDone := make(chan error, 1)
	h3Done := make(chan error, 1)
	go func() { tcpDone <- l.server.Serve(listener) }()
	go func() { h3Done <- l.http3.Serve(l.packet) }()
	select {
	case err := <-tcpDone:
		_ = l.http3.Close()
		return err
	case err := <-h3Done:
		_ = l.server.Close()
		return err
	}
}

// Shutdown stops accepting new connections and waits for active requests until
// ctx expires. It preserves net/http's graceful shutdown semantics.
func (l *Limen) Shutdown(ctx context.Context) error {
	l.hijackedMu.Lock()
	l.shuttingDown = true
	l.hijackedMu.Unlock()
	serverDone := make(chan error, 1)
	go func() { serverDone <- l.server.Shutdown(ctx) }()
	h3Done := make(chan error, 1)
	if l.http3 != nil {
		go func() {
			// HTTP/3 Shutdown closes its QUIC listener and sends GOAWAY to
			// active connections before the raw packet socket is released.
			err := l.http3.Shutdown(ctx)
			if l.packet != nil {
				_ = l.packet.Close()
			}
			h3Done <- err
		}()
	} else {
		h3Done <- nil
	}
	if err := <-serverDone; err != nil {
		l.closeHijacked()
		_ = l.http3Close()
		return err
	}
	if err := <-h3Done; err != nil {
		l.closeHijacked()
		_ = l.http3Close()
		return err
	}
	l.hijackedMu.Lock()
	empty := l.hijackedEmpty
	l.hijackedMu.Unlock()
	select {
	case <-empty:
		return nil
	case <-ctx.Done():
		l.closeHijacked()
		_ = l.http3Close()
		return ctx.Err()
	}
}

// Close immediately closes the server's listeners and active connections.
func (l *Limen) Close() error {
	l.hijackedMu.Lock()
	l.shuttingDown = true
	l.hijackedMu.Unlock()
	err := l.server.Close()
	if l.packet != nil {
		_ = l.packet.Close()
	}
	if h3Err := l.http3Close(); err == nil {
		err = h3Err
	}
	l.closeHijacked()
	return err
}

func (l *Limen) http3Close() error {
	if l.http3 == nil {
		return nil
	}
	return l.http3.Close()
}

func (l *Limen) trackHijacked(conn net.Conn, state http.ConnState) {
	conn = underlyingConnection(conn)
	l.hijackedMu.Lock()
	closeNow := false
	switch state {
	case http.StateHijacked:
		if l.shuttingDown {
			closeNow = true
		} else {
			if len(l.hijacked) == 0 {
				l.hijackedEmpty = make(chan struct{})
			}
			l.hijacked[conn] = struct{}{}
		}
	case http.StateClosed:
		if _, ok := l.hijacked[conn]; ok {
			delete(l.hijacked, conn)
			if len(l.hijacked) == 0 {
				close(l.hijackedEmpty)
			}
		}
	}
	l.hijackedMu.Unlock()
	if closeNow {
		_ = conn.Close()
	}
}

func underlyingConnection(conn net.Conn) net.Conn {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		return tlsConn.NetConn()
	}
	return conn
}

func (l *Limen) untrackHijacked(conn net.Conn) {
	l.hijackedMu.Lock()
	defer l.hijackedMu.Unlock()
	if _, ok := l.hijacked[conn]; !ok {
		return
	}
	delete(l.hijacked, conn)
	if len(l.hijacked) == 0 {
		close(l.hijackedEmpty)
	}
}

func (l *Limen) closeHijacked() {
	l.hijackedMu.Lock()
	connections := make([]net.Conn, 0, len(l.hijacked))
	for conn := range l.hijacked {
		connections = append(connections, conn)
	}
	l.hijackedMu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

type trackingListener struct {
	net.Listener
	owner *Limen
}

func (l *trackingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &trackingConn{Conn: conn, owner: l.owner}, nil
}

type trackingConn struct {
	net.Conn
	owner *Limen
	once  sync.Once
}

func (c *trackingConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.untrackHijacked(c) })
	return err
}
