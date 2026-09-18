package upstream

import (
	"math"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestDynamicPoolReplacementExpiryAndOwnership(t *testing.T) {
	p := &DynamicPool{}
	if _, ok := p.NextHealthy(); ok {
		t.Fatal("uninitialized pool is available")
	}
	targets := []WeightedTarget{{URL: url.URL{Scheme: "http", Host: "one:80"}, Weight: 1}}
	if err := p.Replace(targets, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	targets[0].URL.Host = "mutated"
	if got, ok := p.NextHealthy(); !ok || got.Host != "one:80" {
		t.Fatalf("selection: %v %v", got, ok)
	}
	if err := p.Replace([]WeightedTarget{{Weight: math.NaN()}}, time.Time{}); err == nil {
		t.Fatal("accepted invalid weights")
	}
	if got, _ := p.NextHealthy(); got.Host != "one:80" {
		t.Fatal("invalid replacement damaged active pool")
	}
	_ = p.Replace(targets, time.Now().Add(-time.Second))
	if _, ok := p.NextHealthy(); ok {
		t.Fatal("expired pool selected")
	}
	_ = p.Replace(nil, time.Time{})
	if _, ok := p.NextHealthy(); ok {
		t.Fatal("empty pool selected")
	}
}

func TestDynamicPoolConcurrentReplacement(t *testing.T) {
	p := &DynamicPool{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				if j%2 == 0 {
					_ = p.Replace([]WeightedTarget{{URL: url.URL{Scheme: "http", Host: "one:80"}, Weight: 1}}, time.Time{})
				} else {
					_ = p.Replace(nil, time.Time{})
				}
				if got, ok := p.NextHealthy(); ok && got.Host != "one:80" {
					t.Errorf("torn snapshot: %v", got)
				}
			}
		}()
	}
	wg.Wait()
}
