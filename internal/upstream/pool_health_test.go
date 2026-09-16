package upstream

import (
	"net/url"
	"testing"

	"janus/internal/health"
)

func TestNextHealthySkipsExcludedTargets(t *testing.T) {
	a, _ := url.Parse("http://a")
	b, _ := url.Parse("http://b")
	store := health.NewStore(2)
	store.Record(0, false, 1, 1)
	p, err := NewWithHealth([]*url.URL{a, b}, store)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		target, ok := p.NextHealthy()
		if !ok || target.Host != "b" {
			t.Fatalf("selected target = %s, ok=%v; want b", target.Host, ok)
		}
	}
	store.Record(1, false, 1, 1)
	if _, ok := p.NextHealthy(); ok {
		t.Fatal("expected no target when all targets are unhealthy")
	}
}
