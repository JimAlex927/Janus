package middleware

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"janus/internal/protocol"
)

// StreamTimeout bounds explicitly classified SSE and classic HTTP/1 WebSocket
// requests. The finite API Timeout deliberately bypasses these requests; this
// middleware supplies a separate lifetime and no-activity contract instead.
func StreamTimeout(maxDuration, idleTimeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (!protocol.IsWebSocketRequest(r) && !protocol.WantsSSE(r)) ||
				(maxDuration <= 0 && idleTimeout <= 0) {
				next.ServeHTTP(w, r)
				return
			}

			ctx, cancel := context.WithCancelCause(r.Context())
			control := newStreamControl()
			activity := make(chan struct{}, 1)
			go runStreamTimers(ctx, cancel, control, activity, maxDuration, idleTimeout)

			wrapped := wrapStreamResponseWriter(w, ctx, control, activity)
			next.ServeHTTP(wrapped, r.WithContext(ctx))

			if control.timedOut.Load() && !control.committed.Load() {
				http.Error(w, http.StatusText(http.StatusGatewayTimeout), http.StatusGatewayTimeout)
			}
			if !control.hijacked.Load() || control.closed.Load() {
				control.stop()
				cancel(nil)
			}
		})
	}
}

type streamControl struct {
	connMu sync.Mutex
	conn   net.Conn

	stopOnce sync.Once
	stopCh   chan struct{}

	hijacked  atomic.Bool
	closed    atomic.Bool
	committed atomic.Bool
	timedOut  atomic.Bool
}

func newStreamControl() *streamControl {
	return &streamControl{stopCh: make(chan struct{})}
}

func (c *streamControl) stop() {
	c.stopOnce.Do(func() { close(c.stopCh) })
}

func (c *streamControl) setConn(conn net.Conn) {
	c.connMu.Lock()
	if c.closed.Load() || c.timedOut.Load() {
		c.connMu.Unlock()
		_ = conn.Close()
		return
	}
	c.conn = conn
	c.connMu.Unlock()
}

func (c *streamControl) closeConn() {
	c.connMu.Lock()
	conn := c.conn
	c.conn = nil
	c.connMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *streamControl) connectionClosed() {
	c.closed.Store(true)
	c.connMu.Lock()
	c.conn = nil
	c.connMu.Unlock()
	c.stop()
}

func runStreamTimers(ctx context.Context, cancel context.CancelCauseFunc, control *streamControl, activity <-chan struct{}, maxDuration, idleTimeout time.Duration) {
	var maxTimer *time.Timer
	var maxCh <-chan time.Time
	if maxDuration > 0 {
		maxTimer = time.NewTimer(maxDuration)
		maxCh = maxTimer.C
	}
	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if idleTimeout > 0 {
		idleTimer = time.NewTimer(idleTimeout)
		idleCh = idleTimer.C
	}
	defer func() {
		if maxTimer != nil {
			maxTimer.Stop()
		}
		if idleTimer != nil {
			idleTimer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			control.closeConn()
			return
		case <-control.stopCh:
			return
		case <-maxCh:
			control.timedOut.Store(true)
			cancel(context.DeadlineExceeded)
			control.closeConn()
			return
		case <-idleCh:
			control.timedOut.Store(true)
			cancel(context.DeadlineExceeded)
			control.closeConn()
			return
		case <-activity:
			if idleTimer != nil {
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(idleTimeout)
				idleCh = idleTimer.C
			}
		}
	}
}

type streamResponseWriter struct {
	http.ResponseWriter
	ctx      context.Context
	control  *streamControl
	activity chan<- struct{}
}

func (w *streamResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *streamResponseWriter) WriteHeader(status int) {
	if w.ctx.Err() != nil {
		return
	}
	w.control.committed.Store(true)
	w.ResponseWriter.WriteHeader(status)
}

func (w *streamResponseWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	w.control.committed.Store(true)
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.touch()
	}
	return n, err
}

func (w *streamResponseWriter) touch() {
	select {
	case w.activity <- struct{}{}:
	default:
	}
}

func (w *streamResponseWriter) flush() {
	if w.ctx.Err() != nil {
		return
	}
	w.control.committed.Store(true)
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
		w.touch()
	}
}

func (w *streamResponseWriter) hijack() (net.Conn, *bufio.ReadWriter, error) {
	if err := w.ctx.Err(); err != nil {
		return nil, nil, err
	}
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	conn, brw, err := hijacker.Hijack()
	if err != nil {
		return conn, brw, err
	}
	w.control.hijacked.Store(true)
	w.control.committed.Store(true)
	wrappedConn := &streamConn{Conn: conn, control: w.control}
	wrappedConn.activity = w.activity
	w.control.setConn(wrappedConn)
	w.touch()
	return wrappedConn, brw, nil
}

func wrapStreamResponseWriter(w http.ResponseWriter, ctx context.Context, control *streamControl, activity chan<- struct{}) http.ResponseWriter {
	base := &streamResponseWriter{ResponseWriter: w, ctx: ctx, control: control, activity: activity}
	_, flush := w.(http.Flusher)
	_, hijack := w.(http.Hijacker)
	switch {
	case flush && hijack:
		return &streamFlushHijackWriter{streamResponseWriter: base}
	case flush:
		return &streamFlushWriter{streamResponseWriter: base}
	case hijack:
		return &streamHijackWriter{streamResponseWriter: base}
	default:
		return base
	}
}

type streamFlushWriter struct{ *streamResponseWriter }

func (w *streamFlushWriter) Flush() { w.flush() }

type streamHijackWriter struct{ *streamResponseWriter }

func (w *streamHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

type streamFlushHijackWriter struct{ *streamResponseWriter }

func (w *streamFlushHijackWriter) Flush()                                       { w.flush() }
func (w *streamFlushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

type streamConn struct {
	net.Conn
	control  *streamControl
	activity chan<- struct{}
	once     sync.Once
}

func (c *streamConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		notifyStreamActivity(c.activity)
	}
	return n, err
}

func (c *streamConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		notifyStreamActivity(c.activity)
	}
	return n, err
}

func (c *streamConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.control.connectionClosed)
	return err
}

func notifyStreamActivity(activity chan<- struct{}) {
	if activity == nil {
		return
	}
	select {
	case activity <- struct{}{}:
	default:
	}
}
