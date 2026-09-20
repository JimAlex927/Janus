# 下一阶段首批实施记录

日期：2026-09-20。对应 [实施计划](./next-stage-plan-2026-09-20.md)。
基线：`f2d9191` 加当前工作区；本轮未提交、推送或部署生产。

## 1. 这次推进到了哪里

| 计划项 | 本轮结果 | 尚未完成的边界 |
| --- | --- | --- |
| A1 本轮已有修复 | ForwardAuth、gzip 协商、in-flight 与测试修正一并通过完整 test/race | 仍待整理为可审阅提交 |
| A2 UI 构建契约 | 根路径、`/janus/`、嵌套路径测试；双实际产物与浏览器验收；构建不覆盖仓库 embed | PowerShell 入口尚未在 Windows 实机执行 |
| A3 工具链和证据 | CI / 项目推荐工具链改为 Go 1.26.8；本地扫描；双产物 manifest 和日志 | 干净 checkout、远端 CI、Linux 执行仍待验收；不是发布批准 |
| B1 JWKS 故障边界 | 合并刷新、可取消等待、失败退避、旧 key 上限、rotation/recovery 回归 | ForwardAuth 的完整慢 body/拒绝/超大响应矩阵仍待补齐 |
| B3 真连接 reload | H1、HTTPS H1、H2 并发流、SSE、共享 upstream 连接与地址切换回归 | 本轮未新增真证书轮换/retired 上限组合验收；原有相关测试继续保留 |
| 控制台 SSE | 浏览器发现总写期限截断；真实连接复现后修复 | 不等于已完成 B4 草稿与存储故障专项 |
| B2 / B4 其余项 | 保留原计划中的明确缺口 | 下一批优先处理响应组合和草稿并发 |
| C / D / E | 未执行目标环境工作 | 需要 Linux、Nacos、负载目标和灰度范围 |

## 2. UI base 与构建约定

### 唯一的运行时挂载路径

- Vite 的生产资源统一使用相对路径 `./assets/...`，不再把 `/janus` 固化进 JS。
- Go Handler 在 HTML 的 `<head>` 起始位置插入经过路径校验的 `<base href=".../">`。
- API 与 EventSource 共用 `document.baseURI`；HTML 使用 `Cache-Control: no-store`。
- `/janus` 重定向至 `/janus/`；直接访问 `/janus/index.html` 也使用同样的 base。
- `Options.UIBaseURL == "/"` 可以明确覆盖链接时的默认前缀；空字符串继承默认值。
- 构建参数拒绝空段、`.` 和 `..` 路径段。没有改变 JSON 配置格式。
- 反向代理必须保留配置的挂载前缀，不能把 `/janus` 挂载与 strip-prefix 混用。没有擅自修改用户的示例部署配置。

`internal/admin/admin_test.go` 的资源测试按 HTML 实际引用和 base 解析 URL 后请求，
不再手动拼接期望前缀来“帮助”页面通过。覆盖 JS/CSS、认证 API、事件流、
Cookie Path、登出清除和旧 cookie 失效。

### 临时目录发布构建

`scripts/build-app.sh` 和 `scripts/build-app.ps1` 调用共同的
`scripts/build-app.mjs`：前端生成到临时目录，复制构建所需 Go 源码，在临时树中
组装 embed 并编译，最后清理临时树。普通发布构建不修改 tracked embed，也不修改
`frontend/dist`。源码开发仍需显式更新并审阅 embed，随后执行 parity 检查。

输出旁的 `<binary>.manifest.json` 包含 commit、dirty 标记、Go/Node 版本、目标平台、
挂载路径、链接参数、CGO/UPX、二进制 SHA-256、UI 文件哈希、前端 lockfile / go.sum
哈希，以及暂存源码树摘要。它用于追踪本次构建，不宣称 dirty tree 可从 commit 单独复现。

本轮把已有定制 UI 备份至 `/tmp/janus-embed-backup-dkY1Q1`，再更新为通用构建。
该备份位于临时目录，不应作为长期备份。用户原有配置、未关联二进制和请求流程图未改动。

