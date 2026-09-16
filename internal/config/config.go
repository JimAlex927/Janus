// Package config owns the file format and validates it before listeners open.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const MaxConfigBytes = 1 << 20

type Config struct {
	Version        int                    `json:"version,omitempty"`
	Listen         string                 `json:"listen"`
	TrustedProxies []string               `json:"trusted_proxies,omitempty"`
	Limens         map[string]LimenConfig `json:"limens,omitempty"`
	Settings       Settings               `json:"settings"`
	Middlewares    map[string]Middleware  `json:"middlewares"`
	Services       map[string]Service     `json:"services"`
	Routes         []Route                `json:"routes"`
}

const (
	CurrentConfigVersion   = 1
	ProtocolHTTP1          = "http1"
	ProtocolHTTP2          = "http2"
	ProtocolHTTP3          = "http3"
	RouteProtocolHTTP      = "http"
	RouteProtocolSSE       = "sse"
	RouteProtocolWebSocket = "websocket"
	MaxTrustedProxyCIDRs   = 128
)

// LimenConfig describes one inbound protocol binding. HTTP/2 is enabled only
// over TLS in the first native multi-protocol profile.
type LimenConfig struct {
	Address        string         `json:"address"`
	Protocols      []string       `json:"protocols"`
	TrustedProxies []string       `json:"trusted_proxies,omitempty"`
	TLS            *TLSSettings   `json:"tls,omitempty"`
	HTTP3          *HTTP3Settings `json:"http3,omitempty"`
}

type TLSSettings struct {
	CertFile   string `json:"cert_file"`
	KeyFile    string `json:"key_file"`
	MinVersion string `json:"min_version,omitempty"`
}

// HTTP3Settings controls the inbound QUIC stream budget for an HTTP/3 Limen.
// The setting is intentionally small: HTTP/3 connection idle and header
// budgets are inherited from the validated server settings.
type HTTP3Settings struct {
	MaxConcurrentStreams int64 `json:"max_concurrent_streams"`
}

const (
	DefaultHTTP3MaxConcurrentStreams int64 = 100
	MaxHTTP3ConcurrentStreams              = 1_000_000
)

func (s HTTP3Settings) WithDefaults() HTTP3Settings {
	if s.MaxConcurrentStreams == 0 {
		s.MaxConcurrentStreams = DefaultHTTP3MaxConcurrentStreams
	}
	return s
}

func (s HTTP3Settings) Validate() error {
	s = s.WithDefaults()
	if s.MaxConcurrentStreams < 1 || s.MaxConcurrentStreams > MaxHTTP3ConcurrentStreams {
		return fmt.Errorf("http3.max_concurrent_streams must be between 1 and %d", MaxHTTP3ConcurrentStreams)
	}
	return nil
}

// Middleware is a named, typed route/service middleware definition.
// Exactly one policy is allowed per definition.
type Middleware struct {
	Buffer    *BufferSettings    `json:"buffer,omitempty"`
	BodyLimit *BodyLimitSettings `json:"body_limit,omitempty"`
	InFlight  *InFlightSettings  `json:"in_flight,omitempty"`
}

type BufferSettings struct {
	MaxResponseBodyBytes int64 `json:"max_response_body_bytes"`
}

type BodyLimitSettings struct {
	MaxBytes int64 `json:"max_bytes"`
}

type InFlightSettings struct {
	MaxConcurrent int `json:"max_concurrent"`
}

type Service struct {
	Upstreams   []string             `json:"upstreams"`
	Middlewares []string             `json:"middlewares"`
	HealthCheck *HealthCheckSettings `json:"health_check,omitempty"`
}

// HealthCheckSettings controls optional active HTTP health probes for every
// upstream in a service. A nil value leaves the service in round-robin mode.
// Zero values are filled by WithDefaults when the check is enabled.
type HealthCheckSettings struct {
	Path               string   `json:"path"`
	Interval           Duration `json:"interval"`
	Timeout            Duration `json:"timeout"`
	Jitter             Duration `json:"jitter"`
	UnhealthyThreshold int      `json:"unhealthy_threshold"`
	HealthyThreshold   int      `json:"healthy_threshold"`
	ExpectedStatus     int      `json:"expected_status"`
}

