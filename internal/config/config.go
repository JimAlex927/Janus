// Package config owns the file format and validates it before listeners open.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"janus/internal/rules"

	"golang.org/x/net/http/httpguts"
)

const (
	MaxConfigBytes   = 1 << 20
	MaxTLSAssetBytes = 1 << 20
)

type Config struct {
	Version        int                    `json:"version,omitempty"`
	Listen         string                 `json:"listen"`
	TrustedProxies []string               `json:"trusted_proxies,omitempty"`
	Limens         map[string]LimenConfig `json:"limens,omitempty"`
	Settings       Settings               `json:"settings"`
	Middlewares    map[string]Middleware  `json:"middlewares"`
	Services       map[string]Service     `json:"services"`
	Routes         []Route                `json:"routes"`
	Discovery      DiscoveryConfig        `json:"discovery,omitempty"`
}

const (
	CurrentConfigVersion = 1
	ProtocolHTTP1        = "http1"
	ProtocolHTTP2        = "http2"
	ProtocolH2C          = "h2c"
	ProtocolHTTP3        = "http3"
	MaxTrustedProxyCIDRs = 128
)

// LimenConfig describes one inbound protocol binding. HTTP/2 means TLS-backed
// h2; h2c is the explicit cleartext HTTP/2 alternative.
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
	// Client trust is startup-owned, including the contents of ClientCAFile.
	ClientAuth   string `json:"client_auth,omitempty"`
	ClientCAFile string `json:"client_ca_file,omitempty"`
}

const (
	ClientAuthNone             = "none"
	ClientAuthRequireAndVerify = "require_and_verify"
)

