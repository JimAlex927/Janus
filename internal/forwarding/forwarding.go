// Package forwarding derives the canonical proxy identity headers from the
// inbound connection and an explicit per-limen trusted-proxy policy.
package forwarding

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
)

const maxForwardedHops = 128

type forwardedPrefixKey struct{}

// WithForwardedPrefix carries a path prefix produced by Janus route rewriting.
// It deliberately uses request context rather than an inbound header: the
// proxy removes every client-supplied X-Forwarded-* field before it creates
// the canonical headers sent to the backend. Multiple StripPrefix policies
// compose in request order.
func WithForwardedPrefix(r *http.Request, prefix string) *http.Request {
	if existing := ForwardedPrefix(r); existing != "" {
		prefix = existing + prefix
	}
	return r.WithContext(context.WithValue(r.Context(), forwardedPrefixKey{}, prefix))
}

// ForwardedPrefix returns the trusted prefix accumulated by Janus route
// rewriting. It is empty when no StripPrefix policy matched the request.
func ForwardedPrefix(r *http.Request) string {
	prefix, _ := r.Context().Value(forwardedPrefixKey{}).(string)
	return prefix
}

// Policy trusts forwarded identity headers only when the immediate peer is in
// one of the configured CIDRs. An empty policy intentionally trusts nothing.
type Policy struct {
	networks []*net.IPNet
}

// NewPolicy parses an already validated list of CIDRs. It is kept strict so a
// policy cannot silently widen because a malformed entry was ignored.
func NewPolicy(cidrs []string) (Policy, error) {
	policy := Policy{networks: make([]*net.IPNet, 0, len(cidrs))}
	for _, value := range cidrs {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return Policy{}, err
		}
		policy.networks = append(policy.networks, network)
	}
	return policy, nil
}

// Policies contains immutable per-limen policies. A single policy is also
// used for embedded/legacy handlers that have no LimenID in their context.
type Policies struct {
	byLimen map[string]Policy
}

func NewPolicies() Policies { return Policies{byLimen: make(map[string]Policy)} }

func (p *Policies) Set(limen string, policy Policy) {
	if p.byLimen == nil {
		p.byLimen = make(map[string]Policy)
	}
	p.byLimen[limen] = policy
}

func (p Policies) For(limen string) Policy {
	if policy, ok := p.byLimen[limen]; ok {
		return policy
	}
	if len(p.byLimen) == 1 {
		for _, policy := range p.byLimen {
			return policy
		}
	}
	return Policy{}
}

// Identity is the sanitized set of forwarding headers Janus emits to a
// backend. No raw client forwarding headers are copied through.
type Identity struct {
	ForwardedFor   string
	ForwardedProto string
	ForwardedHost  string
	ClientIP       string
}

// Resolve applies a right-to-left trust-boundary walk. The immediate peer is
// always appended to the canonical X-Forwarded-For chain; when that peer is
// trusted, valid preceding hops are retained and the rightmost non-trusted
// address becomes ClientIP. Malformed chains fall back to the immediate peer.
func (p Policy) Resolve(r *http.Request) Identity {
	peer := peerIP(r.RemoteAddr)
	fallback := Identity{
		ForwardedFor:   peer,
		ForwardedProto: requestScheme(r),
		ForwardedHost:  r.Host,
		ClientIP:       peer,
	}
	if peer == "" || !p.trusted(net.ParseIP(peer)) {
		return fallback
	}

	chain, valid := forwardedChain(r.Header.Values("X-Forwarded-For"), peer)
	if !valid {
		return Identity{
			ForwardedFor:   peer,
			ForwardedProto: forwardedProto(r, requestScheme(r)),
			ForwardedHost:  forwardedHost(r, r.Host),
			ClientIP:       peer,
		}
	}
	client := peer
	for i := len(chain) - 1; i >= 0; i-- {
		ip := net.ParseIP(chain[i])
		if !p.trusted(ip) {
			client = chain[i]
			break
		}
		if i == 0 {
			client = chain[i]
		}
	}
	return Identity{
		ForwardedFor:   strings.Join(chain, ", "),
		ForwardedProto: forwardedProto(r, requestScheme(r)),
		ForwardedHost:  forwardedHost(r, r.Host),
		ClientIP:       client,
	}
}

func (p Policy) trusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range p.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func forwardedChain(values []string, peer string) ([]string, bool) {
	chain := make([]string, 0, len(values)+1)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if len(chain) >= maxForwardedHops-1 {
				return nil, false
			}
			part = strings.TrimSpace(part)
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, false
			}
			chain = append(chain, ip.String())
		}
	}
	return append(chain, peer), true
}

func peerIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil || (r.URL != nil && strings.EqualFold(r.URL.Scheme, "https")) {
		return "https"
	}
	return "http"
}

func forwardedProto(r *http.Request, fallback string) string {
	if len(r.Header.Values("X-Forwarded-Proto")) != 1 {
		return fallback
	}
	value := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	if value == "http" || value == "https" {
		return value
	}
	return fallback
}

func forwardedHost(r *http.Request, fallback string) string {
	if len(r.Header.Values("X-Forwarded-Host")) != 1 {
		return fallback
	}
	value := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if validHost(value) {
		return value
	}
	return fallback
}

func validHost(value string) bool {
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "/?#@\\ \t\r\n") {
		return false
	}
	if strings.HasPrefix(value, "[") {
		host, port, err := net.SplitHostPort(value)
		return err == nil && net.ParseIP(strings.Trim(host, "[]")) != nil && validPort(port)
	}
	if strings.Count(value, ":") == 1 {
		host, port, err := net.SplitHostPort(value)
		return err == nil && validHostname(host) && validPort(port)
	}
	if strings.Contains(value, ":") {
		return false
	}
	return validHostname(value) || net.ParseIP(value) != nil
}

func validHostname(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port >= 1 && port <= 65535
}