const (
	DefaultHealthCheckInterval = Duration(30 * time.Second)
	DefaultHealthCheckTimeout  = Duration(5 * time.Second)
	DefaultHealthCheckFailures = 1
	DefaultHealthCheckPasses   = 1
	MaxHealthCheckThreshold    = 100
)

func (s HealthCheckSettings) WithDefaults() HealthCheckSettings {
	if s.Interval == 0 {
		s.Interval = DefaultHealthCheckInterval
	}
	if s.Timeout == 0 {
		s.Timeout = DefaultHealthCheckTimeout
	}
	if s.UnhealthyThreshold == 0 {
		s.UnhealthyThreshold = DefaultHealthCheckFailures
	}
	if s.HealthyThreshold == 0 {
		s.HealthyThreshold = DefaultHealthCheckPasses
	}
	return s
}

type Route struct {
	Name        string   `json:"name"`
	Limen       string   `json:"limen,omitempty"`
	Protocols   []string `json:"protocols,omitempty"`
	Host        string   `json:"host"`
	PathPrefix  string   `json:"path_prefix"`
	Service     string   `json:"service"`
	Middlewares []string `json:"middlewares"`
}

func Load(r io.Reader) (Config, error) {
	var c Config
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("config must contain exactly one JSON object")
	}
	c = c.WithDefaults()
	return c, c.Validate()
}

// LoadFile loads a configuration and resolves relative TLS asset paths against
// the configuration file directory.
func LoadFile(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > MaxConfigBytes {
		return Config{}, fmt.Errorf("config exceeds %d bytes", MaxConfigBytes)
	}
	return LoadFileBytes(path, data)
}

