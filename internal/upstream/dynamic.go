package upstream

import (
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"sort"
	"sync/atomic"
	"time"
)

// WeightedTarget is independent of the registry that supplied the address.
type WeightedTarget struct {
	URL    url.URL
	Weight float64
}

type dynamicSnapshot struct {
	targets    []WeightedTarget
	cumulative []float64
	total      float64
	expires    time.Time
}

// DynamicPool publishes immutable membership snapshots. A request reads one
// snapshot and keeps its chosen URL even if membership changes during proxying.
// An empty or expired snapshot is a normal unavailable state, not a panic.
type DynamicPool struct {
	current atomic.Pointer[dynamicSnapshot]
}

func (p *DynamicPool) Replace(targets []WeightedTarget, expires time.Time) error {
	s := &dynamicSnapshot{targets: append([]WeightedTarget(nil), targets...), expires: expires}
	for _, target := range s.targets {
		if target.Weight <= 0 || math.IsNaN(target.Weight) || math.IsInf(target.Weight, 0) {
			return fmt.Errorf("invalid upstream weight")
		}
		s.total += target.Weight
		if math.IsInf(s.total, 0) {
			return fmt.Errorf("upstream weight sum overflow")
		}
		s.cumulative = append(s.cumulative, s.total)
	}
	p.current.Store(s)
	return nil
}

func (p *DynamicPool) NextHealthy() (url.URL, bool) {
	s := p.current.Load()
	if s == nil || len(s.targets) == 0 || (!s.expires.IsZero() && !time.Now().Before(s.expires)) {
		return url.URL{}, false
	}
	// Weighted random selection keeps the request path lock-free. Zero-weight,
	// disabled and unhealthy instances are removed before building this snapshot.
	value := rand.Float64() * s.total
	index := sort.Search(len(s.cumulative), func(i int) bool { return s.cumulative[i] > value })
	if index == len(s.targets) {
		index--
	}
	return s.targets[index].URL, true
}
