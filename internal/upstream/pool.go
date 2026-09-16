// Package upstream selects a backend independently of HTTP forwarding.
package upstream

import (
	"fmt"
	"net/url"
	"sync/atomic"

	"janus/internal/health"
)

type Pool struct {
	targets []url.URL
	next    atomic.Uint64
	health  *health.Store
}

func New(targets []*url.URL) (*Pool, error) {
	return NewWithHealth(targets, nil)
}

// NewWithHealth creates a pool whose target eligibility is read from the
// service-owned health store. A nil store preserves plain round-robin mode.
func NewWithHealth(targets []*url.URL, store *health.Store) (*Pool, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("empty upstream pool")
	}
	p := &Pool{targets: make([]url.URL, len(targets)), health: store}
	for i, target := range targets {
		if target == nil {
			return nil, fmt.Errorf("nil upstream target")
		}
		p.targets[i] = *target
	}
	return p, nil
}

// Next returns a copy so a caller cannot mutate the shared configuration.
// 这里是round robin式的取出下游链接 还有其他负载均衡的方式 TODO
func (p *Pool) Next() url.URL {
	return p.targets[(p.next.Add(1)-1)%uint64(len(p.targets))]
}

// NextHealthy returns the next eligible target. It never waits for a probe;
// callers receive false when active health checks have excluded every target.
func (p *Pool) NextHealthy() (url.URL, bool) {
	if p.health == nil {
		return p.Next(), true
	}
	start := p.next.Add(1) - 1
	for offset := uint64(0); offset < uint64(len(p.targets)); offset++ {
		index := (start + offset) % uint64(len(p.targets))
		if p.health.Healthy(int(index)) {
			return p.targets[index], true
		}
	}
	return url.URL{}, false
}
