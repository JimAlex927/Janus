// Package health owns bounded active HTTP probes and the synchronized state
// consumed by upstream selection. It has no dependency on request routing.
package health

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const (
	maxProbeBodyBytes = 64 << 10
	maxProbeWorkers   = 32
)

// Settings is the runtime form of one service health-check policy.
type Settings struct {
	Path               string
	Interval           time.Duration
	Timeout            time.Duration
	Jitter             time.Duration
	UnhealthyThreshold int
	HealthyThreshold   int
	ExpectedStatus     int
}

// Store is a bounded health snapshot for one service's targets.
// Targets start healthy so a configured service remains available while its
// first probe is pending; failed probes then require the configured threshold
// before removal from selection.
type Store struct {
	targets []targetState
}

// TargetStatus is a bounded scrape-time view of one target's state.
type TargetStatus struct {
	Index   int
	Healthy bool
}

type targetState struct {
	healthy  atomic.Bool
	failures atomic.Int32
	passes   atomic.Int32
}

// NewStore creates a store with every target initially eligible.
func NewStore(targets int) *Store {
	states := make([]targetState, targets)
	for i := range states {
		states[i].healthy.Store(true)
	}
	return &Store{targets: states}
}

// Healthy reports whether the target is currently eligible for requests.
func (s *Store) Healthy(index int) bool {
	return s != nil && index >= 0 && index < len(s.targets) && s.targets[index].healthy.Load()
}

// AllUnhealthy reports whether every target is currently excluded. A nil or
// empty store is treated as unavailable rather than healthy.
func (s *Store) AllUnhealthy() bool {
	if s == nil || len(s.targets) == 0 {
		return true
	}
	for i := range s.targets {
		if s.Healthy(i) {
			return false
		}
	}
	return true
}

// Snapshot returns target indexes and current eligibility without exposing
// configured URLs to callers or metrics.
func (s *Store) Snapshot() []TargetStatus {
	if s == nil {
		return nil
	}
	result := make([]TargetStatus, len(s.targets))
	for i := range s.targets {
		result[i] = TargetStatus{Index: i, Healthy: s.Healthy(i)}
	}
	return result
}

// Record applies one probe result and returns whether the eligibility state
// changed. One checker worker owns each target at a time; atomics keep reads
// from request goroutines race-free.
func (s *Store) Record(index int, success bool, unhealthyThreshold, healthyThreshold int) bool {
	if s == nil || index < 0 || index >= len(s.targets) {
		return false
	}
	state := &s.targets[index]
	if success {
		state.failures.Store(0)
		if state.healthy.Load() {
			state.passes.Store(0)
			return false
		}
		passes := state.passes.Add(1)
		if passes < int32(healthyThreshold) {
			return false
		}
		state.passes.Store(0)
		return state.healthy.Swap(true) != true
	}

	state.passes.Store(0)
	if !state.healthy.Load() {
		return false
	}
	failures := state.failures.Add(1)
	if failures < int32(unhealthyThreshold) {
		return false
	}
	state.failures.Store(0)
	return state.healthy.Swap(false) != false
}

// Checker periodically probes every target using a bounded worker pool.
// Closing it cancels in-flight probes and waits for all worker goroutines.
type Checker struct {
	targets  []url.URL
	settings Settings
	store    *Store
	client   *http.Client
	logger   *zap.Logger

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	rngMu  sync.Mutex
	rng    *rand.Rand
}

// New creates and starts a checker. The supplied RoundTripper is shared with
// request forwarding and must remain alive until Close returns.
func New(targets []url.URL, settings Settings, transport http.RoundTripper, logger *zap.Logger) (*Checker, error) {
	if len(targets) == 0 || transport == nil {
		return nil, errors.New("health checker requires targets and a transport")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Checker{
		targets:  append([]url.URL(nil), targets...),
		settings: settings,
		store:    NewStore(len(targets)),
		client: &http.Client{
			Transport: transport,
			// Evaluate the first response. Following a redirect could probe a
			// different origin than the configured upstream.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	go c.run()
	return c, nil
}

// Store returns the health state used by upstream selection.
func (c *Checker) Store() *Store { return c.store }

// Close stops scheduling, cancels active probes and waits for worker exit.
func (c *Checker) Close() {
	if c == nil {
		return
	}
	c.once.Do(func() { c.cancel() })
	<-c.done
}

func (c *Checker) run() {
	defer close(c.done)
	workers := len(c.targets)
	if workers > maxProbeWorkers {
		workers = maxProbeWorkers
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-c.ctx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					if !c.waitJitter() {
						return
					}
					success, err := c.probe(index)
					changed := c.store.Record(index, success, c.settings.UnhealthyThreshold, c.settings.HealthyThreshold)
					if changed {
						c.logger.Info("upstream health changed", zap.Int("target", index), zap.Bool("healthy", success), zap.Error(err))
					} else if err != nil {
						c.logger.Debug("upstream health probe failed", zap.Int("target", index), zap.Error(err))
					}
				}
			}
		}()
	}

	first := true
	for {
		if !first && !c.waitInterval() {
			break
		}
		first = false
		for index := range c.targets {
			select {
			case <-c.ctx.Done():
				close(jobs)
				wg.Wait()
				return
			case jobs <- index:
			}
		}
	}
	close(jobs)
	wg.Wait()
}

func (c *Checker) waitInterval() bool {
	timer := time.NewTimer(c.settings.Interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *Checker) waitJitter() bool {
	if c.settings.Jitter <= 0 {
		return true
	}
	c.rngMu.Lock()
	delay := time.Duration(c.rng.Int63n(int64(c.settings.Jitter) + 1))
	c.rngMu.Unlock()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *Checker) probe(index int) (bool, error) {
	ctx, cancel := context.WithTimeout(c.ctx, c.settings.Timeout)
	defer cancel()
	target := c.targets[index]
	target.Path = c.settings.Path
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "Janus-HealthCheck/1")
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	// Drain only a bounded prefix so successful probes can reuse connections
	// without allowing a misbehaving health endpoint to consume unbounded data.
	_, _ = io.CopyN(io.Discard, resp.Body, maxProbeBodyBytes)
	if c.settings.ExpectedStatus != 0 {
		return resp.StatusCode == c.settings.ExpectedStatus, nil
	}
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices, nil
}
