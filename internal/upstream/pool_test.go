package upstream

import (
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentRoundRobin(t *testing.T) {
	a, _ := url.Parse("http://a")
	b, _ := url.Parse("http://b")
	p, err := New([]*url.URL{a, b})
	if err != nil {
		t.Fatal(err)
	}
	a.Host = "mutated"
	var countA, countB atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				u := p.Next()
				switch u.Host {
				case "a":
					countA.Add(1)
				case "b":
					countB.Add(1)
				default:
					t.Errorf("unexpected target %s", u.Host)
				}
			}
		}()
	}
	wg.Wait()
	if countA.Load() != 5000 || countB.Load() != 5000 {
		t.Fatalf("unbalanced: %d/%d", countA.Load(), countB.Load())
	}
}
