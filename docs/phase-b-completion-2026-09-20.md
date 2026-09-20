# 阶段 B：边界与故障专项验收

日期：2026-09-20。基线：`acbc224` 加本轮工作区修改。范围对应
[下一阶段计划](next-stage-plan-2026-09-20.md) 的 B1–B4；不是 C/D/E 的生产放行。
环境：macOS arm64 / Apple M1，Go 1.26.8，Node v26.7.0。
本轮未 commit、push，也未修改用户的示例部署配置或原有独立二进制。

## 1. 完成范围

| 专项 | 实施与验收 |
| --- | --- |
| B1 外部认证 | ForwardAuth 请求/拒绝响应体、慢响应、取消、自定义客户端超时、动态 hop-by-hop 头；JWKS 畸形/空/超大/重复 key、慢 TLS 服务、失败退避；保留前一批 rotation/stale/恢复回归 |
| B2 HTTP / 文件 / 限流 | 响应组合矩阵、首个最终状态、103、Range/206、HEAD/204/304、Vary/Content-Length、Flush 错误、Trailer、超时/中止/取消；静态文件改为目录句柄约束；LRU 淘汰 O(1) |
| B3 真连接 reload | 保留 H1/HTTPS/H2/SSE/upstream 复用回归；新增真实证书轮换、失败候选及 retired 上限下的连接与资源回收 |
| B4 控制面 | 浏览器延迟请求/发布/编辑冲突、记录级版本检查、串行化控制面写入、文件失败和进程退出恢复、SQLite 事务失败/备份恢复、登录预算与显式 Secure Cookie |

## 2. 关键行为与修复

### B1：认证失败不能放行，也不能无限等待

- ForwardAuth 使用入站 Context 派生独立外部请求期限；即使调用者传入没有 Timeout 的 `http.Client`，配置的认证超时仍生效。
- 拒绝响应先完整读取有界 body，成功后才提交头和状态；慢/损坏/超限 body 返回 502，不泄漏上游 Content-Encoding 等头。正常认证拒绝仍保留其状态和允许转发的响应。
- 转发请求以及认证返回值都剥离 `Connection` 动态指名的 hop-by-hop 字段；身份头缺失时清除客户端旧值，HeaderField 使用规范化名称。
- forward body 仍有大小限制，转发认证后业务能读取原始内容。**入站上传读取**仍由网关 read/request 预算负责，不能把外部认证 Timeout 当成整个上传期限。
- JWKS 延续上一批单次刷新、可取消等待、5 秒刷新期限/失败退避、正常缓存 5 分钟、最大 stale 15 分钟、未知 kid 冷却 30 秒；失败不延长旧 key 寿命。

测试：`internal/middleware/auth_boundary_test.go`、`forward_auth_test.go`、
`jwt_test.go`、`jwt_refresh_test.go`。测试使用真实本机 HTTP/TLS 服务与故障响应。

### B2：响应契约与资源边界

压缩修复前已复现：先写 201/206 再写 500 会覆盖首个最终状态；Range 请求可能被压缩；identity 响应缺少协商 Vary。
现在冻结首个最终响应头和状态；103 不占用最终状态；正文嗅探使用压缩前内容。
HEAD、204、304、206、Range、已有编码、SSE、`no-transform` 不压缩。
gzip 删除原 Content-Length/Content-MD5/Accept-Ranges，强 ETag 转弱，保留并追加 Vary。
`ResponseController.Flush()` 可以获得真实 Flush 错误。

Buffer 保留外层头、冻结最终头并保留 Trailer；HEAD 不储存正文；204/304 拒绝 body。
Buffer 的 Flush 仍为 no-op，1xx 被有意抑制：它是有限 API 响应策略，不支持中途发包；SSE/WS 绕过它。
超时/异常中止/超限不得泄漏部分业务响应；客户端取消不再提交缓冲结果。
组合测试覆盖四种 wrapper 顺序、五种最终状态以及 GET/HEAD，另有真实 TCP 的 103 和 Trailer 验证。

静态文件不再 `EvalSymlinks/Stat → os.Open`。每代持有 `os.Root`，相对打开且从同一文件句柄取得元数据并发送；目录列表/index/SPA fallback 也使用该根。
目录路径被替换后，存活代仍引用旧目录；新代重新打开。代退出及候选构建失败都会关闭根句柄。
并发 symlink 替换测试不能读出根外内容。绝对 symlink 现在会被拒绝，内部链接请使用相对形式；hard link/mount 和目录内容仍需可信运维控制。

