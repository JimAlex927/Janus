package nacos

import (
	"errors"
	"testing"

	"janus/internal/config"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type namingStub struct {
	naming_client.INamingClient
	watch   *vo.SubscribeParam
	unwatch *vo.SubscribeParam
	get     vo.GetServiceParam
	service model.Service
	err     error
	cancels int
}

func (s *namingStub) Subscribe(p *vo.SubscribeParam) error   { s.watch = p; return s.err }
func (s *namingStub) Unsubscribe(p *vo.SubscribeParam) error { s.unwatch = p; s.cancels++; return nil }
func (s *namingStub) GetService(p vo.GetServiceParam) (model.Service, error) {
	s.get = p
	return s.service, s.err
}

func TestAdapterKeepsSubscriptionIdentityForRollback(t *testing.T) {
	s := &namingStub{err: errors.New("failed after callback registration")}
	c := &client{naming: s}
	notifications := 0
	query := config.NacosService{ServiceName: "orders", GroupName: "business", Clusters: []string{"east"}}
	cancel, err := c.Watch(query, func() { notifications++ })
	if err == nil || cancel == nil {
		t.Fatal("failure must still provide cancellation")
	}
	s.watch.SubscribeCallback(nil, nil)
	if notifications != 1 {
		t.Fatal("lost change notification")
	}
	cancel()
	cancel()
	if s.watch != s.unwatch || s.cancels != 1 {
		t.Fatal("SDK callback identity lost or cancelled twice")
	}
	if s.watch.GroupName != "business" || s.watch.Clusters[0] != "east" {
		t.Fatal("subscription query changed")
	}
}

func TestAdapterPreservesRegistryVersionAndInstanceFlags(t *testing.T) {
	s := &namingStub{service: model.Service{LastRefTime: 123, Hosts: []model.Instance{{Ip: "127.0.0.1", Port: 8080, Weight: 3, Healthy: false, Enable: true}}}}
	c := &client{naming: s}
	snapshot, err := c.Snapshot(config.NacosService{ServiceName: "orders", GroupName: "g", Clusters: []string{"west"}})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 123 || len(snapshot.Instances) != 1 || snapshot.Instances[0].Healthy || !snapshot.Instances[0].Enabled || snapshot.Instances[0].Weight != 3 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if s.get.GroupName != "g" || s.get.Clusters[0] != "west" {
		t.Fatal("snapshot query changed")
	}
	s.service.Hosts = nil
	empty, err := c.Snapshot(config.NacosService{})
	if err != nil || len(empty.Instances) != 0 || empty.Version != 123 {
		t.Fatal("empty success converted to failure")
	}
}

func TestMissingPasswordFailsBeforeCreatingSDKClient(t *testing.T) {
	t.Setenv("JANUS_TEST_NACOS_PASSWORD", "")
	if _, err := New(config.NacosRegistry{Username: "user", PasswordEnv: "JANUS_TEST_NACOS_PASSWORD"}); err == nil {
		t.Fatal("missing secret accepted")
	}
}
