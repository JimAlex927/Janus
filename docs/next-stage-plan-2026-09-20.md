# Janus 当前评估与下一阶段实施计划

评估日期：2026-09-20。基线：`f2d9191` 加当前工作区改动；开发机为 macOS arm64，Go 1.26.5。

实施更新：下面保留评估时的基线和发现，不能作为当前测试状态。后续 A/B 阶段的代码、Go 1.26.8 工具链与最新验证结果见 [首批实施记录](./next-stage-progress-2026-09-20.md)。

## 1. 建议先做什么

下一阶段目标是：**完成一个可重复构建、可解释故障、可灰度回滚的首个私有环境候选版本。**

Janus 已经有较完整的数据面与控制面：Limen、多协议入口、稳定 Runtime、generation 热更新、Router、Service、策略中间件、Nacos、健康检查、指标、管理台和配置库。继续叠加协议或策略功能，当前收益小于修复边界问题、验证发布链路和完成真实环境验收。

建议按以下顺序推进：

1. 收敛本次发现的正确性问题与构建路径问题，使目标构建的完整测试无需跳过即可通过；
2. 补齐鉴权依赖故障、真实连接跨 reload、管理台发布冲突的验收用例；
3. 在目标 Linux 上完成部署、故障、负载与 24 小时持续运行；
4. 选择一个低风险服务做灰度，并实际演练回滚；
5. 从运行数据决定是否需要重试、熔断、权重负载均衡或 gRPC。

计划默认以私有部署、一个可维护的 Janus 实例版本、HTTP/1.1 + TLS HTTP/2 为首批基线；SSE/WebSocket 根据首批服务需求加入。HTTP/3 保留现有实现，只有完成 UDP/QUIC 环境验收后才纳入首批生产协议。这里的协议选择是实施建议，不改变当前配置。

## 2. 当前状态：实现充分，发布资格仍有缺口

| 领域 | 代码证据与判断 | 下一步 |
| --- | --- | --- |
| 入口与协议 | `internal/limen` 已有 TLS、H1/H2/h2c/H3、证书轮换和停机测试 | 在目标 Linux / 实际 LB / 防火墙路径复验 |
| 热更新与生命周期 | `Runtime.replace/acquire/release` 实现构建后发布、旧代引用计数、退休代上限，共享 Transport | 把真实 H1 Keep-Alive、TLS 握手计数、H2 同连接并发流做成明确验收矩阵 |
| 路由与策略 | DSL、Route/Service 中间件、内置并发与超时、静态文件等均已实现 | 策略组合的边界比新增策略更优先 |
| 鉴权 | JWT、ForwardAuth、Basic Auth、IP 策略可用；本轮确认一处身份头边界漏洞 | 故障期间的取消、并发、缓存和信任边界专项 |
| 服务发现 | Runtime 级 Manager 管理 Nacos client/feed，generation 持有 lease | 真 Nacos 空列表、失联、恢复、跨 namespace 和高频更新验收 |
| 控制面 | 私有管理台、SQLite 配置库、版本冲突、发布/归档、启动配置落盘 | 浏览器自动化、备份恢复、构建 base path 一致性 |
| 可观测性 | 自定义有基数上限的 Prometheus 指标与请求观测 | 本轮修正总并发重复计数；补充值班所需图表、告警、失败分类 |
| CI / 交付 | CI 声明 Go 1.25.14，含测试、race、fuzz、vet、漏洞扫描；有 Linux gate 与 systemd unit | 本轮未读取远端 CI 运行结果；需要归档一次同版本、同配置的真实 Linux 结果 |
| 容量 | 有普通 HTTP 压测器 `cmd/janus-loadtest` | 尚不能由当前证据承诺 RPS；缺协议混合压力与目标机 soak |

现有 `docs/plan.md` 记录了历史实施阶段，部分“全局 timeout”等描述已落后于当前 Route 内置链。继续保留历史计划，但以本次代码与测试结果作为后续排期依据。

## 3. 本轮已完成的修复

### F1：ForwardAuth 成功响应缺少身份头时，客户端伪造值残留

