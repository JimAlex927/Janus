// Package upstream selects a backend independently of HTTP forwarding.
package upstream

import (
	"fmt"
	"net/url"
	"sync/atomic"
)

type Pool struct {
	targets []url.URL
	next    atomic.Uint64
}

func New(targets []*url.URL) (*Pool, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("empty upstream pool")
	}
	p := &Pool{targets: make([]url.URL, len(targets))}
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
