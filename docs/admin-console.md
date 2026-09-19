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

The console manages a library of named configurations stored in SQLite next
to the active file (`janus-configs.db`). Exactly one record is `active`;
publishing another record archives the previous one. A publish request
normalizes the draft to the active startup-owned sections (settings and
limens), validates and builds a new Runtime generation, persists the active
file with an atomic rename and containing-directory sync, and only then
activates that generation. If
persistence fails, the candidate is released without ever receiving traffic.
Both direct and library-based publishes carry the Runtime revision, so a stale
console cannot overwrite a configuration that another operator has already
published. Settings that affect listeners or process-wide request
infrastructure remain startup-owned; they are normalized away on publish and
must be changed through `全局设置`, which rewrites the file and requires a
restart.

The API also remains compatible with direct atomic edits of the JSON file. The
file reloader recognizes a snapshot already published by the console and does
not build a duplicate generation.

On startup, the configuration library reconciles its `active` marker against
the loaded configuration file when an exact historical record exists. This
covers a process crash between file publication and the SQLite status update;
an unrecognized file is served normally but produces an explicit warning so an
operator can decide whether to import or roll back it.

Published records are immutable rollback history. The console may open an
active record for inspection, but saving content or canvas layout in place is
rejected; use `复制` to create a draft before editing.

## Console 页面

控制台只有三个模块：`概览`、`Config`、`全局设置`。

- `概览`显示生效配置名、Revision，以及分开的失败请求、其他 4xx、5xx
  和 404 计数；404 不会与需要排查的服务端错误混在一起。配置库盘点和
  未绑定路由告警也在此展示。
- `Config`是配置库：卡片列表展示每套配置的名称、状态
 （draft / active / archived）与更新时间，支持新建（空白模板、复制生效
  配置或复制某一套）、删除草稿、一键发布，以及整套配置的 JSON 导入
  （选文件后新建一条）与导出（下载完整配置 JSON；凭据为脱敏占位，
  异机恢复需重填密码，同机复制请用“复制”按钮，凭据由服务端直接拷贝）。
  点`编辑`进入画布。
- 画布是三类节点的 DAG（React Flow）：`Limen`（监听入口，可编辑；
  启动级配置，发布时路由部分即时生效，入口差异会明确提示需重启）、
  `Route`（三段式节点：匹配 → 中间件 → 动作，点击分段直达对应编辑器）、
  `Service`（上游池或 Nacos 引用）。连线即语义：Limen → Route 指定入口，
  Route → Service 设置转发；Backspace/Delete 或“删除选中”按钮删节点，
  被引用的节点会拒绝并点名；顶栏“整理布局”按引用关系分层重排，边尽量
  不交叉。入口监听配置是启动级的：发布时路由部分即时生效，入口差异会
  明确提示需重启；入口抽屉里的“写入文件”可把已保存草稿的入口合并进
  生效文件（运行不受影响，重启后生效）。
- Route / Service 的 Middleware 管理器和 Nacos Registry 管理器使用嵌套事务：
  `确认修改`保留本次资源与编排变更；`取消`、遮罩、右上角关闭和 Escape
  都恢复打开弹窗前的配置快照。Middleware 类型、适用作用域、默认值和
  参数控件来自后端能力目录，不在前端维护另一份类型清单。
- Route 的可视化匹配器可直接添加 `Host`、`Path`、`PathPrefix`、`PathPattern(*)`、`Method`、
  `Protocol`、`Header` 和 `Query` 条件；含 OR、NOT 或括号的表达式保持在
  高级 DSL 模式，以免可视化表单改变原语义。选择 Middleware Scope 后，类型
  列表只显示该 Scope 可用的后端能力；目录给出的必填项、字段类型和数值范围
  也会在保存、校验和发布前做本地快速检查。
- Route 的动作编辑器支持 `Static 静态文件`。填写绝对 `root` 后，可配置
  `index`、SPA fallback、目录浏览和 `Cache-Control`；网关只接受 GET/HEAD，
  并在启动或发布构建时验证根目录和路径安全性。
- 配置画布顶栏提供三种视图：`画布`用于拖拽查看资源关系，`规则`将 Route
  按优先级、名称或配置顺序列出，并可在配置顺序下上下移动；`JSON`用于直接
  查看和编辑完整配置。JSON 修改需要点击“应用 JSON”才会进入草稿，之后仍
  经过同一套保存、校验和发布流程。
- `全局设置`编辑生效配置文件的 settings 段。保存只做校验并写文件，
  不触碰运行中的 generation，保存后必须重启 Janus。

在宽度小于 900px 的窄屏设备上，桌面侧栏会替换为标题下方的横向主导航，
因此 `概览`、`Config` 与 `全局设置` 仍可直接访问；画布和编辑器保持单列、
可纵向滚动的布局。

Visual edits stay in a browser draft until saved to the library; publishing a
record immediately replaces the running generation.

## Frontend development

```text
cd frontend
npm install
npm run dev
npm run build
# from the repository root, refresh the tracked Go embed after reviewing dist/
find internal/admin/ui/assets -type f -delete
cp frontend/dist/index.html internal/admin/ui/index.html
cp frontend/dist/assets/* internal/admin/ui/assets/
./scripts/verify-embedded-ui.sh
```

CI runs the same build and comparison before the Go release gate. The tracked
`internal/admin/ui` files are the release source of truth for `go:embed`; a
successful frontend build alone does not update them.

The Vite development server proxies `/api`, `/livez`, `/readyz`, and `/metrics`
to the local Admin listener at `127.0.0.1:9090`. A release build is copied to `internal/admin/ui/` before
`go build` so the Go `embed` package has the same UI that was reviewed in the
frontend build. The typed API client, config model helpers, request simulator,
node canvas editor, and per-resource drawer editors are kept in separate
modules.

## Middleware capability API

登录后的 `GET /api/v1/capabilities/middlewares` 返回当前二进制实际提供的
Middleware 类型目录。每一项包含稳定类型名、显示名、说明、允许的
`route` / `service` 作用域，以及字段类型、默认值和数值上下限。控制台
用该目录生成 Class 列表和参数表单，因此后端新增内置 Middleware 后，
前端无需再增加类型分支。配置的最终有效性仍由后端 `config.Validate`
判定，能力目录不是配置校验的替代品。
