package middleware

import (
	"bytes"
	"context"
	"errors"
	"net/http"
)

var errResponseTooLarge = errors.New("buffered response exceeds configured limit")

// Buffer delays response commitment until the wrapped handler returns. It is
// intended for finite API responses; Flush is deliberately a no-op, so this
// middleware must not be used for streaming protocols.
func Buffer(maxResponseBodyBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buffered := &responseBuffer{
				header:   make(http.Header),
				maxBytes: maxResponseBodyBytes,
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered != http.ErrAbortHandler {
						panic(recovered)
					}
					if errors.Is(r.Context().Err(), context.Canceled) {
						return
					}
					status := http.StatusBadGateway
					if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
						status = http.StatusGatewayTimeout
					}
					http.Error(w, http.StatusText(status), status)
					return
				}
				if buffered.writeErr != nil {
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					return
				}
				buffered.commit(w)
			}()

			next.ServeHTTP(buffered, r)
		})
	}
}

type responseBuffer struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
	maxBytes    int64
	writeErr    error
}

func (b *responseBuffer) Header() http.Header { return b.header }

func (b *responseBuffer) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		// Informational responses are not part of the buffered final response.
		// The initial API profile does not expose them through this policy.
		return
	}
	if b.wroteHeader {
		return
	}
	b.status = status
	b.wroteHeader = true
}

func (b *responseBuffer) Write(p []byte) (int, error) {
	if !b.wroteHeader {
		b.WriteHeader(http.StatusOK)
	}
	if b.writeErr != nil {
		return 0, b.writeErr
	}
	if b.maxBytes > 0 && int64(b.body.Len())+int64(len(p)) > b.maxBytes {
		b.writeErr = errResponseTooLarge
		return 0, b.writeErr
	}
	return b.body.Write(p)
}

// Flush intentionally does not expose buffered bytes to the client.
func (b *responseBuffer) Flush() {}

func (b *responseBuffer) commit(dst http.ResponseWriter) {
	for key, values := range b.header {
		dst.Header()[key] = append([]string(nil), values...)
	}
	status := b.status
	if !b.wroteHeader {
		status = http.StatusOK
	}
	dst.WriteHeader(status)
	if b.body.Len() > 0 {
		_, _ = dst.Write(b.body.Bytes())
	}
}
