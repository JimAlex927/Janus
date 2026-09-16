// Package config owns the file format and validates it before listeners open.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	Listen   string             `json:"listen"`
	Settings Settings           `json:"settings"`
	Services map[string]Service `json:"services"`
	Routes   []Route            `json:"routes"`
}

type Service struct {
	Upstreams []string `json:"upstreams"`
}

type Route struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	PathPrefix string `json:"path_prefix"`
	Service    string `json:"service"`
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

func (c Config) Validate() error {
	settings := c.Settings.WithDefaults()
	if err := settings.Validate(); err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(c.Listen)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || p < 1 || p > 65535 {
		return fmt.Errorf("listen must be host:port with port 1..65535")
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
		key := strings.ToLower(r.Host) + "\x00" + r.PathPrefix
		if matches[key] {
			return fmt.Errorf("duplicate host/path match on route %q", r.Name)
		}
		matches[key] = true
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
