# Limen 双向 TLS（mTLS）

首次使用请阅读 [证书与 mTLS 使用说明书](certificate-user-guide.zh-CN.md)：包含构建、三步签发、Windows/macOS 导入、管理台配置和故障排查。

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
→ 填写服务器上的 Client CA file → 确认草稿 → 发布。发布会写入完整配置文件。
这里只填写服务器文件路径，不上传私钥。缺少 CA 路径时表单确认/本地配置校验会报错。
关闭 mTLS 会移除该草稿的 `client_ca_file`；关闭 TLS 会移除整个 TLS 草稿配置。

**发布后监听器仍未启用 mTLS。**写入配置文件后需要重启 Janus。
发布时会按启动文件的相对路径规则检查服务器证书、私钥和 Client CA 内容；无效时拒绝写文件。
发布后仍需重启 Janus，先准备好客户端证书再切换。
重新构建部署二进制时要保留原管理台前缀：

```sh
JANUS_UI_BASE_URL=/janus JANUS_OUTPUT=./janus ./scripts/build-app.sh
```

## 独立生成 CA，再签发证书

证书工具不依赖 Janus 配置，不读取 `-config`，也不启动网关。先生成 CA，再显式使用它签发服务器或设备证书。
同一个私有 CA 可以签发两种证书；如果需要隔离用途，也可以自己生成两个 CA，签发时选择不同路径。

以下命令适用于已经重新构建的 `./janus`。也可以用 `go run ./cmd/janus` 替换 `./janus`；
Windows PowerShell 使用 `.\janus.exe`，路径参数可以使用正斜杠。无需安装 OpenSSL。

```sh
# 1. 只生成 CA，不生成服务端或客户端证书
./janus cert ca --out .local/pki --name "Janus private CA"

# 2. 使用这个 CA 签发服务端证书
./janus cert server --ca .local/pki/ca.crt --ca-key .local/pki/ca.key --hosts "39.104.66.49,127.0.0.1,localhost" --out .local/pki/server

# 3. 使用同一个 CA 签发客户端证书及带密码的 P12 导入包
./janus cert client --ca .local/pki/ca.crt --ca-key .local/pki/ca.key --name jim-mac --out .local/pki/clients/jim-mac
```

### hosts 与 name 的区别

- `--hosts` 仅用于服务端证书，写入 SAN。它是**浏览器 URL 中的主机名或 IP**，不含协议、端口、路径。
  访问 `https://39.104.66.49:44091/vault/` 时需要 `39.104.66.49`，不需要 `44091` 或 `/vault/`。
  多个地址用逗号分隔；监听地址 `0.0.0.0` / `::` 不是访问地址。
  客户端验证时，访问地址必须匹配 SAN。仅有 `localhost` 的证书不能用于公网 IP。
- `--name` 是证书的显示标识（Subject CN）。CA 可以叫 `Janus private CA`，设备可以叫 `jim-mac`。
  客户端 name 不是系统用户名、不要求与真实电脑名相同，也不是应用登录或授权规则。
  当前 Janus 验证证书链和用途，并不根据 name 做账号映射或白名单授权。
- `--out` 决定文件放在哪里。客户端 name 不决定目录，不会从 name 推导文件路径。
  添加第二台设备时选择不同的输出目录，例如 `--name jim-phone --out .local/pki/clients/jim-phone`。

### 输出与管理台字段

这里的格式叫 **PEM**，不是 perm。`.crt` / `.pem` 只是扩展名，生成的 `ca.crt`、
`server.crt`、`client.crt` 都是 PEM 公共证书。

| 文件 | 用途 / 对应字段 |
| --- | --- |
| `pki/ca.crt` | CA 公共证书：设备用于信任服务器，Limen 的 `client_ca_file` 用于验证设备 |
| `pki/ca.key` | 签发私钥，管理员保管，网关运行及访问设备都不需要它 |
| `pki/server/server.crt` | Limen 的 `cert_file` |
| `pki/server/server.key` | Limen 的 `key_file`，只放服务器 |
| `pki/clients/jim-mac/client.crt` / `client.key` | 设备的证书和独立私钥，适合程序客户端 |
| `pki/clients/jim-mac/client.p12` | 设备证书、设备私钥及 CA 公共证书的加密导入包 |
| `pki/clients/jim-mac/client-password.txt` | 随机导入密码，单独安全传递，不提交 Git |

用上述同一个 CA 签发后，管理台 **Client CA file 填 `ca.crt`**，不要填客户端叶证书、私钥或 P12。
例如配置文件在项目的 `configs/` 目录时，TLS 配置为：

```json
{
  "cert_file": "../.local/pki/server/server.crt",
  "key_file": "../.local/pki/server/server.key",
  "client_auth": "require_and_verify",
  "client_ca_file": "../.local/pki/ca.crt"
}
```

**生成命令的路径相对于当前终端目录；网关配置中的相对路径则相对于配置文件目录。**
证书生成后再手动填写管理台/配置文件、检查并重启；工具不会改配置或安装系统信任。
每种证书有独立随机生成的私钥，不是从 CA 私钥派生；CA 只负责签名。
新 CA 默认为 3650 天，叶证书为 365 天，可用 `--days` 指定，叶证书不会超过 CA 的到期时间。
签发需要已存在、匹配且有效的自签名根 CA 和私钥；CA 缺失时不会偷偷新建一个。

现有文件一律拒绝覆盖；需要续签时输出到新目录，验证后再安排替换，不要删除仍在使用的 CA。
新建 POSIX 文件使用 0600、目录使用 0700。Windows 上应使用私有目录和 NTFS ACL，
文件权限位不能代替 ACL。P12 密码文件与未加密 PEM 私钥都属于敏感文件。
P12 使用 [SSLMate go-pkcs12](https://pkg.go.dev/software.sslmate.com/src/go-pkcs12) 的固定
`Modern2023` AES/SHA-256 配置及 192-bit 随机密码；密码不打印在终端。

原先提交的 `-init-tls -config ...` 仅作为旧版便捷入口保留；新的独立流程使用 `cert` 子命令。
之前未发布的 `-init-mtls` 草案已移除。

## 客户端与验证

每个设备应使用独立的 clientAuth 证书和私钥，可通过带密码的 PKCS#12 (`.p12`) 包安装。
签发客户端证书应使用自己控制的私有 CA，避免把宽泛的公共 CA 当设备访问白名单；可以与服务端共用私有 CA，也可按需要分开。
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
发布包含 Limen 变更的配置会写入文件，但当前监听器继续使用原有策略；重启后才应用新策略。

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
- 配置库测试确认：保存保留 mTLS 字段，发布写入配置文件但不改变当前监听策略，并返回需重启提示。
- 测试均在临时实例执行，未开启真实 Vault 入口的 mTLS，也未替换正在使用的部署二进制。
