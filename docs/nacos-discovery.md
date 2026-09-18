# Nacos 服务发现：核心实现与后续分工

## 本轮范围

修改前的工作区已提交：`705234e`，`feat(admin): add embedded visual configuration console`。

本轮实现服务发现核心；不接入 Nacos 配置中心。`namespace_id + group_name + service_name` 定位服务，`dataId` 不属于本功能。

Nacos 服务端按 2.x 的 Naming/gRPC 模型接入，固定 Go SDK `v2.3.5`。适配器直接创建 NamingClient；SDK 仍有 RAM 凭据、gRPC、指标等传递依赖，集中在适配器边界内。

## 配置契约

以下是配置片段，应合并到已有的 Limen/Route 配置中：

```json
{
  "discovery": {
    "nacos": {
      "orders-prod": {
        "servers": [{ "address": "10.0.0.10", "port": 8848 }],
        "namespace_id": "orders-namespace-id",
        "username": "janus",
        "password_env": "JANUS_NACOS_PASSWORD",
        "timeout": "5s",
        "stale_after": "2m"
      },
      "payments-prod": {
        "servers": [{ "address": "10.0.0.10", "port": 8848 }],
        "namespace_id": "payments-namespace-id",
        "username": "janus",
        "password_env": "JANUS_NACOS_PASSWORD"
      }
    }
  },
  "services": {
    "orders": {
      "nacos": {
        "registry": "orders-prod",
        "service_name": "order-service",
        "group_name": "DEFAULT_GROUP",
        "clusters": ["DEFAULT"],
        "scheme": "http"
      },
      "middlewares": ["orders-cap"]
    },
    "payments": {
      "nacos": {
        "registry": "payments-prod",
        "service_name": "payment-service"
      }
    }
  },
  "middlewares": {
    "orders-cap": { "in_flight": { "max_concurrent": 100 } }
  }
}
```

- 每个 registry 配置绑定一个 namespace；多个配置支持同集群不同 namespace，也支持不同集群和账号。
- 一个 Service 必须选择非空 `upstreams` 或 `nacos` 其中一种来源。
- `group_name` 默认 `DEFAULT_GROUP`，`scheme` 默认 `http`；`clusters` 省略表示不限制集群。
- `namespace_id` 是实际 ID；空字符串和 `public` 归一化为 SDK 的公共 namespace。
- `servers.address` 为主机/IP，不带协议、路径或端口；支持独立 `grpc_port`，默认 `port + 1000`。部署必须保证 SDK 的 HTTP 与 gRPC 端口均可访问。
- `username` 与 `password_env` 成对配置。配置文件、effective view 和管理接口保存环境变量名称，不保存解析后的口令；环境变量变更需重启以重新创建客户端。
- `timeout` 默认 5s（1ms..30s）；它是 SDK 请求超时参数，不是整个建连/重试流程的总截止时间。
- `stale_after` 默认 2m（30s..24h），表示最后一次观察到新的服务快照后，最多继续使用地址多久。应大于 Nacos 的正常刷新周期。
- 本轮支持的 Nacos 连接使用 SDK 默认内网 HTTP/gRPC；Nacos TLS/mTLS、RAM 动态身份不在当前配置契约中。`scheme: https` 控制的是后端实例连接，不是 Nacos 连接。

## 分层和所有权

```text
config/discovery.go           配置、默认值、互斥/引用校验
discovery/Client              可替换的服务发现客户端接口
discovery/nacos/client.go     唯一导入 Nacos SDK 的生产代码
discovery/Manager             客户端、订阅引用计数和后台刷新
upstream/DynamicPool          不可变实例快照、原子替换、权重选择
proxy/TargetSelector          静态池和动态池共用的本地选址接口
gateway                      获取 Service 租约、组合 Middleware/Proxy
runtime                      跨 Generation 复用 Manager，回滚/排空释放租约
```

客户端 key 包含完整 registry 配置，包括 namespace 和凭据引用；订阅 key 还包含 Service 查询条件。同一连接下不同服务复用客户端，但拥有独立订阅/地址池。同一服务跨 Generation 复用订阅。