- 触发：配置 `auth_response_headers = [X-User, X-Role]` 或匹配这两个头的正则；请求携带 `X-Role: admin`；认证服务返回 2xx，只给出 `X-User: alice`。
- 修复前：`copyAuthResponseHeaders` 只遍历认证响应中的头，因此 `X-Role: admin` 留在请求中。如果后端信任这个身份头，就可能错误授予权限。
- 修复后：先移除客户端请求中所有被选择且允许由认证服务设置的头，再复制认证响应的值。未选择的业务头保留。
- 覆盖：显式列表和正则两种路径；认证值覆盖客户端值；缺失字段必须消失；无关字段保持。
- 位置：`internal/middleware/forward_auth.go`、`forward_auth_test.go`。
- 优先级：P0，启用相关 ForwardAuth 身份传递的部署应包含此修复。

### F2：压缩协商没有尊重明确的 gzip 禁用

- 触发：`Accept-Encoding: gzip;q=0, *;q=1`，或反向顺序 / 拆成多个 Header 值。
- 修复前：看到可接受的 `*` 就返回 true，仍发送 gzip。
- 修复后：显式 gzip 条目优先；仅当未列出 gzip 时才使用 wildcard。
- 覆盖：两种顺序、多个 Header 值、wildcard-only、显式允许、其他编码和无效 q 值。
- 位置：`internal/middleware/compress.go`、`compress_test.go`。
- 依据：[RFC 9110 §12.5.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-12.5.3) 中 wildcard 只匹配未显式列出的编码。

### F3：管理台 in-flight 重复统计嵌套许可

- 触发：同一个请求同时占用 global、route、service 三个许可。
- 修复前：`Metrics.Summary()` 把三个 scope 相加，实际 1 个请求可能显示为 3。
- 修复后：总并发读取 global gauge；各 scope 的 Prometheus 明细仍保留。
- 覆盖：三层许可只计一个请求、明细仍可读、全部释放后为零。
- 位置：`internal/telemetry/metrics.go`、`metrics_test.go`。

### F4：静态目录重定向测试错误地要求绝对路径

- 现象：Go FileServer 返回 `Location: test/`，测试要求 `/test/`。
- 检查：请求 URL 为 `http://gateway/test` 时，两者均指向 `http://gateway/test/`；这是测试断言过窄。
- 修改：按原始请求 URL 解析 Location，再断言最终目标的完整 URL。嵌套目录也保持相同覆盖。
- 位置：`internal/gateway/action_test.go`；未修改文件服务运行逻辑。

补充修正文档：本机 Go `net/http` 的 `conn.serve` 在解析 HTTP 前显式调用 `tlsConn.HandshakeContext(ctx)`，之前的说明把它简化成“首次 Read 触发”。已在请求栈文档中修正。每连接一次握手、每请求重新 acquire generation 的结论不变。

## 4. 本轮验证与剩余限制

| 验证 | 结果 |
| --- | --- |
| F1 / F2 / F3 新回归用例，修复前 | 均复现失败：身份头残留、发送禁用的 gzip、并发 1 显示 3 |
| 修复后相关测试 | 通过 |
| 首次 `go test ./...` | 两个 Admin embed 路径测试失败，一个静态目录 Location 断言失败 |
| 静态目录断言修正后 | 对应用例通过 |
| 最终 `go test ./...` | 仅保留下面两个 Admin 路径测试失败；其他包通过 |
| 全仓 race，跳过下面两个已知 Admin 路径测试 | 通过；不能声称完整无条件通过 |
| `go vet ./...` | 通过 |
| 默认根路径前端构建 | 编译通过；与当前 `/janus/` embed 的 parity 检查不一致 |
| `JANUS_UI_BASE_URL=/janus npm --prefix frontend run build` + `verify-embedded-ui.sh` | 通过，匹配当前 embed |
| 远端 CI、目标 Linux、漏洞扫描、真实 Nacos、压力与 24h soak | 本轮未执行，不沿用旧文档的通过结论 |

两个未关闭的 Admin 测试是：

- `TestEmbeddedConsoleIsServed`；
- `TestEmbeddedConsoleCanMountBelowBuildBaseURL`。

本轮 race 验证的准确命令（省略本机 GOCACHE 路径）是：

```sh
go test -race ./... -skip '^TestEmbeddedConsole(IsServed|CanMountBelowBuildBaseURL)$'
```