// ValidateClientAuth also protects direct Limen constructors that don't load
// a full Config. Never silently ignore a configured client trust bundle.
func (s TLSSettings) ValidateClientAuth() error {
	switch s.ClientAuth {
	case "", ClientAuthNone:
		if s.ClientCAFile != "" {
			return fmt.Errorf("client_ca_file requires client_auth=require_and_verify")
		}
	case ClientAuthRequireAndVerify:
		if strings.TrimSpace(s.ClientCAFile) == "" {
			return fmt.Errorf("client_auth=require_and_verify requires client_ca_file")
		}
	default:
		return fmt.Errorf("client_auth must be none or require_and_verify")
	}
	return nil
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
// Scope is optional for backwards compatibility: an empty scope means the
// definition may be attached to either a route or a service. New definitions
// should declare route or service explicitly.
type Middleware struct {
	Scope            string               `json:"scope,omitempty"`
	Buffer           *BufferSettings      `json:"buffer,omitempty"`
	BodyLimit        *BodyLimitSettings   `json:"body_limit,omitempty"`
	InFlight         *InFlightSettings    `json:"in_flight,omitempty"`
	Headers          *HeadersSettings     `json:"headers,omitempty"`
	CORS             *CORSSettings        `json:"cors,omitempty"`
	JWT              *JWTSettings         `json:"jwt,omitempty"`
	JWTClaimsHeaders *JWTSettings         `json:"jwt_claims_headers,omitempty"`
	ForwardAuth      *ForwardAuthSettings `json:"forward_auth,omitempty"`
	StripPrefix      *StripPrefixSettings `json:"strip_prefix,omitempty"`
	AddPrefix        *AddPrefixSettings   `json:"add_prefix,omitempty"`
	BasicAuth        *BasicAuthSettings   `json:"basic_auth,omitempty"`
	IPAllowList      *IPAllowListSettings `json:"ip_allowlist,omitempty"`
	RateLimit        *RateLimitSettings   `json:"rate_limit,omitempty"`
	Compress         *CompressSettings    `json:"compress,omitempty"`
}

const (
	MiddlewareScopeRoute   = "route"
	MiddlewareScopeService = "service"
)

func (m Middleware) AllowsScope(scope string) bool {
	if m.Scope != "" && m.Scope != scope {
		return false
	}
	kind := MiddlewareType(m)
	for _, capability := range MiddlewareCapabilities() {
		if capability.Type != kind {
			continue
		}
		for _, allowed := range capability.Scopes {
			if allowed == scope {
				return true
			}
		}
		return false
	}
	return false
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

type BasicAuthSettings struct {
	Realm        string            `json:"realm"`
	Users        map[string]string `json:"users"`
	RemoveHeader bool              `json:"remove_header,omitempty"`
}

type IPAllowListSettings struct {
	SourceRanges []string `json:"source_ranges"`
}

type RateLimitSettings struct {
	Average int      `json:"average"`
	Period  Duration `json:"period"`
	Burst   int      `json:"burst"`
	MaxKeys int      `json:"max_keys,omitempty"`
}

type CompressSettings struct{}

const (
	DefaultRateLimitAverage = 100
	DefaultRateLimitPeriod  = Duration(time.Second)
	DefaultRateLimitBurst   = 100
	DefaultRateLimitMaxKeys = 10000
	MaxRateLimitAverage     = 1_000_000
	MaxRateLimitBurst       = 1_000_000
	MaxRateLimitKeys        = 1_000_000
)

func (s RateLimitSettings) WithDefaults() RateLimitSettings {
	if s.Average == 0 {
		s.Average = DefaultRateLimitAverage
	}
	if s.Period == 0 {
		s.Period = DefaultRateLimitPeriod
	}
	if s.Burst == 0 {
		s.Burst = DefaultRateLimitBurst
	}
	if s.MaxKeys == 0 {
		s.MaxKeys = DefaultRateLimitMaxKeys
	}
	return s
}

// HeadersSettings mutates end-to-end headers around the selected handler.
// Hop-by-hop and framing headers are rejected during configuration validation.
type HeadersSettings struct {
	RequestSet     map[string]string `json:"request_set,omitempty"`
	RequestRemove  []string          `json:"request_remove,omitempty"`
	ResponseSet    map[string]string `json:"response_set,omitempty"`
	ResponseRemove []string          `json:"response_remove,omitempty"`
}

// CORSSettings controls browser cross-origin access. Origins are exact HTTP(S)
// origins or the single wildcard "*"; wildcard origins cannot be combined with
// credentialed requests. The policy is valid at route and service scope.
type CORSSettings struct {
	AllowOrigins     []string `json:"allow_origins"`
	AllowMethods     []string `json:"allow_methods"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	AllowCredentials bool     `json:"allow_credentials,omitempty"`
	MaxAgeSeconds    int      `json:"max_age_seconds,omitempty"`
}

const MaxCORSMaxAgeSeconds = 86400

// JWTSettings configures JWT authentication. Claims are forwarded only through
// an explicit ClaimHeaders allowlist. Exactly one key source is required.
type JWTSettings struct {
	KeySource           JWTKeySource      `json:"key_source"`
	Algorithms          []string          `json:"algorithms"`
	Issuer              string            `json:"issuer"`
	Audience            []string          `json:"audience"`
	RequiredClaims      []string          `json:"required_claims,omitempty"`
	ClaimHeaders        map[string]string `json:"claim_headers,omitempty"`
	RemoveAuthorization bool              `json:"remove_authorization,omitempty"`
	ClockSkew           Duration          `json:"clock_skew,omitempty"`
}

type JWTKeySource struct {
	JWKSURL       string `json:"jwks_url,omitempty"`
	PublicKeyFile string `json:"public_key_file,omitempty"`
	SecretEnv     string `json:"secret_env,omitempty"`
}

type ForwardAuthSettings struct {
	Address                  string   `json:"address"`
	AuthRequestHeaders       []string `json:"auth_request_headers,omitempty"`
	AuthResponseHeaders      []string `json:"auth_response_headers,omitempty"`
	AuthResponseHeadersRegex string   `json:"auth_response_headers_regex,omitempty"`
	HeaderField              string   `json:"header_field,omitempty"`
	ForwardBody              bool     `json:"forward_body,omitempty"`
	MaxBodyBytes             int64    `json:"max_body_bytes,omitempty"`
	MaxResponseBodyBytes     int64    `json:"max_response_body_bytes,omitempty"`
	PreserveRequestMethod    bool     `json:"preserve_request_method,omitempty"`
	Timeout                  Duration `json:"timeout,omitempty"`
}

func (s JWTSettings) WithDefaults() JWTSettings {
	if s.ClockSkew == 0 {
		s.ClockSkew = Duration(30 * time.Second)
	}
	s.Algorithms = append([]string(nil), s.Algorithms...)
	s.Audience = append([]string(nil), s.Audience...)
	s.RequiredClaims = append([]string(nil), s.RequiredClaims...)
	if s.ClaimHeaders != nil {
		s.ClaimHeaders = maps.Clone(s.ClaimHeaders)
	}
	return s
}

// StripPrefixSettings removes Prefix from a matching request path before it
// reaches the service. Janus emits the accumulated stripped prefix as the
// trusted X-Forwarded-Prefix header when it forwards the request upstream.
type StripPrefixSettings struct {
	Prefix string `json:"prefix"`
}

// AddPrefixSettings prepends Prefix to the request path before proxying.
type AddPrefixSettings struct {
	Prefix string `json:"prefix"`
}

type Service struct {
	// PassHostHeader preserves the incoming HTTP authority, including its port.
	// Backend dial address and TLS verification still use the selected upstream.
	PassHostHeader bool                 `json:"pass_host_header,omitempty"`
	Upstreams      []string             `json:"upstreams,omitempty"`
	Nacos          *NacosService        `json:"nacos,omitempty"`
	Middlewares    []string             `json:"middlewares"`
	HealthCheck    *HealthCheckSettings `json:"health_check,omitempty"`
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
	Name                       string                      `json:"name"`
	Limen                      string                      `json:"limen,omitempty"`
	Match                      string                      `json:"match,omitempty"`
	Priority                   int                         `json:"priority,omitempty"`
	Host                       string                      `json:"host,omitempty"`
	PathPrefix                 string                      `json:"path_prefix,omitempty"`
	Service                    string                      `json:"service,omitempty"`
	Middlewares                []string                    `json:"middlewares,omitempty"`
	BuiltinMiddlewareOverrides *BuiltinMiddlewareOverrides `json:"builtin_middleware_overrides,omitempty"`
	Action                     *RouteAction                `json:"action,omitempty"`
}

// BuiltinMiddlewareOverrides changes the parameters of Janus-owned middleware
// for one matched route. Missing values inherit the corresponding global
// setting; these definitions are not user middleware instances and therefore
// never appear in Route.Middlewares.
type BuiltinMiddlewareOverrides struct {
	Timeout       *TimeoutMiddlewareOverride       `json:"timeout,omitempty"`
	Admission     *AdmissionMiddlewareOverride     `json:"admission,omitempty"`
	StreamTimeout *StreamTimeoutMiddlewareOverride `json:"stream_timeout,omitempty"`
	WriteTimeout  *WriteTimeoutMiddlewareOverride  `json:"write_timeout,omitempty"`
}

type TimeoutMiddlewareOverride struct {
	MaximumDuration *Duration `json:"maximum_duration,omitempty"`
}

type AdmissionMiddlewareOverride struct {
	MaxInFlight *int `json:"max_in_flight,omitempty"`
}

type StreamTimeoutMiddlewareOverride struct {
	MaxDuration *Duration `json:"max_duration,omitempty"`
	IdleTimeout *Duration `json:"idle_timeout,omitempty"`
}

type WriteTimeoutMiddlewareOverride struct {
	Timeout *Duration `json:"timeout,omitempty"`
}

// BuiltinMiddlewareParameters is the fully resolved policy used by a route.
// It contains no optional values: every omitted override has already inherited
// its global setting.
type BuiltinMiddlewareParameters struct {
	MaximumDuration time.Duration
	MaxInFlight     int
	StreamMax       time.Duration
	StreamIdle      time.Duration
	WriteTimeout    time.Duration
}

func (r Route) EffectiveBuiltinMiddlewareParameters(settings Settings) BuiltinMiddlewareParameters {
	settings = settings.WithDefaults()
	result := BuiltinMiddlewareParameters{
		MaximumDuration: settings.Request.MaximumDuration.Duration(),
		MaxInFlight:     settings.Request.MaxInFlight,
		StreamMax:       settings.Stream.MaxDuration.Duration(),
		StreamIdle:      settings.Stream.IdleTimeout.Duration(),
		WriteTimeout:    settings.Server.WriteTimeout.Duration(),
	}
	overrides := r.BuiltinMiddlewareOverrides
	if overrides == nil {
		return result
	}
	if overrides.Timeout != nil && overrides.Timeout.MaximumDuration != nil {
		result.MaximumDuration = overrides.Timeout.MaximumDuration.Duration()
	}
	if overrides.Admission != nil && overrides.Admission.MaxInFlight != nil {
		result.MaxInFlight = *overrides.Admission.MaxInFlight
	}
	if overrides.StreamTimeout != nil {
		if overrides.StreamTimeout.MaxDuration != nil {
			result.StreamMax = overrides.StreamTimeout.MaxDuration.Duration()
		}
		if overrides.StreamTimeout.IdleTimeout != nil {
			result.StreamIdle = overrides.StreamTimeout.IdleTimeout.Duration()
		}
	}
	if overrides.WriteTimeout != nil && overrides.WriteTimeout.Timeout != nil {
		result.WriteTimeout = overrides.WriteTimeout.Timeout.Duration()
	}
	return result
}

func (r Route) RouteMaxInFlight() (int, bool) {
	if r.BuiltinMiddlewareOverrides == nil || r.BuiltinMiddlewareOverrides.Admission == nil ||
		r.BuiltinMiddlewareOverrides.Admission.MaxInFlight == nil {
		return 0, false
	}
	return *r.BuiltinMiddlewareOverrides.Admission.MaxInFlight, true
}

// RouteAction describes what happens after a route matches. Exactly one
// action is allowed when Action is present; an omitted action keeps the
// programmatic legacy forward path while new files should use forward.
type RouteAction struct {
	Forward  *ForwardAction  `json:"forward,omitempty"`
	Redirect *RedirectAction `json:"redirect,omitempty"`
	Respond  *RespondAction  `json:"respond,omitempty"`
	Static   *StaticAction   `json:"static,omitempty"`
}

type ForwardAction struct {
	Service string `json:"service"`
}

type RedirectAction struct {
	Status   int    `json:"status,omitempty"`
	Location string `json:"location"`
}

type RespondAction struct {
	Status  int               `json:"status,omitempty"`
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// StaticAction serves files from one explicitly configured local directory.
// Root is intentionally absolute so startup files, admin drafts and reloads
// all resolve the same filesystem location.
type StaticAction struct {
	Root             string `json:"root"`
	Index            string `json:"index,omitempty"`
	SPAFallback      bool   `json:"spa_fallback,omitempty"`
	DirectoryListing bool   `json:"directory_listing,omitempty"`
	CacheControl     string `json:"cache_control,omitempty"`
}

const DefaultStaticIndex = "index.html"

func (a StaticAction) WithDefaults() StaticAction {
	if a.Index == "" {
		a.Index = DefaultStaticIndex
	}
	return a
}

func Load(r io.Reader) (Config, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > MaxConfigBytes {
		return Config{}, fmt.Errorf("config exceeds %d bytes", MaxConfigBytes)
	}
	return loadBytes(data, "")
}

// LoadWithAdminPasswordHash loads a configuration submitted by the admin
// console. The console receives a redacted password_hash, so an empty value
// may mean "keep the currently active hash". The normal Load path remains
// strict; callers must explicitly provide the hash they want to inherit.
func LoadWithAdminPasswordHash(r io.Reader, inheritedHash string) (Config, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > MaxConfigBytes {
		return Config{}, fmt.Errorf("config exceeds %d bytes", MaxConfigBytes)
	}
	return loadBytes(data, inheritedHash)
}

// LoadWithAdminSecrets is the admin-console variant that also restores
// redacted Nacos credentials from the currently active configuration.
func LoadWithAdminSecrets(r io.Reader, inheritedHash string, previous DiscoveryConfig) (Config, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > MaxConfigBytes {
		return Config{}, fmt.Errorf("config exceeds %d bytes", MaxConfigBytes)
	}
	return loadBytesWithSecrets(data, inheritedHash, previous)
}

func loadBytes(data []byte, inheritedHash string) (Config, error) {
	return loadBytesWithSecrets(data, inheritedHash, DiscoveryConfig{})
}

func loadBytesWithSecrets(data []byte, inheritedHash string, previous DiscoveryConfig) (Config, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var c Config
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("config must contain exactly one JSON object")
	}
	if inheritedHash != "" && c.Settings.Admin.PasswordHash == "" {
		c.Settings.Admin.PasswordHash = inheritedHash
	}
	if len(previous.Nacos) > 0 {
		c.Discovery = c.Discovery.InheritSecrets(previous)
	}
	c = c.WithDefaults()
	return c, c.Validate()
}

// rejectDuplicateJSONKeys makes configuration interpretation deterministic.
// encoding/json otherwise accepts duplicate object members and keeps the last
// value, which can make a reviewed configuration differ from the effective
// configuration. This scanner only checks object keys; normal decoding below
// remains responsible for types, unknown fields and validation.
func rejectDuplicateJSONKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(d, "$"); err != nil {
		return err
	}
	return nil
}

func scanJSONValue(d *json.Decoder, path string) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, ok := seen[key]; ok {
				return fmt.Errorf("duplicate object key %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(d, path+"."+key); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("object at %s did not terminate", path)
		}
	case '[':
		index := 0
		for d.More() {
			if err := scanJSONValue(d, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		end, err := d.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("array at %s did not terminate", path)
		}
	}
	return nil
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
	//继续还是反序列化 规范化配置 变成config对象 hash是配置文件的签名
	c, err := LoadFileBytes(path, data)
	return c, hash, err
}

// LoadFileBytes parses configuration bytes and resolves relative TLS asset
// paths against the supplied configuration file path. It is used by the
// watcher so parsing and hashing operate on the same atomic-replacement read.
func LoadFileBytes(path string, data []byte) (Config, error) {
	//反序列化配置文件 变成config.Config对象
	c, err := Load(bytes.NewReader(data))
	if err != nil {
		return c, err
	}
	base := filepath.Dir(path)
	for name, binding := range c.Limens {
		if binding.TLS == nil {
			continue
		}
		//这里是规范binding的 tls的证书和keyfile的文件路径 标准化成绝对路径
		if !filepath.IsAbs(binding.TLS.CertFile) {
			binding.TLS.CertFile = filepath.Join(base, binding.TLS.CertFile)
		}
		if !filepath.IsAbs(binding.TLS.KeyFile) {
			binding.TLS.KeyFile = filepath.Join(base, binding.TLS.KeyFile)
		}
		if binding.TLS.ClientCAFile != "" && !filepath.IsAbs(binding.TLS.ClientCAFile) {
			binding.TLS.ClientCAFile = filepath.Join(base, binding.TLS.ClientCAFile)
		}
		// Limens own transport protocols (HTTP/1, TLS HTTP/2, and HTTP/3).
		// Routes select application request shapes such as HTTP, SSE, or the
		// supported classic HTTP/1 WebSocket upgrade independently of that choice.
		c.Limens[name] = binding
	}
	return c, nil
}

func (c Config) Validate() error {
	if err := c.validateDiscovery(); err != nil {
		return err
	}
	bindings, err := c.validateLimenBindings()
	if err != nil {
		return err
	}
	settings := c.Settings.WithDefaults()
	if err := settings.Validate(); err != nil {
		return err
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}
	if len(c.Services) == 0 {
		for _, route := range c.Routes {
			if serviceName, err := routeServiceName(route); err != nil || serviceName != "" {
				if err != nil {
					return err
				}
				return fmt.Errorf("route %q requires a service", route.Name)
			}
		}
	}
	for name, s := range c.Services {
		if name == "" || (len(s.Upstreams) == 0) == (s.Nacos == nil) {
			return fmt.Errorf("service %q needs a name and exactly one upstream source: upstreams or nacos", name)
		}
		if s.Nacos != nil {
			if err := c.validateNacosService(name, s); err != nil {
				return err
			}
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
			definition := c.Middlewares[middlewareName]
			if !definition.AllowsScope(MiddlewareScopeService) {
				return fmt.Errorf("service %q cannot use middleware %q in service scope", name, middlewareName)
			}
			if definition.InFlight != nil {
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
		serviceName, err := routeServiceName(r)
		if err != nil {
			return fmt.Errorf("route %q: %w", r.Name, err)
		}
		if serviceName != "" {
			if _, ok := c.Services[serviceName]; !ok {
				return fmt.Errorf("route %q references missing service %q", r.Name, serviceName)
			}
		}
		if r.Limen == "" {
			if len(bindings) > 1 {
				return fmt.Errorf("route %q must reference a limen when multiple limens are configured", r.Name)
			}
		} else if _, ok := bindings[r.Limen]; !ok {
			return fmt.Errorf("route %q references missing limen %q", r.Name, r.Limen)
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
			definition := c.Middlewares[middlewareName]
			if !definition.AllowsScope(MiddlewareScopeRoute) {
				return fmt.Errorf("route %q cannot use middleware %q in route scope", r.Name, middlewareName)
			}
		}
		if err := validateBuiltinMiddlewareOverrides(r, settings); err != nil {
			return fmt.Errorf("route %q: %w", r.Name, err)
		}
		if r.Match != "" {
			if r.Host != "" || r.PathPrefix != "" {
				return fmt.Errorf("route %q cannot combine match with host or path_prefix", r.Name)
			}
			if _, err := rules.DefaultRegistry().Compile(r.Match); err != nil {
				return fmt.Errorf("route %q match: %w", r.Name, err)
			}
		} else {
			if !strings.HasPrefix(r.PathPrefix, "/") || strings.ContainsAny(r.PathPrefix, "?#%\\ \t\r\n") {
				return fmt.Errorf("route %q needs an unescaped absolute path prefix", r.Name)
			}
			if r.PathPrefix != "/" && strings.HasSuffix(r.PathPrefix, "/") {
				return fmt.Errorf("route %q: omit trailing slash from path_prefix", r.Name)
			}
			// Exact DNS names only in the original structured route fields.
			if strings.ContainsAny(r.Host, ":/*?#@\\ \t\r\n") {
				return fmt.Errorf("route %q host must be an exact hostname without port", r.Name)
			}
		}
		scope := r.Limen
		if scope == "" && len(bindings) == 1 {
			for name := range bindings {
				scope = name
			}
		}
		key := scope + "\x00" + r.Match + "\x00" + strings.ToLower(r.Host) + "\x00" + r.PathPrefix
		if matches[key] {
			return fmt.Errorf("duplicate host/path match on route %q", r.Name)
		}
		matches[key] = true
	}
	for name, definition := range c.Middlewares {
		if name == "" {
			return fmt.Errorf("middleware names must be nonempty")
		}
		if definition.Scope != "" && definition.Scope != MiddlewareScopeRoute && definition.Scope != MiddlewareScopeService {
			return fmt.Errorf("middleware %q has unsupported scope %q", name, definition.Scope)
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
		if definition.Headers != nil {
			defined++
		}
		if definition.CORS != nil {
			defined++
		}
		if definition.JWT != nil {
			defined++
		}
		if definition.JWTClaimsHeaders != nil {
			defined++
		}
		if definition.ForwardAuth != nil {
			defined++
		}
		if definition.StripPrefix != nil {
			defined++
		}
		if definition.AddPrefix != nil {
			defined++
		}
		if definition.BasicAuth != nil {
			defined++
		}
		if definition.IPAllowList != nil {
			defined++
		}
		if definition.RateLimit != nil {
			defined++
		}
		if definition.Compress != nil {
			defined++
		}
		if defined != 1 {
			return fmt.Errorf("middleware %q must define exactly one policy", name)
		}
		if definition.Scope != "" && !definition.AllowsScope(definition.Scope) {
			return fmt.Errorf("middleware %q type %q cannot use scope %q", name, MiddlewareType(definition), definition.Scope)
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
		if definition.Headers != nil {
			if err := validateHeaderSettings(*definition.Headers); err != nil {
				return fmt.Errorf("middleware %q headers: %w", name, err)
			}
		}
		if definition.CORS != nil {
			if err := validateCORSSettings(*definition.CORS); err != nil {
				return fmt.Errorf("middleware %q cors: %w", name, err)
			}
		}
		if definition.JWT != nil {
			if err := validateJWTSettings(*definition.JWT); err != nil {
				return fmt.Errorf("middleware %q jwt: %w", name, err)
			}
		}
		if definition.JWTClaimsHeaders != nil {
			settings := *definition.JWTClaimsHeaders
			if len(settings.ClaimHeaders) == 0 {
				return fmt.Errorf("middleware %q jwt_claims_headers requires claim_headers", name)
			}
			if err := validateJWTSettings(settings); err != nil {
				return fmt.Errorf("middleware %q jwt_claims_headers: %w", name, err)
			}
		}
		if definition.ForwardAuth != nil {
			if err := validateForwardAuthSettings(*definition.ForwardAuth); err != nil {
				return fmt.Errorf("middleware %q forward_auth: %w", name, err)
			}
		}
		if definition.StripPrefix != nil {
			if err := validateMiddlewarePathPrefix(name, "strip_prefix", definition.StripPrefix.Prefix); err != nil {
				return err
			}
		}
		if definition.AddPrefix != nil {
			if err := validateMiddlewarePathPrefix(name, "add_prefix", definition.AddPrefix.Prefix); err != nil {
				return err
			}
		}
		if definition.BasicAuth != nil {
			if err := validateBasicAuthSettings(*definition.BasicAuth); err != nil {
				return fmt.Errorf("middleware %q basic_auth: %w", name, err)
			}
		}
		if definition.IPAllowList != nil {
			if err := validateIPAllowListSettings(*definition.IPAllowList); err != nil {
				return fmt.Errorf("middleware %q ip_allowlist: %w", name, err)
			}
		}
		if definition.RateLimit != nil {
			if err := validateRateLimitSettings(definition.RateLimit.WithDefaults()); err != nil {
				return fmt.Errorf("middleware %q rate_limit: %w", name, err)
			}
		}
	}
	return nil
}

func validateBuiltinMiddlewareOverrides(route Route, settings Settings) error {
	overrides := route.BuiltinMiddlewareOverrides
	if overrides == nil {
		return nil
	}
	if overrides.Timeout != nil && overrides.Timeout.MaximumDuration != nil {
		if err := validateDuration("builtin_middleware_overrides.timeout.maximum_duration", *overrides.Timeout.MaximumDuration); err != nil {
			return err
		}
	}
	if overrides.Admission != nil && overrides.Admission.MaxInFlight != nil {
		value := *overrides.Admission.MaxInFlight
		if value < 1 || value > MaxGlobalInFlight {
			return fmt.Errorf("builtin_middleware_overrides.admission.max_in_flight must be between 1 and %d", MaxGlobalInFlight)
		}
		if value > settings.Request.MaxInFlight {
			return fmt.Errorf("builtin_middleware_overrides.admission.max_in_flight must not exceed global request.max_in_flight")
		}
	}
	if overrides.StreamTimeout != nil {
		if overrides.StreamTimeout.MaxDuration != nil {
			if err := validateDuration("builtin_middleware_overrides.stream_timeout.max_duration", *overrides.StreamTimeout.MaxDuration); err != nil {
				return err
			}
		}
		if overrides.StreamTimeout.IdleTimeout != nil {
			if err := validateDuration("builtin_middleware_overrides.stream_timeout.idle_timeout", *overrides.StreamTimeout.IdleTimeout); err != nil {
				return err
			}
		}
	}
	if overrides.WriteTimeout != nil && overrides.WriteTimeout.Timeout != nil {
		if err := validateDurationBound("builtin_middleware_overrides.write_timeout.timeout", *overrides.WriteTimeout.Timeout, MaxServerWriteTimeout); err != nil {
			return err
		}
	}

	effective := route.EffectiveBuiltinMiddlewareParameters(settings)
	if effective.StreamIdle > effective.StreamMax {
		return fmt.Errorf("effective stream_timeout.idle_timeout must not exceed stream_timeout.max_duration")
	}
	if effective.WriteTimeout <= effective.MaximumDuration {
		return fmt.Errorf("effective write_timeout.timeout must exceed timeout.maximum_duration")
	}
	if effective.WriteTimeout > settings.Shutdown.DrainTimeout.Duration() {
		return fmt.Errorf("effective write_timeout.timeout must not exceed shutdown.drain_timeout")
	}
	return nil
}

func validateBasicAuthSettings(settings BasicAuthSettings) error {
	if strings.TrimSpace(settings.Realm) == "" || strings.ContainsAny(settings.Realm, "\r\n") {
		return errors.New("realm is required")
	}
	if len(settings.Users) == 0 || len(settings.Users) > 256 {
		return errors.New("users must contain 1 to 256 entries")
	}
	for username, hash := range settings.Users {
		if username == "" || strings.ContainsAny(username, "\r\n:") || strings.TrimSpace(hash) == "" {
			return errors.New("usernames and bcrypt hashes are invalid")
		}
	}
	return nil
}

func validateIPAllowListSettings(settings IPAllowListSettings) error {
	if len(settings.SourceRanges) == 0 || len(settings.SourceRanges) > MaxTrustedProxyCIDRs {
		return fmt.Errorf("source_ranges must contain 1 to %d CIDRs", MaxTrustedProxyCIDRs)
	}
	for _, raw := range settings.SourceRanges {
		if _, _, err := net.ParseCIDR(raw); err != nil {
			return fmt.Errorf("invalid source range %q", raw)
		}
	}
	return nil
}

func validateRateLimitSettings(settings RateLimitSettings) error {
	if settings.Average < 1 || settings.Average > MaxRateLimitAverage {
		return fmt.Errorf("average must be between 1 and %d", MaxRateLimitAverage)
	}
	if settings.Period.Duration() <= 0 || settings.Period.Duration() > time.Hour {
		return errors.New("period must be between 1ns and 1h")
	}
	if settings.Burst < 1 || settings.Burst > MaxRateLimitBurst {
		return fmt.Errorf("burst must be between 1 and %d", MaxRateLimitBurst)
	}
	if settings.MaxKeys < 1 || settings.MaxKeys > MaxRateLimitKeys {
		return fmt.Errorf("max_keys must be between 1 and %d", MaxRateLimitKeys)
	}
	return nil
}

var jwtAlgorithms = map[string]struct{}{
	"RS256": {}, "RS384": {}, "RS512": {}, "PS256": {}, "PS384": {}, "PS512": {},
	"ES256": {}, "ES384": {}, "ES512": {}, "EdDSA": {}, "HS256": {}, "HS384": {}, "HS512": {},
}

func validateJWTSettings(settings JWTSettings) error {
	if len(settings.Algorithms) == 0 || len(settings.Algorithms) > 8 {
		return errors.New("algorithms must contain between 1 and 8 values")
	}
	seen := map[string]bool{}
	for _, algorithm := range settings.Algorithms {
		if _, ok := jwtAlgorithms[algorithm]; !ok {
			return fmt.Errorf("unsupported algorithm %q", algorithm)
		}
		if seen[algorithm] {
			return fmt.Errorf("algorithm %q is duplicated", algorithm)
		}
		seen[algorithm] = true
	}
	if strings.TrimSpace(settings.Issuer) == "" || strings.ContainsAny(settings.Issuer, "\x00\r\n") {
		return errors.New("issuer is required")
	}
	if len(settings.Audience) == 0 || len(settings.Audience) > 16 {
		return errors.New("audience must contain between 1 and 16 values")
	}
	for _, value := range append(append([]string(nil), settings.Audience...), settings.RequiredClaims...) {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("audience and claim names must be nonempty and contain no control characters")
		}
	}
	if len(settings.RequiredClaims) > 16 {
		return errors.New("required_claims cannot contain more than 16 values")
	}
	claims := map[string]bool{}
	for _, claim := range settings.RequiredClaims {
		if claims[claim] {
			return fmt.Errorf("required claim %q is duplicated", claim)
		}
		claims[claim] = true
	}
	if err := validateJWTClaimHeaders(settings.ClaimHeaders); err != nil {
		return err
	}
	if settings.ClockSkew.Duration() < 0 || settings.ClockSkew.Duration() > 5*time.Minute {
		return errors.New("clock_skew must be between 0 and 5m")
	}
	sources := 0
	if settings.KeySource.JWKSURL != "" {
		sources++
	}
	if settings.KeySource.PublicKeyFile != "" {
		sources++
	}
	if settings.KeySource.SecretEnv != "" {
		sources++
	}
	if sources != 1 {
		return errors.New("key_source must configure exactly one of jwks_url, public_key_file or secret_env")
	}
	if settings.KeySource.JWKSURL != "" {
		u, err := url.Parse(settings.KeySource.JWKSURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("jwks_url must be an HTTPS URL without credentials, query or fragment")
		}
	}
	if settings.KeySource.SecretEnv != "" {
		if !environmentName.MatchString(settings.KeySource.SecretEnv) {
			return errors.New("secret_env is not a valid environment variable name")
		}
		for _, algorithm := range settings.Algorithms {
			if !strings.HasPrefix(algorithm, "HS") {
				return errors.New("secret_env only supports HS algorithms")
			}
		}
	} else {
		for _, algorithm := range settings.Algorithms {
			if strings.HasPrefix(algorithm, "HS") {
				return errors.New("HS algorithms require secret_env")
			}
		}
	}
	return nil
}

func validateJWTClaimHeaders(headers map[string]string) error {
	if len(headers) > 32 {
		return errors.New("claim_headers cannot contain more than 32 mappings")
	}
	for header, claim := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(header))
		if canonical == "" || strings.TrimSpace(header) != header || !httpguts.ValidHeaderFieldName(header) {
			return fmt.Errorf("claim header name %q is invalid", header)
		}
		if _, forbidden := forbiddenMiddlewareHeaders[canonical]; forbidden || canonical == "Authorization" || canonical == "Cookie" || strings.HasPrefix(canonical, "X-Forwarded-") {
			return fmt.Errorf("claim header %q is reserved", canonical)
		}
		if strings.TrimSpace(claim) == "" || strings.ContainsAny(claim, "\x00\r\n") || len(claim) > 128 {
			return fmt.Errorf("claim name for header %q is invalid", canonical)
		}
	}
	return nil
}

func validateForwardAuthSettings(settings ForwardAuthSettings) error {
	u, err := url.Parse(settings.Address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("address must be an http(s) URL without credentials")
	}
	if settings.Timeout.Duration() < 0 || settings.Timeout.Duration() > time.Minute {
		return errors.New("timeout must be between 0 and 1m")
	}
	if settings.MaxBodyBytes < 0 || settings.MaxBodyBytes > 64<<20 {
		return errors.New("max_body_bytes must be between 0 and 64MiB")
	}
	if settings.MaxResponseBodyBytes < 0 || settings.MaxResponseBodyBytes > 1<<20 {
		return errors.New("max_response_body_bytes must be between 0 and 1MiB")
	}
	for _, name := range settings.AuthRequestHeaders {
		if !validForwardAuthRequestHeader(name) {
			return fmt.Errorf("auth_request_headers contains invalid header %q", name)
		}
	}
	for _, name := range append(append([]string{}, settings.AuthResponseHeaders...), []string{settings.HeaderField}...) {
		if name != "" && !validJWTClaimHeaderName(name) {
			return fmt.Errorf("response header %q is invalid or reserved", name)
		}
	}
	if settings.AuthResponseHeadersRegex != "" {
		if _, err := regexp.Compile(settings.AuthResponseHeadersRegex); err != nil {
			return fmt.Errorf("auth_response_headers_regex: %w", err)
		}
	}
	return nil
}

func validForwardAuthRequestHeader(name string) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
	return strings.TrimSpace(name) == name && name != "" && httpguts.ValidHeaderFieldName(name) && !strings.HasPrefix(canonical, "X-Forwarded-")
}

func validJWTClaimHeaderName(name string) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
	if canonical == "" || strings.TrimSpace(name) != name || !httpguts.ValidHeaderFieldName(name) {
		return false
	}
	if canonical == "Authorization" || canonical == "Cookie" || strings.HasPrefix(canonical, "X-Forwarded-") {
		return false
	}
	_, forbidden := forbiddenMiddlewareHeaders[canonical]
	return !forbidden
}

func validateMiddlewarePathPrefix(name, kind, prefix string) error {
	if prefix == "" || prefix == "/" || !strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") || strings.ContainsAny(prefix, "?#\\ \t\r\n") {
		return fmt.Errorf("middleware %q %s.prefix must be a non-root absolute path prefix without a trailing slash", name, kind)
	}
	return nil
}

var forbiddenMiddlewareHeaders = map[string]struct{}{
	"Connection": {}, "Content-Length": {}, "Host": {}, "Keep-Alive": {},
	"Proxy-Authenticate": {}, "Proxy-Authorization": {}, "Proxy-Connection": {},
	"Te": {}, "Trailer": {}, "Transfer-Encoding": {}, "Upgrade": {},
}

func validateHeaderSettings(settings HeadersSettings) error {
	validateName := func(name string) error {
		trimmed := strings.TrimSpace(name)
		canonical := http.CanonicalHeaderKey(trimmed)
		if canonical == "" || trimmed != name || !httpguts.ValidHeaderFieldName(trimmed) {
			return fmt.Errorf("invalid header name %q", name)
		}
		if _, forbidden := forbiddenMiddlewareHeaders[canonical]; forbidden {
			return fmt.Errorf("header %q is hop-by-hop, framing, or managed separately", canonical)
		}
		return nil
	}
	for _, set := range []map[string]string{settings.RequestSet, settings.ResponseSet} {
		for name, value := range set {
			if err := validateName(name); err != nil {
				return err
			}
			if !httpguts.ValidHeaderFieldValue(value) {
				return fmt.Errorf("header %q value contains a newline", name)
			}
		}
	}
	for _, remove := range [][]string{settings.RequestRemove, settings.ResponseRemove} {
		seen := make(map[string]struct{}, len(remove))
		for _, name := range remove {
			if err := validateName(name); err != nil {
				return err
			}
			canonical := http.CanonicalHeaderKey(name)
			if _, duplicate := seen[canonical]; duplicate {
				return fmt.Errorf("header %q is listed more than once", canonical)
			}
			seen[canonical] = struct{}{}
		}
	}
	return nil
}

func routeServiceName(route Route) (string, error) {
	if route.Action == nil {
		return route.Service, nil
	}
	defined := 0
	if route.Action.Forward != nil {
		defined++
	}
	if route.Action.Redirect != nil {
		defined++
	}
	if route.Action.Respond != nil {
		defined++
	}
	if route.Action.Static != nil {
		defined++
	}
	if defined != 1 {
		return "", fmt.Errorf("action must define exactly one of forward, redirect, respond, or static")
	}
	if route.Action.Forward != nil {
		if route.Action.Forward.Service == "" {
			return "", fmt.Errorf("action.forward.service cannot be empty")
		}
		if route.Service != "" {
			return "", fmt.Errorf("service cannot be combined with action.forward")
		}
		return route.Action.Forward.Service, nil
	}
	if route.Service != "" {
		return "", fmt.Errorf("service cannot be combined with a direct route action")
	}
	if route.Action.Redirect != nil {
		if route.Action.Redirect.Location == "" {
			return "", fmt.Errorf("action.redirect.location cannot be empty")
		}
		if route.Action.Redirect.Status != 0 && (route.Action.Redirect.Status < 300 || route.Action.Redirect.Status > 399) {
			return "", fmt.Errorf("action.redirect.status must be between 300 and 399")
		}
	}
	if route.Action.Respond != nil && route.Action.Respond.Status != 0 && (route.Action.Respond.Status < 100 || route.Action.Respond.Status > 599) {
		return "", fmt.Errorf("action.respond.status must be between 100 and 599")
	}
	if route.Action.Static != nil {
		static := route.Action.Static.WithDefaults()
		if err := validateStaticAction(static); err != nil {
			return "", err
		}
	}
	return "", nil
}

func validateStaticAction(action StaticAction) error {
	if action.Root == "" || !filepath.IsAbs(action.Root) || strings.ContainsRune(action.Root, '\x00') {
		return fmt.Errorf("action.static.root must be a non-empty absolute path")
	}
	cleanRoot := filepath.Clean(action.Root)
	// On Windows, Clean uses backslashes even for an otherwise clean path with forward slashes.
	if cleanRoot != filepath.FromSlash(action.Root) {
		return fmt.Errorf("action.static.root must be cleaned")
	}
	index := filepath.ToSlash(action.Index)
	if index == "" || index == "." || strings.HasPrefix(index, "/") || strings.ContainsRune(index, '\x00') {
		return fmt.Errorf("action.static.index must be a relative file path")
	}
	for _, part := range strings.Split(index, "/") {
		if part == ".." || part == "" {
			return fmt.Errorf("action.static.index must stay within root")
		}
	}
	if strings.ContainsAny(action.CacheControl, "\x00\r\n") || len(action.CacheControl) > 1024 {
		return fmt.Errorf("action.static.cache_control must be a valid bounded header value")
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
			if protocol != ProtocolHTTP1 && protocol != ProtocolHTTP2 && protocol != ProtocolH2C && protocol != ProtocolHTTP3 {
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
		if seen[ProtocolH2C] && binding.TLS != nil {
			return nil, fmt.Errorf("limen %q: h2c cannot be combined with TLS", name)
		}
		if seen[ProtocolH2C] && seen[ProtocolHTTP2] {
			return nil, fmt.Errorf("limen %q: h2c and HTTP/2 cannot be enabled together", name)
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
	if err := settings.ValidateClientAuth(); err != nil {
		return fmt.Errorf("limen %q TLS: %w", name, err)
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
	c.Settings = c.Settings.WithDefaults() // 这里是配置进行一个矫正 没写的配置填充默认值的作用
	c.Discovery = c.Discovery.WithDefaults()
	if c.Middlewares != nil {
		middlewares := make(map[string]Middleware, len(c.Middlewares))
		for name, definition := range c.Middlewares {
			if definition.JWT != nil {
				jwt := definition.JWT.WithDefaults()
				definition.JWT = &jwt
			}
			if definition.JWTClaimsHeaders != nil {
				jwt := definition.JWTClaimsHeaders.WithDefaults()
				definition.JWTClaimsHeaders = &jwt
			}
			if definition.RateLimit != nil {
				rate := definition.RateLimit.WithDefaults()
				definition.RateLimit = &rate
			}
			middlewares[name] = definition
		}
		c.Middlewares = middlewares
	}
	if len(c.Services) > 0 {
		services := make(map[string]Service, len(c.Services))
		for name, service := range c.Services {
			if service.Nacos != nil {
				source := service.Nacos.WithDefaults()
				service.Nacos = &source
			}
			if service.HealthCheck != nil {
				check := service.HealthCheck.WithDefaults()
				service.HealthCheck = &check
			}
			services[name] = service
		}
		c.Services = services
	}
	if len(c.Routes) > 0 {
		routes := make([]Route, len(c.Routes))
		for index, route := range c.Routes {
			if route.Action != nil && route.Action.Static != nil {
				static := route.Action.Static.WithDefaults()
				action := *route.Action
				action.Static = &static
				route.Action = &action
			}
			routes[index] = route
		}
		c.Routes = routes
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
