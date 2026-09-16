package config

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	TLSExternalLoadBalancer = "external_load_balancer"

	MinRequestBodyBytes int64 = 1 << 10
	MaxRequestBodyBytes int64 = 1 << 30
	MinHeaderBytes      int64 = 1 << 10
	MaxHeaderBytes      int64 = 16 << 20

	MinExpectedConcurrency = 1
	MaxExpectedConcurrency = 1_000_000
	MinExpectedRPS         = 1
	MaxExpectedRPS         = 10_000_000

	MinSettingDuration = time.Millisecond
	MaxSettingDuration = 24 * time.Hour
)

// Duration is a time.Duration encoded as a human-readable JSON string such as
// "5s" or "250ms".
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value, err)
	}
	*d = Duration(parsed)
	return nil
}

type Settings struct {
	Request  RequestSettings  `json:"request"`
	Server   ServerSettings   `json:"server"`
	Capacity CapacitySettings `json:"capacity"`
	Backend  BackendSettings  `json:"backend"`
	SLO      SLOSettings      `json:"slo"`
	Trust    TrustSettings    `json:"trust"`
}

type RequestSettings struct {
	MaxBodyBytes    int64    `json:"max_body_bytes"`   // enforced by the body-limit middleware in phase 1
	NormalDuration  Duration `json:"normal_duration"`  // planning budget for normal API calls
	ReadTimeout     Duration `json:"read_timeout"`     // inbound request read budget, including headers and body
	MaximumDuration Duration `json:"maximum_duration"` // active end-to-end request deadline
}

type ServerSettings struct {
	ReadHeaderTimeout Duration `json:"read_header_timeout"`
	IdleTimeout       Duration `json:"idle_timeout"`
	MaxHeaderBytes    int64    `json:"max_header_bytes"`
}

type CapacitySettings struct {
	ExpectedConcurrency int `json:"expected_concurrency"`
	ExpectedRPS         int `json:"expected_rps"`
}

type BackendSettings struct {
	ConnectTimeout         Duration `json:"connect_timeout"`
	KeepAlive              Duration `json:"keep_alive"`
	TLSHandshakeTimeout    Duration `json:"tls_handshake_timeout"`
	ResponseHeaderTimeout  Duration `json:"response_header_timeout"`
	MaxResponseHeaderBytes int64    `json:"max_response_header_bytes"`
	MaxIdleConns           int      `json:"max_idle_conns"`
	MaxIdleConnsPerHost    int      `json:"max_idle_conns_per_host"`
	MaxConnsPerHost        int      `json:"max_conns_per_host"`
	IdleConnTimeout        Duration `json:"idle_conn_timeout"`
	DisableCompression     *bool    `json:"disable_compression"`
}

type SLOSettings struct {
	AcceptableP99Latency Duration `json:"acceptable_p99_latency"`
}

type TrustSettings struct {
	TLSLoadBalancerMode string   `json:"tls_load_balancer_mode"`
	TrustedProxyCIDRs   []string `json:"trusted_proxy_cidrs"`
}

// DefaultSettings records the starter's initial deployment assumptions. The
// expected capacity and p99 target are planning inputs, not admission limits.
func DefaultSettings() Settings {
	disableCompression := true
	return Settings{
		Request: RequestSettings{
			MaxBodyBytes:    8 << 20,
			NormalDuration:  Duration(5 * time.Second),
			ReadTimeout:     Duration(30 * time.Second),
			MaximumDuration: Duration(30 * time.Second),
		},
		Server: ServerSettings{
			ReadHeaderTimeout: Duration(5 * time.Second),
			IdleTimeout:       Duration(60 * time.Second),
			MaxHeaderBytes:    32 << 10,
		},
		Capacity: CapacitySettings{ExpectedConcurrency: 128, ExpectedRPS: 100},
		Backend: BackendSettings{
			ConnectTimeout:         Duration(3 * time.Second),
			KeepAlive:              Duration(30 * time.Second),
			TLSHandshakeTimeout:    Duration(5 * time.Second),
			ResponseHeaderTimeout:  Duration(10 * time.Second),
			MaxResponseHeaderBytes: 64 << 10,
			MaxIdleConns:           256,
			MaxIdleConnsPerHost:    32,
			MaxConnsPerHost:        128,
			IdleConnTimeout:        Duration(90 * time.Second),
			DisableCompression:     &disableCompression,
		},
		SLO:   SLOSettings{AcceptableP99Latency: Duration(250 * time.Millisecond)},
		Trust: TrustSettings{TLSLoadBalancerMode: TLSExternalLoadBalancer},
	}
}

