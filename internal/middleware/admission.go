package middleware

import (
	"net/http"
	"sync"

	"janus/internal/telemetry"
)

// Limiter is a non-waiting concurrency gate. Its limit can be lowered while
// permits are active; existing work is allowed to finish and new work is
// rejected until usage falls below the new limit.
type Limiter struct {
	mu        sync.Mutex
	limit     int
	active    int
	accepting bool
	metrics   *telemetry.Metrics
	scope     string
	service   string
}

func NewLimiter(limit int) *Limiter { return &Limiter{limit: limit, accepting: true} }

// NewLimiterWithMetrics creates an admission gate that also exports its
// current permit gauge and rejection count.
func NewLimiterWithMetrics(limit int, metrics *telemetry.Metrics, scope, service string) *Limiter {
	return &Limiter{limit: limit, accepting: true, metrics: metrics, scope: scope, service: service}
}

func (l *Limiter) SetLimit(limit int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.limit = limit
	l.mu.Unlock()
}

// Stop prevents new acquisitions while existing requests retain their permits.
func (l *Limiter) Stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.accepting = false
	l.mu.Unlock()
}

func (l *Limiter) Acquire() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.accepting || l.limit < 1 || l.active >= l.limit {
		if l.metrics != nil {
			l.metrics.RecordRejection(l.scope, l.service, "capacity")
		}
		return false
	}
	l.active++
	if l.metrics != nil {
		l.metrics.AddInFlight(l.scope, l.service, 1)
	}
	return true
}

func (l *Limiter) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.active > 0 {
		l.active--
		if l.metrics != nil {
			l.metrics.AddInFlight(l.scope, l.service, -1)
		}
	}
	l.mu.Unlock()
}

func (l *Limiter) Active() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

// Admission rejects saturated requests immediately with 503. The deferred
// release runs on normal return and panic, so a handler cannot leak a permit.
func Admission(limiter *Limiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter != nil && !limiter.Acquire() {
				telemetry.MarkError(r.Context(), "admission_rejected")
				http.Error(w, "request capacity exhausted", http.StatusServiceUnavailable)
				return
			}
			if limiter != nil {
				defer limiter.Release()
			}
			next.ServeHTTP(w, r)
		})
	}
}