当前工作区已包含用户构建的 `/janus/assets/...` 和配套 JS；测试硬编码查找 `/assets/...`。默认 `go test` / `go build` 也不会自动携带 `build-app.sh` 使用的 `-X janus/internal/admin.uiBaseURL=/janus`。因此需要把“前端 base、Go 挂载路径、测试输入、发布产物”作为一套构建契约修复。不能只修改正则让测试变绿，也不应在本次评估中覆盖用户已有的定制 embed。

## 5. 分阶段执行计划

以下是单人熟悉现有代码后的粗略投入估计，按工作日计算，不是上线承诺。建议预留约 3–4 周，实际取决于目标机准备、发现的问题及验收协议范围。每阶段以退出条件完成为准。

### 阶段 A：恢复可信的发布基线（约 2–3 天，P0）

**A1. 合入与复核本轮修复**

- 复核 ForwardAuth 的身份头边界与后端使用约定；将对应回归用例加入发布检查。
- 独立审阅压缩与 metrics 改动，确认没有改变配置格式。
- 将本轮源码修复与用户已有前端产物改动分别形成可审阅的提交。
- 退出条件：新旧测试通过，无相关 race；使用者知道 ForwardAuth 缺失身份头现在会被删除。

**A2. 统一 UI base path 构建契约**

- 默认 `/` 和定制 `/janus/` 两种构建配置都要覆盖。
- 构建脚本向前端和 Go 服务端传递同一个 base，并在校验中记录这个参数。
- 测试从页面实际引用的资源 URL 发起请求，验证资源可访问；不能手工补前缀来掩盖页面错误。
- 浏览器验证页面加载、JS/CSS、登录 Cookie Path、API、EventSource、登出和刷新。
- 定制产物在暂存目录生成，避免一次本地 build 改写默认发布 embed。
- 退出条件：两种 base 均端到端通过；正常发布检查不需要 `-skip`。

**A3. 固定候选版本与证据**

- 使用 CI 声明的 release toolchain；根据官方补丁信息与实际漏洞扫描决定是否更新，不把旧扫描结果写成当前安全结论。
- 记录 commit、工作树是否干净、Go/Node 版本、UI base、构建参数、二进制 SHA-256、配置摘要。
- 归档 CI 日志和构建产物；先确认 CI 实际成功，而非只存在 workflow 文件。
- 退出条件：干净 checkout 可构建相同功能产物；完整 test/race/vet/fuzz/mod verify 与漏洞扫描通过或有明确审阅结论。

### 阶段 B：边界与故障专项（约 4–6 天，P0/P1）

2026-09-20 更新：本地 B1–B4 实施与验收已完成，结果、兼容性和运维边界见 [阶段 B 验收记录](phase-b-completion-2026-09-20.md)。下面保留原始任务定义。

**B1. 身份与外部认证依赖（优先）**

- ForwardAuth：缺失身份头、正则过滤、拒绝响应、超大/慢响应、forward body、HeaderField 和 hop-by-hop 头处理。
- JWT：有效/无效签名、未知 kid、key rotation、缓存过期、认证服务器失联与恢复。
- 特别检查 `jwtVerifier.refresh`：当前持有 `cacheMu` 执行网络请求，且请求未使用入站 Context。已有 client timeout，但排队验证请求可能等待多个刷新周期；缓存过期后失败刷新没有独立退避。这里是代码审查发现的风险，尚未进行并发故障复现。
- 建议设计：并发刷新合并、失败退避、等待者可响应各自 Context 取消；继续遵守旧 key 最大可用期限，未知 key 不放行。
- 退出条件：认证依赖变慢时，请求在声明的预算内结束；外部请求数有界；恢复后正常；身份伪造回归通过。

**B2. HTTP 中间件组合与文件边界**

- 按组合验证 `compress / buffer / headers / cors / timeout`：首次最终状态码、1xx、Range/206、HEAD/204/304、Content-Length、Vary、Flush、客户端取消与错误中止。
- 已修复 gzip q=0，但压缩模块仍需完整响应契约审核。例如多次 WriteHeader 和 Range 响应压缩需要专门用例；本轮未修复这些后续项。
- `gateway/static.go` 目前采用 EvalSymlinks/Stat 后再 Open；路径检查和打开之间存在窗口。若允许其他进程修改静态目录，需评估用 `os.Root` 等有目录约束的文件访问方式消除竞争；现有 symlink 静态测试不能证明并发修改安全。
- 明确 rate-limit 状态是否应跨 generation 保留，以及高基数客户端导致 O(max_keys) 淘汰时的 CPU 预算；本轮不改变策略语义。
- 退出条件：每个修复都有失败前可复现的用例；不通过添加一个新开关隐藏契约问题。