// WithDefaults fills zero-valued settings so older configuration files remain
// valid while a new file can override any setting explicitly.
func (s Settings) WithDefaults() Settings {
	d := DefaultSettings()
	if s.Request.MaxBodyBytes == 0 {
		s.Request.MaxBodyBytes = d.Request.MaxBodyBytes
	}
	if s.Request.NormalDuration == 0 {
		s.Request.NormalDuration = d.Request.NormalDuration
	}
	if s.Request.ReadTimeout == 0 {
		s.Request.ReadTimeout = d.Request.ReadTimeout
	}
	if s.Request.MaximumDuration == 0 {
		s.Request.MaximumDuration = d.Request.MaximumDuration
	}
	if s.Server.ReadHeaderTimeout == 0 {
		s.Server.ReadHeaderTimeout = d.Server.ReadHeaderTimeout
	}
	if s.Server.IdleTimeout == 0 {
		s.Server.IdleTimeout = d.Server.IdleTimeout
	}
	if s.Server.MaxHeaderBytes == 0 {
		s.Server.MaxHeaderBytes = d.Server.MaxHeaderBytes
	}
	if s.Capacity.ExpectedConcurrency == 0 {
		s.Capacity.ExpectedConcurrency = d.Capacity.ExpectedConcurrency
	}
	if s.Capacity.ExpectedRPS == 0 {
		s.Capacity.ExpectedRPS = d.Capacity.ExpectedRPS
	}
	s.Backend = s.Backend.WithDefaults()
	if s.SLO.AcceptableP99Latency == 0 {
		s.SLO.AcceptableP99Latency = d.SLO.AcceptableP99Latency
	}
	if s.Trust.TLSLoadBalancerMode == "" {
		s.Trust.TLSLoadBalancerMode = d.Trust.TLSLoadBalancerMode
	}
	return s
}

func (s BackendSettings) WithDefaults() BackendSettings {
	d := DefaultSettings().Backend
	if s.ConnectTimeout == 0 {
		s.ConnectTimeout = d.ConnectTimeout
	}
	if s.KeepAlive == 0 {
		s.KeepAlive = d.KeepAlive
	}
	if s.TLSHandshakeTimeout == 0 {
		s.TLSHandshakeTimeout = d.TLSHandshakeTimeout
	}
	if s.ResponseHeaderTimeout == 0 {
		s.ResponseHeaderTimeout = d.ResponseHeaderTimeout
	}
	if s.MaxResponseHeaderBytes == 0 {
		s.MaxResponseHeaderBytes = d.MaxResponseHeaderBytes
	}
	if s.MaxIdleConns == 0 {
		s.MaxIdleConns = d.MaxIdleConns
	}
	if s.MaxIdleConnsPerHost == 0 {
		s.MaxIdleConnsPerHost = d.MaxIdleConnsPerHost
	}
	if s.MaxConnsPerHost == 0 {
		s.MaxConnsPerHost = d.MaxConnsPerHost
	}
	if s.IdleConnTimeout == 0 {
		s.IdleConnTimeout = d.IdleConnTimeout
	}
	if s.DisableCompression == nil {
		s.DisableCompression = d.DisableCompression
	}
	return s
}