Gateway 构建成功后持有 `Lease`。构建中途失败，清理已获取的租约；发布成功后，旧 Generation 保持租约直到最后一个请求退出。最后一个租约关闭时，停止 Janus 后台刷新、取消 SDK 订阅，并在没有其他订阅时关闭客户端。`Close` 幂等。

实例更新不调用 `Runtime.Replace`，也不增加配置 revision。每个新请求只读取本地原子快照，选择一次目标；已经转发中的请求不重新选址。SSE/WebSocket 不会仅因实例移出列表而被网关主动断开，但后端退出仍可能中断它们。

## 实例与故障语义

- 仅选择 `healthy && enabled && weight > 0` 的实例；验证 IP、端口与有限权重，去重同一 origin；支持 IPv6。
- 动态池按权重随机选择；静态池继续使用现有轮询算法。
- 成功收到空列表立即发布空快照，新请求返回 503。
- 读取失败或无效实例批次保留最后有效快照；过了有效期后新请求返回 503，收到新有效快照后恢复。
- SDK 缓存成功读取不会续期。利用 `LastRefTime` 识别新版本，忽略旧版本；启用 SDK `AsyncUpdateService` 刷新没有变化的服务，避免安静的服务因为没有推送而错误过期。
- 首次订阅/读取失败阻止启动或新配置发布；已运行的旧 Generation 保留。
- SDK 禁用启动时读取磁盘旧缓存，并启用空实例列表更新；缓存和有界滚动日志位于用户缓存目录下 `janus/nacos/`。
- Nacos Service 暂时不允许同时配置 Janus `health_check`：静态探测健康状态按数组索引绑定，不适合直接复用于动态增删。后续若增加主动探测，必须先改成稳定 endpoint 身份模型。

`Runtime.DiscoverySnapshot()` 提供每个 Service 的实例数、最后更新、到期时间和失败/过期标记。它是只读运行态视图，读取不触发 Nacos 网络请求；没有变化的缓存读也不会被标记为新同步。

## 给 Luna 的明确任务

1. Service 编辑器增加“静态 URL / Nacos”来源切换，切换时删除互斥字段。Nacos 表单使用上面已经实现的字段，禁止输入 `dataId`。显示 namespace 所属连接及健康检查限制。
2. 增加 registry 配置表单，支持多连接、多 namespace、多个服务器以及环境变量名称。沿用 JSON/画布同一草稿，不在浏览器解析秘密。
3. 更新 Service 节点摘要：静态来源显示 URL 数，Nacos 来源显示 registry / group / service。不能把 Nacos Service 显示成“0 upstream、无效配置”。
4. 将 `Runtime.DiscoverySnapshot` 通过受保护的 Admin API 暴露，补充状态与到期提示；API 没接好前不能在 UI 展示虚构的“健康/已连接”。
5. 添加完整 `configs/janus-nacos.example.json`（当前版本 Match/Action 格式，两个 namespace），更新 README 和部署环境变量说明。
6. 前端构建、模型测试，重新同步嵌入资源。提交前更新 commit-log，避免把旧构建 bundle 反复堆入嵌入目录。

核心 Client/Manager/Pool 和生命周期规则已经实现；这些任务不需要再实现订阅或在前端轮询 Nacos。

## 验证边界

自动化测试覆盖配置校验、namespace 隔离、客户端/订阅复用、初始失败清理、缓存不续期、过期/恢复、空列表清空、乱序版本、并发快照替换，以及真实 TCP HTTP 客户端观测到上游切换/503/恢复。

Runtime 回归验证配置失败时旧服务仍可转发，跨代订阅不重建，在途请求完成后才释放旧订阅。还修复了原有 Gateway 构建失败返回 typed-nil Generation 导致回滚 panic 的问题。

测试使用可注入的发现客户端及 SDK 接口替身；真实 Nacos 集群的鉴权、断连重连、集群切换与持续运行验证仍是发布前的集成验收项。

本轮 `go test ./...` 和 `go vet ./...` 通过。Windows 上执行核心包 `go test -race` 时，ThreadSanitizer 因内存分配失败（error code 87）未能启动；这不是 race 检查通过，需在目标 Linux 环境重跑。
