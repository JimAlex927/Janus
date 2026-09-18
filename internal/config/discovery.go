package config

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Each named connection owns one namespace and one credential set. Services
// reference the name; multiple names may point to the same Nacos cluster.
type DiscoveryConfig struct {
	Nacos map[string]NacosRegistry `json:"nacos,omitempty"`
}

type NacosRegistry struct {
	Servers     []NacosServer `json:"servers"`
	NamespaceID string        `json:"namespace_id,omitempty"`
	Username    string        `json:"username,omitempty"`
	Password    string        `json:"password,omitempty"`
	PasswordEnv string        `json:"password_env,omitempty"`
	Timeout     Duration      `json:"timeout,omitempty"`
	StaleAfter  Duration      `json:"stale_after,omitempty"`
}

type NacosServer struct {
	Address  string `json:"address"`
	Port     uint64 `json:"port"`
	GRPCPort uint64 `json:"grpc_port,omitempty"`
}

type NacosService struct {
	Registry    string   `json:"registry"`
	ServiceName string   `json:"service_name"`
	GroupName   string   `json:"group_name,omitempty"`
	Clusters    []string `json:"clusters,omitempty"`
	Scheme      string   `json:"scheme,omitempty"`
}

func (r NacosRegistry) WithDefaults() NacosRegistry {
	r.Servers = append([]NacosServer(nil), r.Servers...)
	if r.NamespaceID == "public" {
		r.NamespaceID = ""
	}
	if r.Timeout == 0 {
		r.Timeout = Duration(5 * time.Second)
	}
	if r.StaleAfter == 0 {
		r.StaleAfter = Duration(2 * time.Minute)
	}
	return r
}

func (s NacosService) WithDefaults() NacosService {
	if s.GroupName == "" {
		s.GroupName = "DEFAULT_GROUP"
	}
	if s.Scheme == "" {
		s.Scheme = "http"
	}
	s.Clusters = append([]string(nil), s.Clusters...)
	sort.Strings(s.Clusters)
	return s
}

func (d DiscoveryConfig) WithDefaults() DiscoveryConfig {
	if d.Nacos == nil {
		return d
	}
	result := DiscoveryConfig{Nacos: make(map[string]NacosRegistry, len(d.Nacos))}
	for name, registry := range d.Nacos {
		result.Nacos[name] = registry.WithDefaults()
	}
	return result
}

// Redacted returns a copy safe for effective-config and Admin responses.
// Passwords are intentionally never returned to operators after parsing.
func (d DiscoveryConfig) Redacted() DiscoveryConfig {
	result := d.WithDefaults()
	for name, registry := range result.Nacos {
		registry.Password = ""
		result.Nacos[name] = registry
	}
	return result
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (c Config) validateDiscovery() error {
	for name, raw := range c.Discovery.Nacos {
		r := raw.WithDefaults()
		if strings.TrimSpace(name) == "" || len(r.Servers) == 0 || len(r.Servers) > 16 {
			return fmt.Errorf("nacos registry %q needs a name and 1..16 servers", name)
		}
		for _, server := range r.Servers {
			host := server.Address
			if host == "" || strings.ContainsAny(host, "/?#@\\ \t\r\n") || (strings.ContainsAny(host, ":[]") && net.ParseIP(host) == nil) || server.Port < 1 || server.Port > 65535 || server.GRPCPort > 65535 || (server.GRPCPort == 0 && server.Port > 64535) {
				return fmt.Errorf("nacos registry %q has an invalid server address or port", name)
			}
		}
		if r.Timeout.Duration() < time.Millisecond || r.Timeout.Duration() > 30*time.Second {
			return fmt.Errorf("nacos registry %q timeout must be between 1ms and 30s", name)
		}
		if r.StaleAfter.Duration() < 30*time.Second || r.StaleAfter.Duration() > 24*time.Hour {
			return fmt.Errorf("nacos registry %q stale_after must be between 30s and 24h", name)
		}
		if r.Password != "" && r.PasswordEnv != "" {
			return fmt.Errorf("nacos registry %q cannot set both password and password_env", name)
		}
		if r.Username == "" && (r.Password != "" || r.PasswordEnv != "") {
			return fmt.Errorf("nacos registry %q requires username with password authentication", name)
		}
		if r.Username != "" && r.Password == "" && r.PasswordEnv == "" {
			return fmt.Errorf("nacos registry %q requires password or password_env", name)
		}
		if r.PasswordEnv != "" && !environmentName.MatchString(r.PasswordEnv) {
			return fmt.Errorf("nacos registry %q has an invalid password_env", name)
		}
	}
	return nil
}

func (c Config) validateNacosService(name string, service Service) error {
	s := service.Nacos.WithDefaults()
	if _, ok := c.Discovery.Nacos[s.Registry]; !ok {
		return fmt.Errorf("service %q references missing nacos registry %q", name, s.Registry)
	}
	if strings.TrimSpace(s.ServiceName) == "" || strings.ContainsAny(s.ServiceName, "@\x00\r\n") || strings.TrimSpace(s.GroupName) == "" || strings.ContainsAny(s.GroupName, "@\x00\r\n") || (s.Scheme != "http" && s.Scheme != "https") {
		return fmt.Errorf("service %q has invalid nacos service_name, group_name or scheme", name)
	}
	seen := map[string]bool{}
	for _, cluster := range s.Clusters {
		if strings.TrimSpace(cluster) == "" || strings.ContainsAny(cluster, ",\x00\r\n") || seen[cluster] {
			return fmt.Errorf("service %q has invalid or duplicate nacos clusters", name)
		}
		seen[cluster] = true
	}
	// Static probes associate health with array indexes. Until probes support
	// stable endpoint identities, mixing them with changing membership is unsafe.
	if service.HealthCheck != nil {
		return fmt.Errorf("service %q: nacos uses registry health; health_check currently requires static upstreams", name)
	}
	return nil
}
