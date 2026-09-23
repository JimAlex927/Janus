# Janus 证书与 mTLS 使用说明书

适用范围：`janus cert ca / server / client` 独立证书工具及 Limen 的 mTLS 配置。
本说明以私有 CA、TCP 穿透、公网地址 `https://39.104.66.49:44091/vault/` 为例。
请按自己的访问地址调整，不要照抄到不同部署。

## 1. 先理解三个角色

1. **CA**：签发机构。`ca.crt` 是公共证书，`ca.key` 是签发私钥。
2. **服务端证书**：网关向访问者证明服务器身份，必须包含正确的访问域名/IP。
3. **客户端证书**：访问设备向网关证明自己持有获准的凭据，mTLS 开启后必须提供。

同一个私有 CA 可以签发两类证书，无需强制使用两个 CA。需要隔离用途时，再自行建立两个 CA。
每种证书都有独立随机生成的私钥，**不是从 CA 私钥推导出来的**；CA 只负责签名。
服务端证书限定为 `serverAuth`，客户端证书限定为 `clientAuth`，不能互换。

这些命令不需要 `-config`，不读取 Limen、不启动网关、不修改系统信任或部署配置。
可以在单独的管理电脑签发，再将必要文件安全送到网关和设备。

## 2. 准备可执行文件

在仓库根目录操作。已经有包含 `cert` 子命令的新二进制时，可跳过构建。
构建需要仓库规定的 Go 工具链、Node.js 和 npm；生成证书本身不依赖 OpenSSL。

macOS / Linux：

```sh
JANUS_UI_BASE_URL=/janus JANUS_OUTPUT=bin/janus ./scripts/build-app.sh
./bin/janus cert --help
```

Windows PowerShell 7：

```powershell
$env:JANUS_UI_BASE_URL = '/janus'
$env:JANUS_OUTPUT = 'bin/janus.exe'
./scripts/build-app.ps1
.\bin\janus.exe cert --help
```

`JANUS_UI_BASE_URL=/janus` 保留现有管理台前缀；它是构建参数，不是运行参数。
以上构建输出到 `bin/`，不会自动替换根目录的旧 `./janus` 或重启正在运行的服务。
如果只想从源码调用命令，可把下文的 `./bin/janus` 换成 `go run ./cmd/janus`。
Windows 下把它换成 `.\bin\janus.exe`，其余参数不变；文件参数可使用正斜杠。

## 3. 三步生成证书

### 第一步：只生成 CA

```sh
./bin/janus cert ca --out .local/pki --name "Janus private CA"
```

输出：`.local/pki/ca.crt` 和 `.local/pki/ca.key`。本步骤不生成服务端或客户端证书。
CA 名称是显示名称，可以自行修改。CA 默认有效期为 3650 天，可用 `--days` 指定。

### 第二步：使用 CA 签发服务端证书

```sh
./bin/janus cert server --ca .local/pki/ca.crt --ca-key .local/pki/ca.key --hosts "39.104.66.49,localhost,127.0.0.1" --out .local/pki/server
```

输出：`.local/pki/server/server.crt` 和 `server.key`。
默认有效期 365 天，且不会超过签发 CA 的到期时间。

**`--hosts` 是访问地址，不是监听配置。**

| 浏览器访问地址 | 应包含的 hosts 项 |
| --- | --- |
| `https://39.104.66.49:44091/vault/` | `39.104.66.49` |
| `https://localhost:8443/vault/` | `localhost` |
| `https://127.0.0.1:8443/vault/` | `127.0.0.1` |
| `https://vault.example.com/` | `vault.example.com` |

多个域名/IP 用逗号分隔。不要填写 `https://`、端口、路径或通配监听地址 `0.0.0.0` / `::`。
这些名称写入证书的 SAN；访问地址不匹配时，客户端会报服务器证书名称错误。
工具目前不支持 `*.example.com` 这样的通配域名输入。

### 第三步：使用同一个 CA 签发客户端证书

```sh
./bin/janus cert client --ca .local/pki/ca.crt --ca-key .local/pki/ca.key --name jim-mac --out .local/pki/clients/jim-mac
```

输出四个文件：`client.crt`、`client.key`、`client.p12`、`client-password.txt`。

**`--name` 是客户端证书的显示标识（Subject CN）。** 可以叫 `jim-mac`、`jim-phone`。
它不是操作系统用户名，不要求等于电脑名，也不是 Vault 账号或权限规则。
当前 Janus 不按 name 做白名单授权；同一受信任 CA 签发、验证通过的客户端证书都可以通过这层认证。
输出目录由 `--out` 决定，不由 name 决定。

