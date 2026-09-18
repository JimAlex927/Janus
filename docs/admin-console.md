# Janus Admin Console

Janus includes an optional private HTTP administration console. The frontend
source lives in `frontend/` and is built independently with Vite; the release
binary embeds the resulting static files under `internal/admin/ui/`.

## Configuration

Set `settings.admin.address` to enable the listener. The address must be a
loopback or private-network address. Set `username` and a bcrypt
`password_hash` to enable login protection and configuration publishing:

```json
"admin": {
  "address": "192.168.10.20:9090",
  "username": "admin",
  "password_hash": "$2a$..."
}
```

The example hash is only a development placeholder and must be replaced before
deployment. Health probes remain available at `/livez` and `/readyz`; the
console API requires the configured session when credentials are present.

Generate a replacement hash without putting the password in shell history:

```text
go run ./cmd/janus-hash
```

For automation, pipe a protected password file through standard input:

```text
Get-Content -Raw .\password.txt | go run ./cmd/janus-hash -password-stdin
```

The command writes only the bcrypt hash to standard output. Copy that value to
`settings.admin.password_hash` and restart Janus.

## Configuration lifecycle

The console keeps a browser draft separate from the running configuration. A
publish request is checked against `X-Janus-Revision`, validated, built as a
new Runtime generation, and then persisted with an atomic rename. If
persistence fails, the previous generation is restored. Settings that affect
listeners or process-wide request infrastructure remain startup-owned; a
configuration containing those changes is rejected by Runtime and must be
applied after a restart.

The API also remains compatible with direct atomic edits of the JSON file. The
file reloader recognizes a snapshot already published by the console and does
not build a duplicate generation.

## Console 页面

控制台采用「表格列表 + 右侧抽屉表单」结构，所有页面共享同一份浏览器
草稿，发布前统一校验：

- `概览`显示请求统计、Revision、未绑定 Service 的路由告警和操作顺序指引。
- `路由`按入口过滤、按名称/规则搜索；新建与编辑在抽屉中完成，只能引用
  已存在的 Service 与 Middleware；底部有请求模拟器，只评估当前草稿，
  不发送真实流量。
- `服务`维护静态上游池或 Nacos 引用（含健康检查）；被路由引用的服务
  不允许直接删除。
- `中间件`创建 buffer / body_limit / in_flight 策略；改名会自动同步所有
  Route 与 Service 的引用，删除会同步清理引用。
- `注册中心`管理 Nacos 连接并支持连通性测试；密码字段留空表示沿用远端
  已保存的凭据。
- `入口`管理监听地址、协议、TLS 与 HTTP/3；地址/协议/TLS 属于启动级
  配置，发布后需重启 Janus。
- `全局设置`编辑 request/stream/server/backend/admin/shutdown 各分组的
  常用字段；留空表示使用后端默认值。
- `JSON` 是全量兜底：表单未覆盖的高级字段只能在这里改。文本是局部
  状态，只有点击「应用到草稿」才会进入全局草稿，避免与表单互相覆盖。

抽屉的取消/确认语义在所有页面一致：打开时快照当前草稿，取消则整体
回滚，确认才写入草稿。删除操作会先检查引用关系，被引用时拒绝并提示
调用方。发布按钮仅在草稿变脏时可用；远端发生变更时若本地无脏草稿则
自动刷新，有脏草稿则只提示，由操作者决定丢弃或先发布。

## Frontend development

```text
cd frontend
npm install
npm run dev
npm run test:model
```

The Vite development server proxies `/api`, `/metrics`, and `/readyz` to the
local Admin listener. A release build is copied to `internal/admin/ui/` before
`go build` so the Go `embed` package has the same UI that was reviewed in the
frontend build. The typed API client, config model helpers, request simulator,
and per-resource editors are kept in separate modules. Deterministic tests
cover the request simulator's match semantics.
