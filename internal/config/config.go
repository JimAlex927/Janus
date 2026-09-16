// Package config owns the file format and validates it before listeners open.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const MaxConfigBytes = 1 << 20

type Config struct {
	Version     int                    `json:"version,omitempty"`
	Listen      string                 `json:"listen"`
	Limens      map[string]LimenConfig `json:"limens,omitempty"`
	Settings    Settings               `json:"settings"`
	Middlewares map[string]Middleware  `json:"middlewares"`
	Services    map[string]Service     `json:"services"`
	Routes      []Route                `json:"routes"`
}

const (
	CurrentConfigVersion = 1
	ProtocolHTTP1        = "http1"
	ProtocolHTTP2        = "http2"
)

// LimenConfig describes one inbound protocol binding. HTTP/2 is enabled only
// over TLS in the first native multi-protocol profile.
type LimenConfig struct {
	Address   string       `json:"address"`
	Protocols []string     `json:"protocols"`
	TLS       *TLSSettings `json:"tls,omitempty"`
}

type TLSSettings struct {
	CertFile   string `json:"cert_file"`
	KeyFile    string `json:"key_file"`
	MinVersion string `json:"min_version,omitempty"`
}

// Middleware is a named, typed route middleware definition.
// Exactly one policy is currently supported per definition.
type Middleware struct {
	Buffer *BufferSettings `json:"buffer,omitempty"`
}

type BufferSettings struct {
	MaxResponseBodyBytes int64 `json:"max_response_body_bytes"`
}

type Service struct {
	Upstreams []string `json:"upstreams"`
}

type Route struct {
	Name        string   `json:"name"`
	Limen       string   `json:"limen,omitempty"`
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
		if definition.Buffer == nil {
			return fmt.Errorf("middleware %q must define buffer", name)
		}
		if definition.Buffer.MaxResponseBodyBytes < 1 || definition.Buffer.MaxResponseBodyBytes > MaxBufferedResponseBytes {
			return fmt.Errorf("middleware %q buffer.max_response_body_bytes must be between 1 and %d bytes", name, MaxBufferedResponseBytes)
		}
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
		return map[string]LimenConfig{
			"default": {Address: c.Listen, Protocols: []string{ProtocolHTTP1}},
		}, nil
	}
	if c.Version != CurrentConfigVersion {
		return nil, fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Listen != "" {
		return nil, fmt.Errorf("listen cannot be combined with versioned limens")
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
			if protocol != ProtocolHTTP1 && protocol != ProtocolHTTP2 {
				return nil, fmt.Errorf("limen %q has unsupported protocol %q", name, protocol)
			}
			if seen[protocol] {
				return nil, fmt.Errorf("limen %q enables protocol %q more than once", name, protocol)
			}
			seen[protocol] = true
		}
		if seen[ProtocolHTTP2] && binding.TLS == nil {
			return nil, fmt.Errorf("limen %q: HTTP/2 requires TLS", name)
		}
		if err := validateTLSSettings(name, binding.TLS); err != nil {
			return nil, err
		}
		binding.Protocols = append([]string(nil), binding.Protocols...)
		bindings[name] = binding
	}
	return bindings, nil
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
