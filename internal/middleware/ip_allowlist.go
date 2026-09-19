package middleware

import (
	"fmt"
	"net"
	"net/http"
)

type IPAllowListOptions struct {
	SourceRanges []string
	ClientIP     func(*http.Request) string
}

func IPAllowList(options IPAllowListOptions) (Middleware, error) {
	if len(options.SourceRanges) == 0 || len(options.SourceRanges) > 128 {
		return nil, fmt.Errorf("ip_allowlist requires 1 to 128 source ranges")
	}
	networks := make([]*net.IPNet, 0, len(options.SourceRanges))
	for _, raw := range options.SourceRanges {
		ip, network, err := net.ParseCIDR(raw)
		if err != nil || ip == nil {
			return nil, fmt.Errorf("invalid source range %q", raw)
		}
		networks = append(networks, network)
	}
	clientIP := options.ClientIP
	if clientIP == nil {
		clientIP = requestPeerIP
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := net.ParseIP(clientIP(r))
			allowed := false
			for _, network := range networks {
				if network.Contains(ip) {
					allowed = true
					break
				}
			}
			if !allowed {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func requestPeerIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