`client.p12` 是带随机密码的设备导入包，含客户端证书、客户端私钥和 CA 公共证书，**不含 CA 私钥**。
密码写在同目录 `client-password.txt`，不会打印到终端。密码文件也是敏感文件。

## 4. 文件保管与分发

`.crt` / `.pem` 是文件名扩展名；本工具生成的 `.crt` 使用 PEM 格式。不是 “perm 文件”。

| 文件 | 放在哪里 / 用来做什么 |
| --- | --- |
| `ca.crt` | 公共证书：访问设备信任服务端；网关用它验证客户端 |
| `ca.key` | 管理员签发区或安全备份；不要分发给访问设备，网关运行也不需要它 |
| `server.crt` | 网关：`cert_file` |
| `server.key` | 网关：`key_file`；不得给客户端 |
| `client.crt`、`client.key` | 对应设备：程序客户端/支持 PEM 的工具使用 |
| `client.p12` | 对应设备：导入其客户端身份 |
| `client-password.txt` | 管理员保管，按需单独安全传递导入密码 |

新建 POSIX 文件使用 0600、目录使用 0700；已有父目录权限不会被修改。
Windows 上应使用私有目录与 NTFS ACL，不能把 POSIX 权限位当成 ACL 保护。
不要将证书材料放进公共共享目录；私钥与密码不提交 Git。
仓库的 `.local/` 已被忽略，选用其他目录时需自行检查 Git 状态。

客户端私钥可以复制，因此 mTLS 认证的是凭据持有者，并不自动绑定物理设备。

## 5. 访问设备安装

必须完成两件不同的事情：**信任服务端 CA**，以及 **安装自己的客户端证书和私钥**。
只有 `ca.crt` 不能通过客户端认证，只有客户端 P12 也不能假设系统已信任服务端 CA。

### Windows

先核对 CA 指纹，再通过 Windows 证书管理工具将可信的 `ca.crt` 加入适当的受信任根证书存储。
不要导入来历不明的 CA；建立根信任会影响这台设备的证书验证。

客户端 P12 可使用证书导入向导，也可使用支持 PKI 模块的 Windows PowerShell：

```powershell
$deviceP12Password = Read-Host '输入 client-password.txt 中的密码' -AsSecureString
Import-PfxCertificate -FilePath '.local/pki/clients/jim-mac/client.p12' -CertStoreLocation 'Cert:\CurrentUser\My' -Password $deviceP12Password
```