func (s Settings) Validate() error {
	s = s.WithDefaults()
	if err := validateSize("request.max_body_bytes", s.Request.MaxBodyBytes, MinRequestBodyBytes, MaxRequestBodyBytes); err != nil {
		return err
	}
	if err := validateDuration("request.normal_duration", s.Request.NormalDuration); err != nil {
		return err
	}
	if err := validateDuration("request.read_timeout", s.Request.ReadTimeout); err != nil {
		return err
	}
	if err := validateDuration("request.maximum_duration", s.Request.MaximumDuration); err != nil {
		return err
	}
	if s.Request.MaximumDuration < s.Request.NormalDuration {
		return fmt.Errorf("request.maximum_duration must be at least request.normal_duration")
	}

	if err := validateDuration("server.read_header_timeout", s.Server.ReadHeaderTimeout); err != nil {
		return err
	}
	if err := validateDuration("server.idle_timeout", s.Server.IdleTimeout); err != nil {
		return err
	}
	if err := validateSize("server.max_header_bytes", s.Server.MaxHeaderBytes, MinHeaderBytes, MaxHeaderBytes); err != nil {
		return err
	}

	if s.Capacity.ExpectedConcurrency < MinExpectedConcurrency || s.Capacity.ExpectedConcurrency > MaxExpectedConcurrency {
		return fmt.Errorf("capacity.expected_concurrency must be between %d and %d", MinExpectedConcurrency, MaxExpectedConcurrency)
	}
	if s.Capacity.ExpectedRPS < MinExpectedRPS || s.Capacity.ExpectedRPS > MaxExpectedRPS {
		return fmt.Errorf("capacity.expected_rps must be between %d and %d", MinExpectedRPS, MaxExpectedRPS)
	}

	for name, value := range map[string]Duration{
		"backend.connect_timeout":         s.Backend.ConnectTimeout,
		"backend.keep_alive":              s.Backend.KeepAlive,
		"backend.tls_handshake_timeout":   s.Backend.TLSHandshakeTimeout,
		"backend.response_header_timeout": s.Backend.ResponseHeaderTimeout,
		"backend.idle_conn_timeout":       s.Backend.IdleConnTimeout,
	} {
		if err := validateDuration(name, value); err != nil {
			return err
		}
	}
	if err := validateSize("backend.max_response_header_bytes", s.Backend.MaxResponseHeaderBytes, MinHeaderBytes, MaxHeaderBytes); err != nil {
		return err
	}
	for name, value := range map[string]int{
		"backend.max_idle_conns":          s.Backend.MaxIdleConns,
		"backend.max_idle_conns_per_host": s.Backend.MaxIdleConnsPerHost,
		"backend.max_conns_per_host":      s.Backend.MaxConnsPerHost,
	} {
		if value < 1 || value > MaxExpectedConcurrency {
			return fmt.Errorf("%s must be between 1 and %d", name, MaxExpectedConcurrency)
		}
	}
	if s.Backend.MaxIdleConnsPerHost > s.Backend.MaxConnsPerHost {
		return fmt.Errorf("backend.max_idle_conns_per_host must not exceed backend.max_conns_per_host")
	}
	if err := validateDuration("slo.acceptable_p99_latency", s.SLO.AcceptableP99Latency); err != nil {
		return err
	}
	if s.SLO.AcceptableP99Latency > s.Request.NormalDuration {
		return fmt.Errorf("slo.acceptable_p99_latency must not exceed request.normal_duration")
	}

	if s.Trust.TLSLoadBalancerMode != TLSExternalLoadBalancer {
		return fmt.Errorf("trust.tls_load_balancer_mode must be %q", TLSExternalLoadBalancer)
	}
	for _, raw := range s.Trust.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(raw); err != nil {
			return fmt.Errorf("trust.trusted_proxy_cidrs contains invalid CIDR %q: %w", raw, err)
		}
	}
	return nil
}

func validateDuration(name string, value Duration) error {
	d := value.Duration()
	if d < MinSettingDuration || d > MaxSettingDuration {
		return fmt.Errorf("%s must be between %s and %s", name, MinSettingDuration, MaxSettingDuration)
	}
	return nil
}

func validateSize(name string, value, minimum, maximum int64) error {
	if value < minimum || value > maximum {
		return fmt.Errorf("%s must be between %d and %d bytes", name, minimum, maximum)
	}
	return nil
}
