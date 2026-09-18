// Package discovery owns shared registry clients and service subscriptions.
// SDK-specific code lives in adapters; the request path only sees a local pool.
package discovery

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"sort"
	"strconv"

	"janus/internal/config"
	"janus/internal/upstream"
)

type Instance struct {
	IP      string
	Port    uint64
	Weight  float64
	Healthy bool
	Enabled bool
}

// Version changes only when the registry actually refreshes the service. A
// successful read from an SDK cache must not extend an old snapshot's lease.
type Snapshot struct {
	Version   uint64
	Instances []Instance
}

type Client interface {
	Watch(config.NacosService, func()) (cancel func(), err error)
	Snapshot(config.NacosService) (Snapshot, error)
	Close()
}

type Factory func(config.NacosRegistry) (Client, error)

func targetsFor(snapshot Snapshot, scheme string) ([]upstream.WeightedTarget, error) {
	if snapshot.Version == 0 {
		return nil, fmt.Errorf("registry has no confirmed snapshot")
	}
	if len(snapshot.Instances) > 10000 {
		return nil, fmt.Errorf("registry instance limit exceeded")
	}
	byOrigin := make(map[string]upstream.WeightedTarget)
	for _, instance := range snapshot.Instances {
		if !instance.Healthy || !instance.Enabled || instance.Weight <= 0 {
			continue
		}
		ip := net.ParseIP(instance.IP)
		if ip == nil || instance.Port == 0 || instance.Port > 65535 || math.IsNaN(instance.Weight) || math.IsInf(instance.Weight, 0) {
			return nil, fmt.Errorf("registry returned an invalid instance")
		}
		u := url.URL{Scheme: scheme, Host: net.JoinHostPort(ip.String(), strconv.FormatUint(instance.Port, 10))}
		key := u.String()
		// Repeated endpoint registrations must not multiply traffic weight.
		if previous, ok := byOrigin[key]; !ok || instance.Weight > previous.Weight {
			byOrigin[key] = upstream.WeightedTarget{URL: u, Weight: instance.Weight}
		}
	}
	keys := make([]string, 0, len(byOrigin))
	for key := range byOrigin {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]upstream.WeightedTarget, 0, len(keys))
	for _, key := range keys {
		result = append(result, byOrigin[key])
	}
	return result, nil
}