该示例将设备身份导入当前用户的个人证书存储，不把密码作为命令行明文参数。
参考：[Microsoft Import-PfxCertificate](https://learn.microsoft.com/en-us/powershell/module/pki/import-pfxcertificate)。

### macOS

在“钥匙串访问”中导入 `client.p12`，输入对应密码。另行导入并核对 `ca.crt`，
按自己的信任策略设置 SSL 信任。浏览器需要时选择对应设备证书。
参考：[Apple 导入钥匙串项目](https://support.apple.com/guide/keychain-access/import-and-export-keychain-items-kyca35961/mac)。

浏览器使用的证书存储和选择行为可能不同；如果未出现证书选择，先检查客户端身份是否包含私钥，
再检查浏览器所使用的证书存储。手机等设备应另行签发自己的证书，不建议共用同一份私钥。

## 6. 配置 Janus

先给访问设备准备好客户端凭据，再启用强制认证，避免将自己挡在入口外。
建议 Vault 和管理台使用不同的 Limen，保留本机管理通道。

管理台操作：编辑目标 Limen → TLS → 客户端证书认证选择“强制验证” → 填写 Client CA file
→ 确认 → 保存草稿 → 写入文件 → 检查 → 重启。

使用同一个 CA 签发时，**Client CA file 填 `ca.crt`**，不是 `client.crt`、`.key` 或 `.p12`。
只修改目标 Limen 的 `tls` 部分，保留地址、协议、路由等其他配置。
假设配置文件位于项目的 `configs/` 目录，证书按本说明生成，示例为：

```json
{
  "cert_file": "../.local/pki/server/server.crt",
  "key_file": "../.local/pki/server/server.key",
  "min_version": "1.2",
  "client_auth": "require_and_verify",
  "client_ca_file": "../.local/pki/ca.crt"
}
```

路径规则必须区分：生成命令相对于**当前终端目录**；配置文件里的证书路径相对于**配置文件所在目录**。
漏掉上述 `../` 会去找 `configs/.local/...`。Windows 的 JSON 路径建议使用 `/`，或正确转义反斜杠。

先检查配置（不会开始监听），再在原运行终端停止旧进程并启动新构建：

```sh
./bin/janus -config configs/janus-admin.example.json -check
# 检查通过、停止旧进程后执行：
./bin/janus -config configs/janus-admin.example.json
```

Windows 使用 `.\bin\janus.exe`。配置中的本机文件服务路径也必须适合所在操作系统。
更新应用路由或“发布”不会改变正在运行的 TLS 认证策略；mTLS 模式和客户端 CA 变更需要重启。
已有服务端证书可继续使用，不必因启用 mTLS 而更换。此时客户端应信任它原来的服务端 CA，
网关的 `client_ca_file` 则填实际签发客户端证书的 CA，两者可以不同。

## 7. 验证成功与拒绝

先用支持 PEM 客户端证书的 curl 执行以下两项。Windows 自带 curl 的 TLS 后端可能使用系统证书存储，
若不支持这些 PEM 参数，可用浏览器或支持 PEM 的 curl 版本验证，不要直接判定是 Janus 错误。

```sh
# 携带客户端证书：应完成 TLS 握手，获得应用响应
curl --cacert .local/pki/ca.crt --cert .local/pki/clients/jim-mac/client.crt --key .local/pki/clients/jim-mac/client.key https://39.104.66.49:44091/vault/

# 不携带客户端证书：应在 TLS 层失败，不能获得应用页面
curl --cacert .local/pki/ca.crt https://39.104.66.49:44091/vault/
```

上述命令假设服务端也由这个 CA 签发；保留旧服务端证书时，`--cacert` 应换成其原 CA。
应用仍可能要求登录，这与 TLS 是否成功是两回事。不要使用 `-k` 掩盖 CA 信任或地址匹配错误。

## 8. 新设备、续签与丢失凭据

新增设备，继续使用同一个 CA，但选择新的输出目录：

```sh
./bin/janus cert client --ca .local/pki/ca.crt --ca-key .local/pki/ca.key --name jim-phone --out .local/pki/clients/jim-phone
```

相同 name 可以重复签发，**name 不是唯一设备数据库键**；区分证书应使用序列号/指纹和独立私钥。
文件已存在时命令拒绝覆盖。续签使用新目录，验证后再安排替换和重启，不要删除仍在使用的 CA。
`--days` 可在三个命令使用，范围 1～36500；叶证书有效期始终受 CA 到期时间限制。

当前没有单设备吊销列表、CRL/OCSP 自动检查或自动续签。删除本机的客户端证书文件，
不会撤销已经分发出去的副本。设备私钥泄露时，需要单独安排吊销机制，或切换信任 CA 并重新签发。
CA 私钥泄露应更换 CA、更新网关/设备信任并处理旧连接。

同一条已认证连接上的 keep-alive / HTTP/2 / HTTP/3 请求不会逐次重新握手。
修改 CA 内容并不能让旧连接立即失效；当前客户端 CA 是启动时快照，需要重启并关闭旧连接。

## 9. 常见问题

| 现象 | 排查方向 |
| --- | --- |
| `unknown certificate command` 或旧程序无法识别 `cert` | 使用新构建，确认没有误运行根目录的旧二进制 |
| `--hosts is required` | 服务端证书必须明确指定访问域名/IP，工具不从网关推断 |
| `--ca and --ca-key are required` | 先执行 `cert ca`，再明确提供两个输入文件 |
| `refusing to overwrite` | 已有证书不会覆盖；换一个输出目录，不要为了重试随意删 CA |
| CA/key 不匹配或 CA 已过期 | 检查文件是否来自同一对、用途与有效期；不会自动生成替代 CA |
| 浏览器提示服务器不可信 | 检查服务端签发 CA 的信任，与客户端身份安装分开处理 |
| 证书地址不匹配 | 检查 SAN 是否包含实际公网 IP/域名，端口和路径不参与 hosts 匹配 |
| 启用 mTLS 后打不开页面 | 检查客户端证书及私钥是否安装，是否由 Client CA file 中的 CA 签发 |
| `configs/.local/...` 不存在 | 配置相对路径少了 `../`；或用绝对路径消除歧义 |
| 只保存/发布后无变化 | 入口认证策略需写入文件、检查并重启；不是普通路由热更新 |

原来的 `-init-tls -config ...` 作为旧版便捷入口保留，仅用于配置驱动的服务端证书初始化。
新流程使用独立 `cert` 子命令；之前未发布的 `-init-mtls` 草案不再提供。
更多运行时边界与实现说明见 [mTLS 说明](mtls.md)。
