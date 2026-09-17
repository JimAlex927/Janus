# Janus 源码阅读路线

目标：你能解释每个函数为什么存在、谁调用它、状态归谁所有，以及出错后如何收尾。
按执行路径阅读；启动构建发生一次，请求处理发生很多次，reload 构建新的一代。
暂时没有完成目标 Linux 验证、容量测试和灰度验收，因此当前代码仍是待验收版本。

## 每次怎么读

每次选一个函数或 30–80 行，贴出文件和函数名，要求按以下顺序讲解：

1. 这段在启动、请求、reload、停机中的哪一步；上一层是谁，下一层是谁。
2. 输入、输出、每个局部变量和字段的用途。
3. 按行解释，包括 Go 语法、闭包捕获、接口动态分派、defer 的实际执行时刻。
4. 标注每个资源的创建者、共享范围、释放者；标注每把锁保护的数据。
5. 用一个具体请求演算成功、取消和失败三条路径。
6. 找到对应测试；先预测结果，再运行；最后用自己的话复述。

可直接使用的提问：

> 从 cmd/janus/main.go 的 main 开始，每次只解释一个完整函数。先说明它在整体流程中的位置，再逐行解释。遇到调用先说明契约，不立即递归展开。给我一个可以验证理解的小问题，等我回答后再继续。

维护一份个人笔记：变量含义、调用关系、资源归属、不确定的问题。
把调试断点放在“构建 handler”和“调用 ServeHTTP”两处，观察它们发生的次数。

## 推荐顺序

| 次序 | 文件 / 入口 | 这一段要能回答的问题 |
| --- | --- | --- |
| 1 | cmd/janus/main.go：main、run | 参数、日志、signal context 从哪里来？启动失败谁清理？先概览 run，然后分段读。 |
| 2 | internal/config/config.go、settings.go、effective.go | JSON 如何变成配置？默认值、校验、文件快照 hash 的区别？哪些配置只能重启？ |
| 3 | internal/runtime/runtime.go：New、NewWithBuilder | transport、admission、metrics 和 generation 分别属于进程还是某一代？ |
| 4 | internal/gateway/gateway.go：NewWithTransportAndLimiters | service 和 route 的 handler 如何在启动时组装？为什么 Handler 不含全局 middleware？ |
| 5 | internal/middleware/middleware.go：Middleware、Chain | 函数接收 handler 再返回 handler 是什么？反向构建为什么保持声明顺序执行？ |
| 6 | internal/limen/limen.go：NewBinding、Listen、ListenPacket、Serve | TCP/TLS/H2/QUIC 如何变成同一个 ServeHTTP？监听器与请求的生命周期有什么不同？ |
| 7 | internal/runtime/runtime.go：ServeHTTP、dispatch、acquire、release | 请求在哪一刻绑定 generation？同一连接上的后续请求如何选择新的一代？ |
| 8 | internal/middleware：observer、admission、timeout、stream_timeout、streaming | 每层调用 next 前后做什么？ResponseWriter 怎么透传能力？context 取消为什么不等于网络写中断？ |
| 9 | internal/router/router.go、internal/protocol/context.go | Limen、协议、host、路径如何匹配？匹配成功后究竟调用哪个 handler？ |
| 10 | internal/middleware：buffer、BodyLimit、RouteMetadata | 什么会提前结束请求？什么时候还能改状态码？临时 1xx 与最终响应如何区别？ |
| 11 | internal/proxy/proxy.go、internal/upstream/pool.go | 选后端、重写 URL/头、复用连接、转发响应、错误映射如何衔接？ |
| 12 | internal/forwarding、internal/health | 哪些身份信息可信？主动健康探测与业务请求是否共享生命周期？ |
| 13 | internal/runtime：Replace、reloader；internal/limen：cert_reloader、tls_asset | 候选构建、发布、失败回退、旧代退休分别何时发生？hash 是否对应真正应用的字节？ |
| 14 | cmd/janus/main.go 的停机分支；Limen.Shutdown、Close；Runtime.Close | readiness、停止接入、排空、超时强制关闭的顺序是什么？谁等待谁？ |
| 15 | internal/admin、internal/telemetry、pkg/logger | 日志/指标何时记录？基数如何限制？诊断会不会阻塞请求或泄露数据？ |
| 16 | 对应 *_test.go、.github/workflows/ci.yml、deploy/systemd | 断言验证了什么？客户端超时与服务端主动关闭如何区别？部署预算与代码预算是否一致？ |

## 可以先画在纸上的调用链

启动：配置快照 → Runtime → generation / Gateway → route & service handlers → Limen。

请求：Limen → 全局 observer / protocol guard / admission / timeout → dispatch
→ 当前 generation 的 Router → route middleware → service middleware → ReverseProxy → backend。

返回：backend → ReverseProxy 写 response → middleware 退出 → release generation → observer 记录结果。
流式响应的头和 body 在 handler 返回以前就可能发送给客户端；不能把“函数返回”当成“开始发送”。

reload：读取并验证快照 → 构建候选 → 原子发布 → 新请求用新代 → 旧请求归还引用 → 释放旧代。

## 阅读时保留的四个关键反例

- listener 的 Serve 返回，不代表已接收的请求都完成了。
- context 取消，不自动解除所有阻塞 I/O。
- 磁盘当前内容的 hash，不代表运行中的配置或证书已经应用了它。
- 103 Early Hints 不是最终响应；发送过 103 后仍可能需要返回 504。

对应回归集中在 internal/limen/lifecycle_regression_test.go、stream_proxy_regression_test.go、
internal/runtime/startup_snapshot_test.go；Linux 真实进程信号测试位于 cmd/janus/lifecycle_linux_test.go。
