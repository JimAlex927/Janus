// Package limen owns Janus's inbound protocol boundary and HTTP server
// lifecycle. The current implementation serves plain HTTP/1.x; TLS/HTTP/2 and
// HTTP/3 adapters are added without changing the handler it serves.
package limen

import (
	"context"
	"net"
	"net/http"

	"janus/internal/config"
)

// Limen is the inbound protocol boundary for one listener. It deliberately
// accepts an http.Handler so the protocol layer remains independent from
// routing, middleware, and service construction.
type Limen struct {
	address string
	server  *http.Server
}

// New creates a Limen using the server budgets from settings. The caller must
// validate the full configuration before publishing the Limen.
func New(address string, handler http.Handler, settings config.Settings) *Limen {
	settings = settings.WithDefaults()
	return &Limen{
		address: address,
		server: &http.Server{
			Addr: address, Handler: handler,
			ReadHeaderTimeout: settings.Server.ReadHeaderTimeout.Duration(),
			ReadTimeout:       settings.Request.ReadTimeout.Duration(),
			WriteTimeout:      settings.Server.WriteTimeout.Duration(),
			IdleTimeout:       settings.Server.IdleTimeout.Duration(),
			MaxHeaderBytes:    int(settings.Server.MaxHeaderBytes),
		},
	}
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
