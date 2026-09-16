// Package admin provides the private process-health HTTP endpoints.
package admin

import (
	"net/http"
	"sync/atomic"

	"janus/internal/telemetry"
)

type State struct {
	live  atomic.Bool
	ready atomic.Bool
}

func NewState() *State {
	state := &State{}
	state.live.Store(true)
	return state
}

func (s *State) SetLive(value bool) {
	if s != nil {
		s.live.Store(value)
	}
}

func (s *State) SetReady(value bool) {
	if s != nil {
		s.ready.Store(value)
	}
}

func (s *State) Live() bool { return s != nil && s.live.Load() }

func (s *State) Ready() bool { return s != nil && s.ready.Load() }

func NewHandler(state *State) http.Handler {
	return NewHandlerWithMetrics(state, nil, nil)
}

// NewHandlerWithMetrics adds a scrape-only metrics endpoint to the private
// admin listener. The health callback must return a bounded active-generation
// snapshot and may be nil when no health checks are configured.
func NewHandlerWithMetrics(state *State, metrics *telemetry.Metrics, health func() []telemetry.BackendHealth) http.Handler {
	if state == nil {
		state = NewState()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !state.Live() {
			http.Error(w, "not live", http.StatusServiceUnavailable)
			return
		}
		writeOK(w, r)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !state.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		writeOK(w, r)
	})
	if metrics != nil {
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodHead {
				return
			}
			var snapshot []telemetry.BackendHealth
			if health != nil {
				snapshot = health()
			}
			_, _ = w.Write(metrics.Render(snapshot))
		})
	}
	return mux
}

func writeOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("ok\n"))
	}
}