RateLimit 使用 map + 双向链表，单次 LRU 淘汰 O(1)，空间 O(max_keys)。**明确保留原语义：每进程、每个中间件实例、每代独立；reload 会重置 token。**
key 被淘汰后再来会获得新的 burst。这是容量有界的流量保护，不是跨代、跨实例、抗身份轮换的严格用户配额。
登录额外有不依赖 peer key 的全局桶，因此 peer 高基数不能绕过全局预算。

本机 200ms 微基准（不等于生产吞吐）：

| max_keys | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| 1,024 | 241.2 | 144 | 4 |
| 65,536 | 332.2 | 144 | 4 |

测试：`response_contract_test.go`、既有 buffer/compress/timeout 测试、
`static_confinement_test.go`、`rate_limit_test.go`。

### B3：TCP/TLS 生命周期与配置代分离

证书测试通过真实 Limen HTTPS Listener：首个请求记录证书与连接；只替换证书导致 cert/key 不匹配时拒绝更新；旧连接仍工作；完整替换证书和 key 后，新连接看到新证书，旧连接继续同一 TLS 会话。
这补齐了前一批“HTTPS Keep-Alive reload 后握手数仍为 1”的证明。

retired 测试持有八个旧代的实际慢请求；下一次更新触发 `ErrRetiredLimit`，当前代仍可经 TCP 访问；放行慢请求后旧代资源回收。
无效候选被关闭，不替换当前代。既有 H2 流并行、SSE 存活和 upstream Transport 复用测试全部保留。

测试：`internal/limen/tcp_certificate_rotation_test.go`、
`internal/runtime/retired_connection_test.go`、`connection_reload_test.go`。
不据此推断 H3/UDP 或目标 Linux 已验收。

### B4：草稿、版本与持久化

1. `useConfig` 在发请求和收到响应之间检查编辑序号；过期响应不能覆盖新草稿，也不能错误推进其 revision。后发请求优先，busy 按在途数管理。
2. 发布固定提交快照；发布过程中新增的编辑继续保留为 dirty。409 保留草稿和基线。显式丢弃操作也不能覆盖点击之后产生的新编辑。
3. 实际 ConfigEditor 的保存/发布使用记录版本，并防重复提交；保存后只更新已提交的基线，不覆盖当前编辑。切换记录时隔离组件实例。
4. 浏览器复现并修复画布 `onSelectionChange` 不稳定导致的持续重渲染；缺失布局时以实际自动布局初始化基线，不再一打开就显示未保存。
5. `X-Janus-Record-Revision` 使用 `updated_at`；过时的保存或发布返回 409。Runtime 的 `X-Janus-Revision` 与它分别防止“记录被改过”和“线上已经换代”。发布直接返回自己提交的记录版本，不通过后续 GET 误认他人新版本。
6. 时间戳保存为固定九位小数 UTC，避免同秒更新只有秒级精度，也避免 RFC3339Nano 可变宽度破坏 SQL 字符串排序；打开旧库时事务化归一化已有合法时间，范围过滤使用相同格式。
7. 控制面修改共享单实例互斥锁，避免两个 Admin 发布的 SQLite active 标记反序提交。旧 API 客户端可以省略记录版本以保持兼容，但因此不具备草稿丢失更新保护。文件外部编辑不参与此锁；不支持多个 Janus 实例共同写同一配置/库。

**故障后的真相来源：启动配置文件。**

- 配置文件 rename 失败：候选不激活，Runtime 保持旧配置。
- Runtime/文件发布后，SQLite active 标记更新失败：既有 API 返回成功加 warning，表示流量已经切换，不能误称完全回滚。
- 子进程测试在真实 `publishConfig` 成功后直接退出，刻意跳过 `Store.Publish`、Close 和 WAL checkpoint；重新启动时按文件恢复 Runtime，并修正已有匹配记录的 active 标记。
- SQLite trigger 在归档旧 active 后使新 active 写入失败，验证事务回滚仍保留旧标记。
- `Store.Backup` 用 `VACUUM INTO` 获取含 WAL 的一致快照，目标必须不存在，权限 0600；恢复该快照可读出旧 active 和完整历史，且后续原库更新不污染备份。

