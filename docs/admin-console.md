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
limens), validates it, builds a new Runtime generation, and then persists
the active file with an atomic rename. If persistence fails, the previous
generation is restored. Settings that affect listeners or process-wide
request infrastructure remain startup-owned; they are normalized away on
publish and must be changed through `全局设置`, which rewrites the file and
requires a restart.

The API also remains compatible with direct atomic edits of the JSON file. The
file reloader recognizes a snapshot already published by the console and does
not build a duplicate generation.

## Console 页面

控制台只有三个模块：`概览`、`Config`、`全局设置`。

- `概览`显示生效配置名、Revision、请求统计、配置库盘点和未绑定路由告警。
- `Config`是配置库：卡片列表展示每套配置的名称、状态
 （draft / active / archived）与更新时间，支持新建（空白模板、复制生效
  配置或复制某一套）、删除草稿、一键发布，以及整套配置的 JSON 导入
  （选文件后新建一条）与导出（下载完整配置 JSON；凭据为脱敏占位，
  异机恢复需重填密码，同机复制请用“复制”按钮，凭据由服务端直接拷贝）。
  点`编辑`进入画布。
- 画布是三类节点的 DAG（React Flow）：`Limen`（监听入口，可增删改；
  启动级配置，发布时路由部分即时生效，入口差异会明确提示需重启）、
  `Route`（三段式节点：匹配 → 中间件 → 动作，点击分段直达对应编辑器）、
  `Service`（上游池或 Nacos 引用）。连线即语义：Limen → Route 指定入口，
  Route → Service 设置转发；Backspace/Delete 或“删除选中”按钮删节点，
  被引用的节点会拒绝并点名；顶栏“整理布局”按引用关系分层重排，边尽量
  不交叉。入口监听配置是启动级的：发布时路由部分即时生效，入口差异会
  明确提示需重启；入口抽屉里的“写入文件”可把已保存草稿的入口合并进
  生效文件（运行不受影响，重启后生效）。
- `全局设置`编辑生效配置文件的 settings 段。保存只做校验并写文件，
  不触碰运行中的 generation，保存后必须重启 Janus。

Visual edits stay in a browser draft until saved to the library; publishing a
record immediately replaces the running generation.

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
node canvas editor, and per-resource drawer editors are kept in separate
modules.
