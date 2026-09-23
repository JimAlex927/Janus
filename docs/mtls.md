# Limen 双向 TLS（mTLS）

普通 HTTPS 由客户端验证服务器；mTLS 还要求服务器验证客户端证书。
Janus 在 Limen 的 TLS 握手层执行认证，不是 Route/Gateway 中间件。
未通过认证的连接不会进入 HTTP handler，因此不会返回 HTTP 401/403。
HTTP/1.1、HTTP/2 和 HTTP/3 共用同一客户端信任策略；HTTP/3 仍需 UDP 转发。

## 配置

在目标 Limen 的 `tls` 下增加：

```json
{
  "cert_file": "../certs/server.crt",
  "key_file": "../certs/server.key",
  "min_version": "1.2",
  "client_auth": "require_and_verify",
  "client_ca_file": "../certs/client-ca.pem"
}
```

- `client_auth`：省略、空字符串或 `none` 为普通 HTTPS；`require_and_verify` 强制验证客户端证书。其他值拒绝加载。
- `client_ca_file`：PEM 格式的客户端信任 CA 公共证书 bundle，可包含多个 CA；不是私钥，也不是设备叶证书。相对路径按配置文件目录解析。
- 开启认证却缺少 CA 路径、关闭认证却保留 CA 路径，均拒绝配置，以免静默忽略认证意图。
- 启动时读取 CA（最大 1 MiB），拒绝空文件、缺失文件、非证书块、损坏内容、非签发 CA、尚未生效/过期的 CA。只接受 PEM 证书块及块间空白。
- TLS 标准验证负责客户端链、有效期、签名及 clientAuth 用途。不要把 `RequireAnyClientCert` 当作此功能的替代品。
- 有效配置诊断视图显示 `client_auth`，不输出 CA、服务端证书或私钥文件路径。

作用范围是整个 Limen。握手发生在 HTTP 路径可用之前，不能在此层仅对 `/vault` 要求客户端证书。
建议让 Vault 使用独立 Limen；管理台保留独立入口以免锁住管理通道。
本次仅增加功能，不自动修改当前 Vault 部署、生成设备凭据或开启强制认证。

## 管理台

编辑 Limen → 开启 TLS → “客户端证书认证（mTLS）”选择“开启 · 强制验证客户端证书”
→ 填写服务器上的 Client CA file → 确认草稿 → 保存 → “写入文件（需重启生效）”。
这里只填写服务器文件路径，不上传私钥。缺少 CA 路径时表单确认/本地配置校验会报错。
关闭 mTLS 会移除该草稿的 `client_ca_file`；关闭 TLS 会移除整个 TLS 草稿配置。

**保存/发布不代表监听器已经启用 mTLS。**写入配置文件后需要重启 Janus。
配置校验只检查字段，文件内容由新进程启动时检查；先准备好服务器文件和客户端证书，再切换。
重新构建部署二进制时要保留原管理台前缀：

```sh
JANUS_UI_BASE_URL=/janus JANUS_OUTPUT=./janus ./scripts/build-app.sh
```

## 客户端与验证

每个设备应使用独立的 clientAuth 证书和私钥，可通过带密码的 PKCS#12 (`.p12`) 包安装。
签发客户端证书建议使用独立私有 CA，避免把宽泛的公共 CA 当设备访问白名单。
信任服务端 CA 与安装客户端证书是两个独立步骤；只有公共 CA 证书不能证明客户端身份。
CA 私钥不放到访问设备，也不提交 Git。mTLS 不替代 Vault 登录，不自动把证书映射为应用账号。

```sh
# 必须成功：使用获准设备证书，并验证服务端身份。
curl --cacert server-root-ca.pem --cert device.crt --key device.key \
  https://gateway.example:8443/vault/

# 必须握手失败：未携带客户端证书。
curl --cacert server-root-ca.pem https://gateway.example:8443/vault/
```

浏览器“忽略服务端证书警告”不能绕过服务端的客户端证书验证。
客户端私钥可以复制，因此 mTLS 限制的是凭据持有者，不保证绑定物理设备。

## 重启、证书轮换与 keep-alive

`client_auth`、`client_ca_file` 路径和 **CA 文件内容**都是启动时快照；第一版不支持客户端信任热更新。
现有证书轮换器仍只轮换服务端证书/私钥，不会重读客户端 CA，也不会修改运行中的认证模式。
修改路由的热发布不能改变这层策略，尝试改变 Limen 启动配置会被拒绝。

已认证的 HTTP keep-alive / HTTP/2 连接可以继续复用，后续请求不会重新握手，也不会逐请求重新检查证书过期。
HTTP/3 同样在已有 QUIC 连接上复用。TLS 会话恢复仍由 Go TLS 实现处理，不等于逐请求认证。
当前未实现 CRL/OCSP、单设备吊销列表或证书身份授权规则；删除某个本地证书文件不会撤销已签发凭据。
紧急切换信任时需要更新 CA 并重启/关闭旧连接；同一 CA 下撤销单个设备需要另外的吊销设计。

测试覆盖正常/失败握手、H1 TLS1.2/1.3、H2、H3、keep-alive、配置校验、启动信任文件校验及禁止热修改。

## 本轮验收（2026-09-23）

- 完整 `go test -race ./...`、`go vet ./...`、前端构建及内嵌资源一致性检查通过。
- 独立测试二进制保留 `/janus` 前缀，管理台资源、登录、Cookie、API、SSE、退出冒烟检查通过。
- Playwright 实际操作确认：默认普通 HTTPS；启用 mTLS 显示 CA 字段；缺失 CA 时阻止确认；
  填写后可保存，刷新重新打开仍正确回显；关闭 mTLS 隐藏 CA 字段。
- 配置库测试确认：保存保留 mTLS 字段，发布路由不改变监听策略，写入文件返回需重启提示。
- 测试均在临时实例执行，未开启真实 Vault 入口的 mTLS，也未替换正在使用的部署二进制。
