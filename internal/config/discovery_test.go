package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func discoveryConfig() Config {
	return Config{Listen: "127.0.0.1:8080",
		Discovery: DiscoveryConfig{Nacos: map[string]NacosRegistry{
			"one": {NamespaceID: "ns-one", Servers: []NacosServer{{Address: "127.0.0.1", Port: 8848}}},
			"two": {NamespaceID: "ns-two", Servers: []NacosServer{{Address: "127.0.0.1", Port: 8848}}},
		}},
		Services: map[string]Service{"s": {Nacos: &NacosService{Registry: "one", ServiceName: "orders"}}},
		Routes:   []Route{{Name: "r", PathPrefix: "/", Service: "s"}},
	}
}

func TestNacosConfigurationDefaultsAndRoundTrip(t *testing.T) {
	c := discoveryConfig()
	data, _ := json.Marshal(c)
	loaded, err := Load(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Services["s"].Nacos.GroupName != "DEFAULT_GROUP" || loaded.Services["s"].Nacos.Scheme != "http" {
		t.Fatal("missing discovery defaults")
	}
	if loaded.Discovery.Nacos["one"].NamespaceID == loaded.Discovery.Nacos["two"].NamespaceID {
		t.Fatal("namespaces merged")
	}
	if c.Services["s"].Nacos.GroupName != "" {
		t.Fatal("defaulting mutated input")
	}
	view := loaded.EffectiveView()
	if view.Services["s"].Nacos == nil || len(view.Discovery.Nacos) != 2 {
		t.Fatal("effective config lost discovery")
	}
}

func TestNacosConfigurationRejectsAmbiguousSources(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"both": func(c *Config) {
			s := c.Services["s"]
			s.Upstreams = []string{"http://127.0.0.1:9000"}
			c.Services["s"] = s
		},
		"neither":           func(c *Config) { c.Services["s"] = Service{} },
		"unknown registry":  func(c *Config) { c.Services["s"].Nacos.Registry = "missing" },
		"unknown scheme":    func(c *Config) { c.Services["s"].Nacos.Scheme = "tcp" },
		"static health":     func(c *Config) { s := c.Services["s"]; s.HealthCheck = &HealthCheckSettings{}; c.Services["s"] = s },
		"empty name":        func(c *Config) { c.Services["s"].Nacos.ServiceName = "" },
		"duplicate cluster": func(c *Config) { c.Services["s"].Nacos.Clusters = []string{"a", "a"} },
		"credentials":       func(c *Config) { r := c.Discovery.Nacos["one"]; r.Username = "admin"; c.Discovery.Nacos["one"] = r },
		"port":              func(c *Config) { c.Discovery.Nacos["one"].Servers[0].Port = 65535 },
	} {
		t.Run(name, func(t *testing.T) {
			c := discoveryConfig()
			mutate(&c)
			if c.WithDefaults().Validate() == nil {
				t.Fatal("invalid discovery accepted")
			}
		})
	}
}