登录策略与 HTTPS 终止配置见 [Admin Console](admin-console.md)：全局 60/min burst 20，peer 5/min burst 10，最多四个并发处理、4 KiB body、5 秒 body read。
peer 只用 RemoteAddr。Secure 只认真实 TLS 或 `settings.admin.cookie_secure`，不信任任意转发头。

测试：`internal/admin/control_boundary_test.go`、`internal/store/recovery_boundary_test.go`、
`cmd/janus/publication_recovery_test.go` 和 `frontend/tests/`。

## 3. 运维备份与恢复步骤

配置文件与 SQLite **不是同一个跨文件事务**。不要在持续发布中随手复制两个文件后声称它们来自同一时刻。

1. 安排维护窗口，停止管理发布和外部文件写入，正常停止 Janus，确认进程已经退出。
2. 将实际使用的配置 JSON、配置库 DB，以及仍存在的同名 `-wal`/`-shm` 一起复制到新的受限备份目录；另存二进制版本、证书/私钥的安全备份及路径清单。文件可能包含秘密，目录/文件仅允许运维账户访问。
3. 若由代码做在线 DB 快照，可以调用 `Store.Backup`，但备份配置 JSON 期间仍必须暂停所有发布。它只备份库，不自动锁住另一进程/文件编辑器，也不自动备份证书。
4. 恢复前再次停机。保留故障现场，使用**新的空目录**恢复成对备份，避免混入另一个数据库的旧 WAL；不要只覆盖正在使用的 DB 主文件。
5. 用匹配版本的 Janus 指向恢复配置启动，核对启动日志、readiness、实际 Runtime 配置及库的 active；存在不匹配 warning 时先排查，不能把 UI 标记视作流量已经回滚的证据。
6. 发实际业务请求后才重新开放管理写入。要回滚线上路由，必须发布/加载旧配置，单独更改 SQLite active 标记不够。

本轮恢复证明覆盖进程退出及注入写失败，不是断电、磁盘控制器缓存或文件系统损坏模拟；目标机故障演练属于 C。

## 4. 验证与复跑

执行完整 `go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...`、
`go mod verify`，均无 skip。配置与规则两个 fuzz 各 3 秒通过（27,691 / 433,589 次执行）。
前端 `npm --prefix frontend run build` 包括源码和测试夹具类型检查，
`scripts/verify-embedded-ui.sh` 校验生成物与 Go embed 一致。
测试夹具不会进入生产入口或生产 bundle。

本轮另外构建 `bin/janus-phase-b`，通过 `scripts/smoke-console.mjs` 的实际二进制
根路径资源、登录、Cookie、API、SSE、登出检查。该文件为本地测试产物，不是发布候选。

浏览器复跑：

```sh
npm --prefix frontend run dev -- --host 127.0.0.1
```

- 打开 `/tests/concurrency.html`，点击 Run concurrency regression；六个场景应显示 `ALL PASS`。
- 打开 `/tests/editor.html`：真实 ConfigEditor + 可控网络响应。初始必须已保存且无持续重渲染错误；进入 JSON，编辑/应用/保存，在点击 Complete save 前继续编辑，完成保存后新编辑必须保留为未保存。
- 再保存并点 Reject save with 409，必须显示保留本地草稿的提示且 JSON 不变。
- 保存干净草稿后发布，确认后在 Complete publish 前继续编辑并应用 JSON；发布结束后新内容仍为未保存，后续保存使用发布响应中的记录版本。

上述是 Playwright 实际 Chromium 验证，不只是源码推断；两管理员服务端同时发布由 Go 并发用例断言 200/409。
网络夹具用于确定性重排事件，不替代真实网络部署或双管理员长期使用。

内嵌前端更新前的旧 JS 和入口备份在 `/tmp/janus-b-embed-backup-inf5Fo`，属于临时备份。
本轮未改变依赖版本，未重跑漏洞扫描；上一批扫描结果只作为历史，不表示当前扫描已通过。
远端 CI、Linux/Windows 实机、真实 Nacos 故障、容量 SLO、24h soak 与灰度回滚没有在本轮执行。

下一阶段：按 C 在目标 Linux 上验证部署、停机 drain、故障恢复和网络协议边界，再进入 D 的容量与持续负载验收。
