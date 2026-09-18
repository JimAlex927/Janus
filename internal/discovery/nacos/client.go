// Package nacos is the only layer allowed to import the Nacos SDK. It consumes
// naming/discovery APIs only; Janus never registers backend instances itself.
package nacos

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"janus/internal/config"
	"janus/internal/discovery"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/nacos_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/common/http_agent"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type client struct{ naming naming_client.INamingClient }

func New(registry config.NacosRegistry) (discovery.Client, error) {
	r := registry.WithDefaults()
	var password string
	if r.PasswordEnv != "" {
		password = os.Getenv(r.PasswordEnv)
		if password == "" {
			return nil, fmt.Errorf("nacos password environment variable is unset or empty")
		}
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("nacos SDK cache directory is unavailable")
	}
	data, _ := json.Marshal(r)
	id := fmt.Sprintf("%x", sha256.Sum256(data))
	dir := filepath.Join(cacheRoot, "janus", "nacos", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create nacos SDK cache directory")
	}
	servers := make([]constant.ServerConfig, 0, len(r.Servers))
	for _, server := range r.Servers {
		if server.GRPCPort == 0 {
			server.GRPCPort = server.Port + 1000
		}
		servers = append(servers, constant.ServerConfig{IpAddr: server.Address, Port: server.Port, GrpcPort: server.GRPCPort})
	}
	// Construct only the naming client, avoiding the SDK's umbrella factory
	// which also imports its configuration-center/KMS implementation.
	base := &nacos_client.NacosClient{}
	err = base.SetClientConfig(constant.ClientConfig{
		NamespaceId: r.NamespaceID, Username: r.Username, Password: password,
		TimeoutMs: uint64(r.Timeout.Duration().Milliseconds()),
		// Empty results are authoritative. Disk cache must not resurrect
		// previously removed instances after a restart.
		NotLoadCacheAtStart: true, UpdateCacheWhenEmpty: true,
		// Refresh unchanged services too, otherwise a quiet but healthy service
		// would eventually expire simply because no push event was necessary.
		AsyncUpdateService: true, UpdateThreadNum: 4,
		CacheDir: filepath.Join(dir, "cache"), LogDir: filepath.Join(cacheRoot, "janus", "nacos", "logs"),
		LogLevel: "warn", LogRollingConfig: &constant.ClientLogRollingConfig{MaxSize: 10, MaxBackups: 3, MaxAge: 7},
	})
	if err == nil {
		err = base.SetServerConfig(servers)
	}
	if err == nil {
		err = base.SetHttpAgent(&http_agent.HttpAgent{})
	}
	if err != nil {
		return nil, fmt.Errorf("cannot configure nacos naming client")
	}
	naming, err := naming_client.NewNamingClient(base)
	if err != nil {
		// SDK errors may contain connection/auth details. Do not send those to
		// configuration APIs or logs alongside credential-bearing client config.
		return nil, fmt.Errorf("cannot initialize nacos naming client")
	}
	return &client{naming: naming}, nil
}

func (c *client) Watch(query config.NacosService, changed func()) (func(), error) {
	param := &vo.SubscribeParam{ServiceName: query.ServiceName, GroupName: query.GroupName, Clusters: query.Clusters,
		SubscribeCallback: func(_ []model.Instance, _ error) { changed() }}
	var once sync.Once
	cancel := func() { once.Do(func() { _ = c.naming.Unsubscribe(param) }) }
	if err := c.naming.Subscribe(param); err != nil {
		// The SDK installs its local callback before its network subscription.
		// Return cleanup even on failure so construction rollback removes it.
		return cancel, fmt.Errorf("nacos subscription failed")
	}
	return cancel, nil
}

func (c *client) Snapshot(query config.NacosService) (discovery.Snapshot, error) {
	service, err := c.naming.GetService(vo.GetServiceParam{ServiceName: query.ServiceName, GroupName: query.GroupName, Clusters: query.Clusters})
	if err != nil {
		return discovery.Snapshot{}, fmt.Errorf("nacos service snapshot unavailable")
	}
	result := discovery.Snapshot{Version: service.LastRefTime, Instances: make([]discovery.Instance, 0, len(service.Hosts))}
	for _, instance := range service.Hosts {
		result.Instances = append(result.Instances, discovery.Instance{IP: instance.Ip, Port: instance.Port, Weight: instance.Weight, Healthy: instance.Healthy, Enabled: instance.Enable})
	}
	return result, nil
}

func (c *client) Close() { c.naming.CloseClient() }
