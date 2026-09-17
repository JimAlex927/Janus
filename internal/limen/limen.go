// Package limen owns Janus's inbound protocol boundary and HTTP server
// lifecycle. It serves plaintext HTTP/1.x, TLS HTTP/1.x/HTTP/2, and opt-in TLS
// HTTP/3 without changing the handler it serves.
package limen

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"janus/internal/config"
	"janus/internal/protocol"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Limen is the inbound protocol boundary for one listener. It deliberately
// accepts an http.Handler so the protocol layer remains independent from
// routing, middleware, and service construction.
type Limen struct {
	address  string
	server   *http.Server
	tls      *tls.Config
	cert     *atomic.Pointer[tls.Certificate]
	http3    *http3.Server
	packetMu sync.Mutex
	packet   net.PacketConn
	h3Close  sync.Once

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
	//协议 必须要有一个协议 。 http1 不检验TLS。http2必须要有TLS。http走的是QUIC
	protocols, err := serverProtocols(binding)
	if err != nil {
		return nil, err
	}
	//加载证书 有热更新机制 一个binding一个证书
	tlsConfig, certificates, err := serverTLSConfig(binding, protocols)
	if err != nil {
		return nil, err
	}
	if name != "" {
		next := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//这里是把请求包装一下，修改请求的context，并在context里面放入limen的Id 估计是为了后面metric用
			next.ServeHTTP(w, protocol.WithLimenID(r, name))
		})
	}
	//这里如果协议里面有http3
	var h3Server *http3.Server
	if hasProtocol(binding.Protocols, config.ProtocolHTTP3) {
		h3Settings := config.HTTP3Settings{}.WithDefaults()
		if binding.HTTP3 != nil {
			h3Settings = binding.HTTP3.WithDefaults()
		}
		h3TLS := tlsConfig.Clone()
		h3TLS.MinVersion = tls.VersionTLS13
		h3Server = &http3.Server{
			TLSConfig:      http3.ConfigureTLSConfig(h3TLS),
			Handler:        handler,
			MaxHeaderBytes: int(settings.Server.MaxHeaderBytes),
			IdleTimeout:    settings.Server.IdleTimeout.Duration(),
			// 0-RTT is intentionally disabled: Janus does not have a replay-safe
			// request policy for arbitrary backend operations.
			QUICConfig: &quic.Config{
				Allow0RTT:          false,
				MaxIncomingStreams: h3Settings.MaxConcurrentStreams,
			},
		}
		// Advertise H3 only from the TCP response path after the UDP listener
		// has been bound and the QUIC server has started.
		next := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Advertise the UDP port on the TCP response so capable clients can
			// discover the HTTP/3 endpoint through Alt-Svc.
			_ = h3Server.SetQUICHeaders(w.Header())
			next.ServeHTTP(w, r)
		})
	}
	hijackedEmpty := make(chan struct{})
	close(hijackedEmpty)
	l := &Limen{
		//地址
		address: binding.Address,
		//tls配置
		tls:  tlsConfig,
		cert: certificates,
		//如果协议里面有h3
		http3:    h3Server,
		hijacked: make(map[net.Conn]struct{}), hijackedEmpty: hijackedEmpty,
		// The TCP server handles HTTP/1 and HTTP/2. TLS is applied to its listener
		// in Serve; HTTP/3 uses the separate QUIC server above.
		server: &http.Server{
			Addr: binding.Address, Handler: handler,
			// net/http serves HTTP/1 and HTTP/2 on the TCP listener. TLS, when
			// configured, wraps that listener before it is passed to net/http;
			// it is not a second server forwarding requests to this one.
			Protocols:         protocols,
			ReadHeaderTimeout: settings.Server.ReadHeaderTimeout.Duration(),
			ReadTimeout:       settings.Request.ReadTimeout.Duration(),
			WriteTimeout:      settings.Server.WriteTimeout.Duration(),
			IdleTimeout:       settings.Server.IdleTimeout.Duration(),
			MaxHeaderBytes:    int(settings.Server.MaxHeaderBytes),
		},
	}
	//下面这个是属于TCP的回调函数 主要用于跟踪 HTTP/1.1 WebSocket 升级后的连接。
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
			//必须要有一个协议
			return nil, fmt.Errorf("limen enables protocol %q more than once", name)
		}
		seen[name] = true
		//这里遍历每个协议，对于http2 必须要求有TLS配置。
		//对于http3 放在后面继续判断 http3 不属于net/http包 走的是QUIC

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
	//如果需要支持http3 必须要TLS 如果没有可以单独跳过 必需要有一个合法的http请求 否则返回nil 报错
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
	// One binding has one TLS identity shared by its enabled TCP protocols and
	// the HTTP/3 adapter. ALPN selects HTTP/2 or HTTP/1.1 on the TLS connection.
	cert, err := loadTLSKeyPair(binding.TLS.CertFile, binding.TLS.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load limen TLS certificate: %w", err)
	}
	if err := validateServerCertificate(cert); err != nil {
		return nil, nil, fmt.Errorf("validate limen TLS certificate: %w", err)
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
	//这里用了一个原子指针 是为了更新配置时候 旧请求用旧证书 新请求用新证书
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
	cert, err := loadTLSKeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load rotated limen TLS certificate: %w", err)
	}
	return l.publishCertificate(cert)
}

