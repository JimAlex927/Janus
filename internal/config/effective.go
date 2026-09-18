package config

// EffectiveConfig is the operator-facing, normalized view of a Config.
// Secret-bearing TLS asset paths are intentionally not part of this type.
type EffectiveConfig struct {
	Discovery   DiscoveryConfig             `json:"discovery,omitempty"`
	Version     int                         `json:"version"`
	Limens      map[string]EffectiveLimen   `json:"limens"`
	Settings    Settings                    `json:"settings"`
	Middlewares map[string]Middleware       `json:"middlewares,omitempty"`
	Services    map[string]EffectiveService `json:"services"`
	Routes      []Route                     `json:"routes"`
}

type EffectiveLimen struct {
	Address        string         `json:"address"`
	Protocols      []string       `json:"protocols"`
	TrustedProxies []string       `json:"trusted_proxies,omitempty"`
	TLS            *EffectiveTLS  `json:"tls,omitempty"`
	HTTP3          *HTTP3Settings `json:"http3,omitempty"`
}

type EffectiveTLS struct {
	Enabled    bool   `json:"enabled"`
	MinVersion string `json:"min_version,omitempty"`
}

type EffectiveService struct {
	Nacos       *NacosService        `json:"nacos,omitempty"`
	Upstreams   []string             `json:"upstreams"`
	Middlewares []string             `json:"middlewares,omitempty"`
	HealthCheck *HealthCheckSettings `json:"health_check,omitempty"`
}

// EffectiveView returns the validated configuration in the form used by
// operators to inspect runtime defaults. Legacy syntax is represented as one
// normalized Limen named "default". TLS certificate and key paths are omitted
// so the output is safe to include in diagnostics and deployment checks.
func (c Config) EffectiveView() EffectiveConfig {
	c = c.WithDefaults()
	settings := c.Settings
	settings.Admin.PasswordHash = ""
	bindings := c.LimenBindings()
	limens := make(map[string]EffectiveLimen, len(bindings))
	for name, binding := range bindings {
		item := EffectiveLimen{
			Address:        binding.Address,
			Protocols:      append([]string(nil), binding.Protocols...),
			TrustedProxies: append([]string(nil), binding.TrustedProxies...),
		}
		if binding.TLS != nil {
			item.TLS = &EffectiveTLS{Enabled: true, MinVersion: binding.TLS.MinVersion}
		}
		if binding.HTTP3 != nil {
			http3 := binding.HTTP3.WithDefaults()
			item.HTTP3 = &http3
		}
		limens[name] = item
	}

	middlewares := make(map[string]Middleware, len(c.Middlewares))
	for name, middleware := range c.Middlewares {
		middlewares[name] = middleware
	}
	services := make(map[string]EffectiveService, len(c.Services))
	for name, service := range c.Services {
		item := EffectiveService{
			Nacos:       service.Nacos,
			Upstreams:   append([]string(nil), service.Upstreams...),
			Middlewares: append([]string(nil), service.Middlewares...),
		}
		if service.HealthCheck != nil {
			check := service.HealthCheck.WithDefaults()
			item.HealthCheck = &check
		}
		services[name] = item
	}
	routes := make([]Route, len(c.Routes))
	for index, route := range c.Routes {
		route.Protocols = append([]string(nil), route.Protocols...)
		route.Middlewares = append([]string(nil), route.Middlewares...)
		routes[index] = route
	}
	return EffectiveConfig{
		Discovery:   c.Discovery.Redacted(),
		Version:     CurrentConfigVersion,
		Limens:      limens,
		Settings:    settings,
		Middlewares: middlewares,
		Services:    services,
		Routes:      routes,
	}
}