## 3. JWKS 修复的具体语义

实现：`internal/middleware/jwt.go`；测试：`internal/middleware/jwt_refresh_test.go`。

1. JWT 校验接收入站 `Context`；等待认证依赖时可以及时响应当前请求取消。
2. 每个 verifier 同时至多一个共享刷新；互斥锁不覆盖网络 IO。
3. 共享刷新使用独立的 5 秒 Context，单个等待者取消不取消其他等待者的刷新。
4. 失败后退避 5 秒，包括冷缓存失败；并发请求不能排队制造连续网络刷新。
5. 保持 5 分钟正常缓存和 15 分钟最大 stale 时间；失败不延长旧 key 的期限。
6. 未知 kid 仍拒绝，成功刷新后维持 30 秒强制刷新冷却；rotation 后移除的 key 不保留。
7. 在完成刷新后的真实时间判断 stale，取 key 时再次检查，避免等待过程跨过期限后误放行。
8. HTTPS / 同主机重定向限制保留，增加最多 10 次重定向的上限。

新增回归覆盖 64 个并发等待者取消、单次外部请求、冷缓存失败退避、未知 kid 洪泛、
key rotation、旧 key 移除、过期缓存故障宽限、期限届满拒绝和认证服务恢复。
这不是跨 generation 共享 JWKS 缓存；每一代仍有自己的 verifier。

## 4. 真连接上的热更新证明

测试：`internal/runtime/connection_reload_test.go`。

| 场景 | 断言 |
| --- | --- |
| HTTP/1.1 reload 后第二个请求 | `httptrace.GotConn` 为同一个 `net.Conn`，`Reused=true`，服务端只 accept 一次，响应从 old 变 new |
| HTTPS HTTP/1.1 | 上述断言外，`TLSHandshakeDone` 总数恰为 1 |
| H2 慢流与新请求 | 同一真实 TLS/H2 连接，慢流返回 old，新请求返回 new；旧代在流结束前不关闭，结束后回收 |
| HTTP/1 SSE 跨 reload | 旧流继续输出 old-end，新请求返回 new；H1 的并行请求使用另一连接是正常行为 |
| 同 upstream reload | 使用实际 Gateway，后端只 accept 一次，证明进程共享 Transport 复用了出站连接 |
| upstream 地址改变 | 后续响应来自第二个后端，不会错误地继续命中旧地址 |

H1/H2/SSE 入站用例使用真实 `net/http` server 和 Runtime 测试 generation，专门验证
连接层与 generation 的边界；upstream 用例使用实际 Gateway。它们不替代 Limen 的
证书、入口重载及目标网络验证。原来的详细流程图和时序图继续保留。

## 5. 浏览器暴露并修复的 Admin SSE 截断

Playwright 实际登录 `/` 与 `/janus/` 后，观察到事件流周期性出现
`ERR_INCOMPLETE_CHUNKED_ENCODING`。原因是 Admin Server 的普通 HTTP `WriteTimeout`
仍是整个响应的期限，而事件流没有重新设置 deadline。

新增 `TestEventsOutliveServerWriteTimeout` 把服务器期限设为 30ms，先读取 ready，
90ms 后再发送事件。修复前稳定得到 `unexpected EOF`；修复后连续 10 次通过。

修复只作用于 Admin SSE：每次 event/heartbeat 写入和 Flush 使用 5 秒 deadline，
成功后清除 deadline，等待下一事件时不占用普通响应的总期限。写/Flush 失败立即退出，
释放订阅。未取消普通 Admin API 的超时，也未改变网关 Route 的 stream timeout。

## 6. 验证证据与工具链

验证环境：macOS arm64；最终 Go 1.26.8、Node v26.7.0；本轮没有运行目标 Linux。