func (l *Limen) publishCertificate(cert tls.Certificate) error {
	if l.cert == nil {
		return fmt.Errorf("limen does not use TLS")
	}
	if err := validateServerCertificate(cert); err != nil {
		return fmt.Errorf("validate rotated limen TLS certificate: %w", err)
	}
	l.cert.Store(&cert)
	return nil
}

func validateServerCertificate(cert tls.Certificate) error {
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("certificate chain is empty")
	}
	now := time.Now()
	var leaf *x509.Certificate
	for index, der := range cert.Certificate {
		parsed, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("parse certificate %d: %w", index, err)
		}
		if now.Before(parsed.NotBefore) {
			return fmt.Errorf("certificate %d is not valid before %s", index, parsed.NotBefore.Format(time.RFC3339))
		}
		if !now.Before(parsed.NotAfter) {
			return fmt.Errorf("certificate %d expired at %s", index, parsed.NotAfter.Format(time.RFC3339))
		}
		if index == 0 {
			leaf = parsed
		}
	}
	if leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return fmt.Errorf("leaf certificate does not allow digital signatures")
	}
	if len(leaf.ExtKeyUsage) != 0 {
		serverAuth := false
		for _, usage := range leaf.ExtKeyUsage {
			if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
				serverAuth = true
				break
			}
		}
		if !serverAuth {
			return fmt.Errorf("leaf certificate does not allow server authentication")
		}
	}
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
	l.packetMu.Lock()
	defer l.packetMu.Unlock()
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
	l.packetMu.Lock()
	packet := l.packet
	l.packetMu.Unlock()
	if packet == nil {
		return fmt.Errorf("limen HTTP/3 packet listener is not bound")
	}
	tcpDone := make(chan error, 1)
	h3Done := make(chan error, 1)
	go func() { tcpDone <- l.server.Serve(listener) }()
	go func() { h3Done <- l.http3.Serve(packet) }()
	select {
	case err := <-tcpDone:
		if l.isGracefulStop(err) {
			return err
		}
		l.closePacket()
		l.startHTTP3Close()
		return err
	case err := <-h3Done:
		if l.isGracefulStop(err) {
			return err
		}
		_ = l.server.Close()
		l.closePacket()
		l.startHTTP3Close()
		return err
	}
}

// A listener stops accepting before Shutdown has finished draining handlers.
// Only failures outside that normal lifecycle may force-close its sibling.
func (l *Limen) isGracefulStop(err error) bool {
	l.hijackedMu.Lock()
	defer l.hijackedMu.Unlock()
	return l.shuttingDown && errors.Is(err, http.ErrServerClosed)
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
			err := l.shutdownHTTP3(ctx)
			l.closePacket()
			h3Done <- err
		}()
	} else {
		h3Done <- nil
	}
	if err := <-serverDone; err != nil {
		_ = l.Close()
		return err
	}
	if err := <-h3Done; err != nil {
		_ = l.Close()
		return err
	}
	l.hijackedMu.Lock()
	empty := l.hijackedEmpty
	l.hijackedMu.Unlock()
	select {
	case <-empty:
		return nil
	case <-ctx.Done():
		_ = l.Close()
		return ctx.Err()
	}
}

// Close immediately closes the server's listeners and active connections.
func (l *Limen) Close() error {
	l.hijackedMu.Lock()
	l.shuttingDown = true
	l.hijackedMu.Unlock()
	err := l.server.Close()
	l.closePacket()
	l.startHTTP3Close()
	l.closeHijacked()
	return err
}

// shutdownHTTP3 gives the library a chance to send GOAWAY, but does not allow
// a non-cooperative handler to extend Limen's caller-owned drain budget. The
// force-close path starts the library's connection termination asynchronously;
// request handlers still need to observe their own request context.
func (l *Limen) shutdownHTTP3(ctx context.Context) error {
	if l.http3 == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- l.http3.Shutdown(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		l.startHTTP3Close()
		return ctx.Err()
	}
}

func (l *Limen) startHTTP3Close() {
	if l.http3 == nil {
		return
	}
	l.h3Close.Do(func() {
		go func() { _ = l.http3.Close() }()
	})
}

// closePacket releases the application-owned UDP socket passed to the HTTP/3
// server. It is safe to call concurrently and repeatedly from lifecycle paths.
func (l *Limen) closePacket() {
	l.packetMu.Lock()
	packet := l.packet
	l.packet = nil
	l.packetMu.Unlock()
	if packet != nil {
		_ = packet.Close()
	}
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