**B3. 真连接上的热更新回归**

| 场景 | 必须观察到的结果 |
| --- | --- |
| H1 Keep-Alive 完成请求后 reload | 同一连接 ID；第二个请求命中新代 |
| HTTPS H1 同连接 reload | TLS 握手计数仍为 1；后续请求用新代 |
| H2 一条连接上慢流 + reload + 快流 | 慢流保持旧代，快流用新代；无因路由 reload 发出的 GOAWAY |
| SSE 持续输出时 reload | 原流不中断且保持旧代，新请求用新代 |
| 证书轮换 | 旧连接继续用已有会话；新连接完整握手看到新证书 |
| 相同后端地址 reload | `httptrace` / 后端 ConnState 证明出站 Transport 可以复用连接 |
| upstream 地址改变 | 后续请求选择新 target；不误复用到旧 target |
| 无效候选 / retired 上限 | 已发布代保持可用，拒绝原因明确，资源不泄漏 |

退出条件：断言连接/握手身份与后端命中，不只检查响应 200；所有涉及并发的新增测试通过 race。

**B4. 控制面一致性与持久化**

- 浏览器草稿编辑期间到达远端更新；加载响应延迟到达；发布中的编辑；两个管理员同时发布。
- `useConfig.load()` 当前只在发起 fetch 前检查 dirty；补回包期间用户开始编辑的测试，避免旧响应覆盖草稿。这是静态审查线索，尚未浏览器复现。
- SQLite / 配置文件 / Runtime 发布三者在落盘失败、进程退出后的恢复；保留现有原子写与版本冲突机制。
- 管理登录补拒绝爆破的速率/并发策略；规定 TLS 终止位置与 Cookie Secure 行为，不能信任任意客户端 X-Forwarded-Proto。
- 退出条件：不丢草稿；冲突返回 409 并保留本地内容；崩溃后 active 与实际文件可解释；备份可恢复。

### 阶段 C：目标 Linux 部署和故障演练（约 2–3 天，P1）

- 准备真实目标 VM：记录发行版、内核、CPU/RAM、架构、文件描述符限制、磁盘和网络拓扑。
- 运行 `scripts/verify-linux.sh` 与 pinned 扫描；保留 Linux-only SIGTERM / H3 子进程测试证据。
- 按 `deploy/systemd/README.md` 非 root 安装；验证证书、配置库目录权限和 CA roots。
- 让 readiness 先撤销、停止新准入，再完成 drain；验证超过预算后 force-close，进程退出后端口释放。
- 部署预算必须对齐：`TimeoutStopSec` 要覆盖总 drain 时间；当前示例存在可配置的小时级请求/流预算，不能只沿用默认 35 秒 systemd 设置。
- 故障注入：后端拒绝连接、响应头迟迟不来、body 中断、慢读客户端、慢上传、Nacos 失联/空列表/恢复、证书非法更新、磁盘写失败。
- HTTP/3 若启用：单独验证 UDP 丢包、防火墙、Alt-Svc 和 TCP 回退；不能从 H2 的成功推断 H3 的成功。
- 退出条件：故障后恢复可验证，资源回落，信号与进程边界行为有日志和客户端结果支撑。

### 阶段 D：容量、可观测性和 24h soak（约 3–5 天，P1）

**开始前填写工作负载表**

| 输入 | 必须记录的值 |
| --- | --- |
| 流量 | 稳态/峰值 RPS；短请求并发；SSE/WS 活跃连接数 |
| 数据 | 请求/响应典型与上限大小；上传下载比例；是否 buffer/compress |
| 后端 | 平均/p99 延迟、实例数、故障率、TLS 比例 |
| 配置 | Route 数、middleware 组合、reload 频率、证书轮换频率 |
| 环境 | CPU/RAM、容器或 VM 限额、网络 RTT、文件描述符 |
| 目标 | 允许的新增 p95/p99 延迟、错误率和恢复时间；由实际业务设定 |