| 检查 | 结果 |
| --- | --- |
| `go test -count=1 ./...` | 通过，无 skip |
| `go test -race ./...` | 通过，无 skip |
| `go vet ./...` / `go mod verify` | 通过 |
| 配置解析 fuzz，10s | 通过；仅短时健壮性检查，不是完整 fuzz 验收 |
| JWT 专项重复 10 次 / 真连接专项重复 5 次 | 通过；最终全仓 race 再覆盖 |
| SSE 跨写期限专项重复 10 次 | 通过 |
| 前端 build / embed parity / `git diff --check` | 通过 |
| 两份发布产物 | 已生成根路径和 `/janus` 版本；manifest 标记 dirty |
| 最终产物 SSE 持续验收 | 两种挂载均以 `JANUS_SMOKE_HEARTBEATS=3` 接收约 45s 的 3 次心跳，超过原总写期限后仍连通，随后登出及 Cookie 失效通过 |
| 浏览器 | 两种路径登录、刷新恢复会话、登出成功；`/janus/` 的实际 JS/CSS URL、HttpOnly / SameSite=Strict Cookie 和 EventSource ready 已检查 |
| 原生 PowerShell / 远端 CI / 目标 Linux | 未执行；CI 已加入两种实际产物的本机 HTTP 冒烟步骤 |

本轮验证日志位于 `output/qualification/2026-09-20/`，浏览器快照与诊断日志在
`output/playwright/cli/`；这些本机证据目录不加入版本控制。浏览器验收使用临时配置和
测试账号，未访问用户运行中的服务；测试会话及临时服务已关闭。

本机最终产物（darwin/arm64，不是目标 Linux 发布包）：

- `bin/janus-root`：SHA-256 `884455dab1540d8d99adad51a9740fd84c1a6ac19339f85017abe18bbcc1b37d`。
- `bin/janus-prefixed`：SHA-256 `f60a6f5da742027624956a1116c1f75333724d52f1ff96f6c1513a141b6c0f63`。
- 两份 manifest 的 UI 资源哈希相同，挂载前缀不同；二者暂存源码树摘要相同。

### 工具链安全结论

本机原有 Go 1.26.5 的 fresh scan 命中 7 项可达标准库漏洞。官方
[发布记录](https://go.dev/doc/devel/release) 列出了 1.26.6 的安全修复和随后补丁。
本轮选择仍受支持的 1.26 分支中的 1.26.8，而不是升级到新的 1.27 大版本。
`go.mod` 增加 `toolchain go1.26.8`，CI 固定 1.26.8，系统 Go 安装未被替换。

最终命令 `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 -show verbose ./...`
退出 0，**可达漏洞 0**。不是“所有依赖无漏洞”：仍有以下静态分析未发现调用路径的告警。

| 层级 | 告警 | 当前处理 |
| --- | --- | --- |
| 导入包 | gRPC `GO-2026-6443`；jsonparser `GO-2026-4514` | 尚未升级，记录为后续依赖补丁任务；变更调用路径后必须重扫 |
| 依赖模块 | SSH `GO-2026-6355`、`6354`、`6303`；OpenPGP `GO-2026-5932` | 本项目扫描未发现调用这些功能；不宣称模块本身安全 |

这些告警的原文、版本和修复版本都保留在 `govulncheck.log`。未做大范围依赖升级，
以免把 Nacos/gRPC 的额外兼容性变更混入连接与认证修复。

## 7. 下一批实施顺序

1. B4：`useConfig` 请求/编辑版本栅栏，确保迟到的 load 响应和 publish 后 reload 不覆盖新草稿；增加浏览器并发复现与回归。
2. B2：compress/buffer/headers/cors/timeout 组合，先处理首次最终状态码与 Range，再审查静态文件打开竞争。
3. B1 补齐：ForwardAuth 慢响应/超大响应/请求体/拒绝路径；进一步扩大 JWKS 非法响应和真实网络故障矩阵。
4. A3 收尾：审阅本次变更形成干净候选，远端执行更新后的 Linux CI；单独评估未可达依赖告警的补丁升级。
5. C 前需要明确目标 Linux 的访问方式、拓扑、Nacos 实例和首批后端；D 前填写计划中的流量/SLO 表；E 前明确灰度和回滚授权。

本轮完成的是可以独立验证的首批代码交付，不能据此宣称整个下一阶段计划完成或可以直接上线。