// LoadFileSnapshot reads and validates one bounded configuration file and
// returns a content hash for polling deduplication. The hash is returned even
// when validation fails, allowing a watcher to suppress repeated errors until
// the file changes again.
func LoadFileSnapshot(path string) (Config, [sha256.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, [sha256.Size]byte{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return Config{}, [sha256.Size]byte{}, fmt.Errorf("read config: %w", err)
	}
	hash := sha256.Sum256(data)
	if len(data) > MaxConfigBytes {
		return Config{}, hash, fmt.Errorf("config exceeds %d bytes", MaxConfigBytes)
	}
	c, err := LoadFileBytes(path, data)
	return c, hash, err
}

// LoadFileBytes parses configuration bytes and resolves relative TLS asset
// paths against the supplied configuration file path. It is used by the
// watcher so parsing and hashing operate on the same atomic-replacement read.
func LoadFileBytes(path string, data []byte) (Config, error) {
	c, err := Load(bytes.NewReader(data))
	if err != nil {
		return c, err
	}
	base := filepath.Dir(path)
	for name, binding := range c.Limens {
		if binding.TLS == nil {
			continue
		}
		if !filepath.IsAbs(binding.TLS.CertFile) {
			binding.TLS.CertFile = filepath.Join(base, binding.TLS.CertFile)
		}
		if !filepath.IsAbs(binding.TLS.KeyFile) {
			binding.TLS.KeyFile = filepath.Join(base, binding.TLS.KeyFile)
		}
		c.Limens[name] = binding
	}
	return c, nil
}

func (c Config) Validate() error {
	bindings, err := c.validateLimenBindings()
	if err != nil {
		return err
	}
	settings := c.Settings.WithDefaults()
	if err := settings.Validate(); err != nil {
		return err
	}
	if len(c.Services) == 0 || len(c.Routes) == 0 {
		return fmt.Errorf("at least one service and route are required")
	}
	for name, s := range c.Services {
		if name == "" || len(s.Upstreams) == 0 {
			return fmt.Errorf("service %q needs a name and upstreams", name)
		}
		if s.HealthCheck != nil {
			if err := validateHealthCheck(name, s.HealthCheck.WithDefaults()); err != nil {
				return err
			}
		}
		seen := map[string]bool{}
		for _, raw := range s.Upstreams {
			if _, err := ParseUpstream(raw); err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
			if seen[raw] {
				return fmt.Errorf("service %q has duplicate upstream %q", name, raw)
			}
			seen[raw] = true
		}
		seenMiddlewares := map[string]bool{}
		inFlightPolicies := 0
		for _, middlewareName := range s.Middlewares {
			if middlewareName == "" {
				return fmt.Errorf("service %q has an empty middleware reference", name)
			}
			if seenMiddlewares[middlewareName] {
				return fmt.Errorf("service %q references middleware %q more than once", name, middlewareName)
			}
			seenMiddlewares[middlewareName] = true
			if _, ok := c.Middlewares[middlewareName]; !ok {
				return fmt.Errorf("service %q references missing middleware %q", name, middlewareName)
			}
			if c.Middlewares[middlewareName].InFlight != nil {
				inFlightPolicies++
			}
		}
		if inFlightPolicies > 1 {
			return fmt.Errorf("service %q may reference at most one in_flight middleware", name)
		}
	}
	names, matches := map[string]bool{}, map[string]bool{}
	for _, r := range c.Routes {
		if r.Name == "" || names[r.Name] {
			return fmt.Errorf("route names must be nonempty and unique: %q", r.Name)
		}
		names[r.Name] = true
		if _, ok := c.Services[r.Service]; !ok {
			return fmt.Errorf("route %q references missing service %q", r.Name, r.Service)
		}
		if r.Limen == "" {
			if len(bindings) > 1 {
				return fmt.Errorf("route %q must reference a limen when multiple limens are configured", r.Name)
			}
		} else if _, ok := bindings[r.Limen]; !ok {
			return fmt.Errorf("route %q references missing limen %q", r.Name, r.Limen)
		}
		seenProtocols := map[string]bool{}
		for _, routeProtocol := range r.Protocols {
			if routeProtocol != RouteProtocolHTTP && routeProtocol != RouteProtocolSSE && routeProtocol != RouteProtocolWebSocket {
				return fmt.Errorf("route %q has unsupported protocol %q", r.Name, routeProtocol)
			}
			if seenProtocols[routeProtocol] {
				return fmt.Errorf("route %q enables protocol %q more than once", r.Name, routeProtocol)
			}
			seenProtocols[routeProtocol] = true
		}
		seenMiddlewares := map[string]bool{}
		for _, middlewareName := range r.Middlewares {
			if middlewareName == "" {
				return fmt.Errorf("route %q has an empty middleware reference", r.Name)
			}
			if seenMiddlewares[middlewareName] {
				return fmt.Errorf("route %q references middleware %q more than once", r.Name, middlewareName)
			}
			seenMiddlewares[middlewareName] = true
			if _, ok := c.Middlewares[middlewareName]; !ok {
				return fmt.Errorf("route %q references missing middleware %q", r.Name, middlewareName)
			}
			if c.Middlewares[middlewareName].InFlight != nil {
				return fmt.Errorf("route %q cannot use service-only in_flight middleware %q", r.Name, middlewareName)
			}
		}
		if !strings.HasPrefix(r.PathPrefix, "/") || strings.ContainsAny(r.PathPrefix, "?#%\\ \t\r\n") {
			return fmt.Errorf("route %q needs an unescaped absolute path prefix", r.Name)
		}
		if r.PathPrefix != "/" && strings.HasSuffix(r.PathPrefix, "/") {
			return fmt.Errorf("route %q: omit trailing slash from path_prefix", r.Name)
		}
		// Exact DNS names only in v0; no ports, wildcards, or IPv6 host rules.
		if strings.ContainsAny(r.Host, ":/*?#@\\ \t\r\n") {
			return fmt.Errorf("route %q host must be an exact hostname without port", r.Name)
		}
		scope := r.Limen
		if scope == "" && len(bindings) == 1 {
			for name := range bindings {
				scope = name
			}
		}
		key := scope + "\x00" + strings.ToLower(r.Host) + "\x00" + r.PathPrefix
		if matches[key] {
			return fmt.Errorf("duplicate host/path match on route %q", r.Name)
		}
		matches[key] = true
	}
	for name, definition := range c.Middlewares {
		if name == "" {
			return fmt.Errorf("middleware names must be nonempty")
		}
		defined := 0
		if definition.Buffer != nil {
			defined++
		}
		if definition.BodyLimit != nil {
			defined++
		}
		if definition.InFlight != nil {
			defined++
		}
		if defined != 1 {
			return fmt.Errorf("middleware %q must define exactly one policy", name)
		}
		if definition.Buffer != nil && (definition.Buffer.MaxResponseBodyBytes < 1 || definition.Buffer.MaxResponseBodyBytes > MaxBufferedResponseBytes) {
			return fmt.Errorf("middleware %q buffer.max_response_body_bytes must be between 1 and %d bytes", name, MaxBufferedResponseBytes)
		}
		if definition.BodyLimit != nil && (definition.BodyLimit.MaxBytes < 1 || definition.BodyLimit.MaxBytes > MaxRequestBodyBytes) {
			return fmt.Errorf("middleware %q body_limit.max_bytes must be between 1 and %d bytes", name, MaxRequestBodyBytes)
		}
		if definition.InFlight != nil && (definition.InFlight.MaxConcurrent < 1 || definition.InFlight.MaxConcurrent > MaxBackendConnections) {
			return fmt.Errorf("middleware %q in_flight.max_concurrent must be between 1 and %d", name, MaxBackendConnections)
		}
	}
	return nil
}

func validateHealthCheck(service string, check HealthCheckSettings) error {
	if check.Path == "" || !strings.HasPrefix(check.Path, "/") || strings.ContainsAny(check.Path, "?#%\\ \t\r\n") {
		return fmt.Errorf("service %q health_check.path must be an unescaped absolute path", service)
	}
	if err := validateDuration("health_check.interval", check.Interval); err != nil {
		return fmt.Errorf("service %q: %w", service, err)
	}
	if err := validateDuration("health_check.timeout", check.Timeout); err != nil {
		return fmt.Errorf("service %q: %w", service, err)
	}
	if check.Timeout.Duration() > check.Interval.Duration() {
		return fmt.Errorf("service %q health_check.timeout must not exceed interval", service)
	}
	if check.Jitter.Duration() < 0 || check.Jitter.Duration() > MaxSettingDuration {
		return fmt.Errorf("service %q health_check.jitter must be between 0s and %s", service, MaxSettingDuration)
	}
	if check.Jitter.Duration() > check.Interval.Duration() {
		return fmt.Errorf("service %q health_check.jitter must not exceed interval", service)
	}
	if check.UnhealthyThreshold < 1 || check.UnhealthyThreshold > MaxHealthCheckThreshold {
		return fmt.Errorf("service %q health_check.unhealthy_threshold must be between 1 and %d", service, MaxHealthCheckThreshold)
	}
	if check.HealthyThreshold < 1 || check.HealthyThreshold > MaxHealthCheckThreshold {
		return fmt.Errorf("service %q health_check.healthy_threshold must be between 1 and %d", service, MaxHealthCheckThreshold)
	}
	if check.ExpectedStatus != 0 && (check.ExpectedStatus < http.StatusOK || check.ExpectedStatus > 599) {
		return fmt.Errorf("service %q health_check.expected_status must be 0 or between 200 and 599", service)
	}
	return nil
}

// LimenBindings returns the validated inbound bindings. Legacy configurations
// are normalized to one plaintext HTTP/1 binding named "default".
func (c Config) LimenBindings() map[string]LimenConfig {
	bindings, _ := c.validateLimenBindings()
	return bindings
}

func (c Config) validateLimenBindings() (map[string]LimenConfig, error) {
	if c.Version == 0 && len(c.Limens) == 0 {
		if err := validateListenAddress(c.Listen); err != nil {
			return nil, err
		}
		trusted, err := normalizeTrustedProxies("default", c.TrustedProxies)
		if err != nil {
			return nil, err
		}
		return map[string]LimenConfig{
			"default": {Address: c.Listen, Protocols: []string{ProtocolHTTP1}, TrustedProxies: trusted},
		}, nil
	}
	if c.Version != CurrentConfigVersion {
		return nil, fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Listen != "" {
		return nil, fmt.Errorf("listen cannot be combined with versioned limens")
	}
	if len(c.TrustedProxies) != 0 {
		return nil, fmt.Errorf("trusted_proxies must be configured on a versioned limen")
	}
	if len(c.Limens) == 0 {
		return nil, fmt.Errorf("at least one limen is required")
	}
	bindings := make(map[string]LimenConfig, len(c.Limens))
	for name, binding := range c.Limens {
		if name == "" {
			return nil, fmt.Errorf("limen names must be nonempty")
		}
		if err := validateListenAddress(binding.Address); err != nil {
			return nil, fmt.Errorf("limen %q: %w", name, err)
		}
		if len(binding.Protocols) == 0 {
			return nil, fmt.Errorf("limen %q must enable at least one protocol", name)
		}
		seen := map[string]bool{}
		for _, protocol := range binding.Protocols {
			if protocol != ProtocolHTTP1 && protocol != ProtocolHTTP2 && protocol != ProtocolHTTP3 {
				return nil, fmt.Errorf("limen %q has unsupported protocol %q", name, protocol)
			}
			if seen[protocol] {
				return nil, fmt.Errorf("limen %q enables protocol %q more than once", name, protocol)
			}
			seen[protocol] = true
		}
		if binding.HTTP3 != nil {
			if !seen[ProtocolHTTP3] {
				return nil, fmt.Errorf("limen %q configures http3 settings without enabling HTTP/3", name)
			}
			http3 := binding.HTTP3.WithDefaults()
			if err := http3.Validate(); err != nil {
				return nil, fmt.Errorf("limen %q: %w", name, err)
			}
			binding.HTTP3 = &http3
		}
		if (seen[ProtocolHTTP2] || seen[ProtocolHTTP3]) && binding.TLS == nil {
			return nil, fmt.Errorf("limen %q: HTTP/2 and HTTP/3 require TLS", name)
		}
		if seen[ProtocolHTTP3] && !seen[ProtocolHTTP1] && !seen[ProtocolHTTP2] {
			return nil, fmt.Errorf("limen %q: HTTP/3 requires an HTTP/1 or HTTP/2 TCP fallback", name)
		}
		if err := validateTLSSettings(name, binding.TLS); err != nil {
			return nil, err
		}
		trusted, err := normalizeTrustedProxies(name, binding.TrustedProxies)
		if err != nil {
			return nil, err
		}
		binding.Protocols = append([]string(nil), binding.Protocols...)
		binding.TrustedProxies = trusted
		bindings[name] = binding
	}
	return bindings, nil
}

func normalizeTrustedProxies(limenName string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > MaxTrustedProxyCIDRs {
		return nil, fmt.Errorf("limen %q has more than %d trusted proxy CIDRs", limenName, MaxTrustedProxyCIDRs)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("limen %q trusted_proxies entry %q is not a CIDR", limenName, raw)
		}
		canonical := network.String()
		if _, ok := seen[canonical]; ok {
			return nil, fmt.Errorf("limen %q has duplicate trusted proxy CIDR %q", limenName, canonical)
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result, nil
}

func validateListenAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || p < 1 || p > 65535 {
		return fmt.Errorf("address must be host:port with port 1..65535")
	}
	return nil
}

func validateTLSSettings(name string, settings *TLSSettings) error {
	if settings == nil {
		return nil
	}
	if strings.TrimSpace(settings.CertFile) == "" || strings.TrimSpace(settings.KeyFile) == "" {
		return fmt.Errorf("limen %q TLS requires cert_file and key_file", name)
	}
	if settings.MinVersion != "" && settings.MinVersion != "1.2" && settings.MinVersion != "1.3" &&
		settings.MinVersion != "TLS1.2" && settings.MinVersion != "TLS1.3" {
		return fmt.Errorf("limen %q TLS min_version must be 1.2 or 1.3", name)
	}
	return nil
}

// WithDefaults returns a copy with omitted settings filled from the starter
// profile. Explicit non-zero values are preserved for validation.
func (c Config) WithDefaults() Config {
	c.Settings = c.Settings.WithDefaults()
	if len(c.Services) > 0 {
		services := make(map[string]Service, len(c.Services))
		for name, service := range c.Services {
			if service.HealthCheck != nil {
				check := service.HealthCheck.WithDefaults()
				service.HealthCheck = &check
			}
			services[name] = service
		}
		c.Services = services
	}
	return c
}

// ParseUpstream accepts origins only. Path rewriting is a separate future policy.
func ParseUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("upstream must be an http(s) origin without credentials, path, query, or fragment")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("invalid upstream port")
		}
	}
	return u, nil
}