- 用独立压测机先测直连 backend，再测 Janus；分别记录网关与压测器资源。
- `janus-loadtest` 的固定 concurrency 模式不要当成固定到达率；需要固定 rate 场景，并同时看 dropped、started、completed。
- 延迟分位数来自粗粒度桶，只适合初步观察；若用于严格的 p99 放行阈值，应增加高精度分布或外部测量。
- SSE/WS 使用协议感知客户端，加入流心跳、慢读、并发普通 API；当前工具只覆盖普通 HTTP。
- 采集 CPU、RSS、heap、goroutine、FD、连接数、in-flight、503/429、502/504、reload 成败、后端健康与 discovery 状态。
- 按稳态、峰值、超载、恢复测试，之后跑 24h 混合负载；期间周期性 reload 和证书轮换。
- buffer 的内存至少按“同时缓冲请求数 × 最大缓冲体积”预算，再加连接、TLS/QUIC 和 Go 堆余量。
- 退出条件：达到预先约定的 SLO；过载可控且恢复；最后稳定窗口资源没有持续单向增长；保留原始数据而不是只保留截图。

### 阶段 E：小流量灰度与回滚（约 2–3 天观察，P1）

- 选定一个非核心服务、流量入口、负责人、已知良好旧版本与配置。
- 给出初始流量比例、观察窗口和自动/人工回滚阈值，再开始流量切换；本计划不替用户执行生产切流。
- 建议从很小比例开始，只有延迟、错误与资源满足阈值才逐级扩大；具体比例取决于入口能力和业务风险。
- 实际回滚一次：流量回旧实例、新实例 drain、版本/配置恢复、readiness 和后端结果校验。
- 退出条件：灰度窗口通过，回滚时间满足目标，操作手册可由另一个人照做。

## 6. 可以直接拆成任务的顺序

| 顺序 | 任务 | 交付物 | 前置 |
| --- | --- | --- | --- |
| 1 | A1 + A2：修复审阅、双 base 发布链 | 回归用例、构建矩阵、无 skip 的测试结果 | 无 |
| 2 | B1：ForwardAuth/JWKS 故障边界 | 故障测试和有界刷新实现 | 1 |
| 3 | B3：Keep-Alive/TLS/reload 真连接证明 | 协议验收矩阵与集成测试 | 1 |
| 4 | B2 + B4：响应组合和控制台可靠性 | 中间件回归、浏览器测试、恢复演练 | 1 |
| 5 | A3 + C：固定候选并部署 Linux | 构建证据目录和 Linux 报告 | 2–4 |
| 6 | D：压力、指标、soak | 明确的容量边界、监控和 24h 数据 | 5、工作负载表 |
| 7 | E：灰度回滚 | 首次发布记录与回滚记录 | 6 |

最先开始的具体任务应是第 1 项，随后优先做鉴权依赖故障与真实连接热更新。Linux VM 和独立压测机可以同时准备，不必等代码任务全部结束。

## 7. 暂缓扩展

- 自动重试：先定义幂等性、请求体能否重放、响应是否已提交、重试次数与总预算，否则可能重复业务写入。
- 熔断和 outlier detection：先根据健康检查与故障数据证明需求，再定义与现有 Admission 的关系。
- gRPC / extended CONNECT / 任意 TCP/UDP：需要独立协议契约，不由 HTTP/2 已启用自动获得。
- 新插件体系、更多 Provider、多租户策略平台：先完成首个环境验收，避免扩大维护面。
- 路由树微优化：没有 profile 与目标负载前，不应把它排在正确性和故障恢复前面。

## 8. 参考

- 当前代码：`internal/runtime/runtime.go`、`internal/limen/limen.go`、`internal/gateway/gateway.go`、`internal/middleware`、`internal/admin`、`internal/store`、`frontend/src/useConfig.ts`。
- 原验收基线：[production-readiness.md](./production-readiness.md)。其中历史结果属于各自当时的版本和环境。
- 请求栈分析：[request-stack-keepalive-reload.md](./request-stack-keepalive-reload.md)。
- ForwardAuth 头映射参考：[Traefik 官方文档](https://doc.traefik.io/traefik/v2.11/middlewares/http/forwardauth/)；其 regex 模式明确要求先清除选中头。Janus 的本地信任边界以自己的实现与回归测试为准。
- gzip 协商：[RFC 9110 §12.5.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-12.5.3)。

本次是基于代码检查、局部失败复现和自动测试的工程评估，尚未覆盖全部浏览器交互、真实注册中心和目标生产网络。
