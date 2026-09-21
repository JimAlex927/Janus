# 本机 local-vault 的 TCP 穿透与 HTTPS

本页记录 2026-09-21 本机部署，不是 Vaultwarden 配置。应用源码在
`/Users/jim/Desktop/local-vault`，Docker 容器名为 `devhub`。

## 入口和信任边界

- 浏览器地址：`https://39.104.66.49:44091/vault/`。
- 用户配置的 TCP 隧道：公网 `44091` → Mac `127.0.0.1:8443`。
- Janus `vault_https` Limen 在本机完成 TLS（HTTP/1.1 或 HTTP/2，不启用 UDP/H3）。
- `/vault` 返回 308 到 `/vault/`；路径段感知的 `/vault` 路由剥离前缀后转发 `127.0.0.1:8788`。
- 8788 是 Vault-only；8787 是本地维护入口，不能作为公开上游。
- HTTPS 入口不匹配 `/janus/` 或 `/files/`。8081 上原有管理台/文件路由保留，但不再转发 `/vault`。
- local-vault 的 `DEVHUB_REQUIRE_HTTPS=1` 保留，Docker 两个端口仍只发布在 loopback。

Janus 的服务级 `pass_host_header: true` 仅对 Vault 服务开启，保持原 Host 和端口，
让应用的 Origin 同源校验正确工作。缺省 false 保留原来“后端 Host”的语义。
这个选项只影响 HTTP Host，不改变拨号地址、上游 TLS SNI 或证书校验。
它不读取客户端伪造的 `X-Forwarded-Host`；规范化转发头继续由 Limen 策略生成。
真实 TLS 请求使 `X-Forwarded-Proto` 为 https，不要在未加密公网 HTTP 上伪造该值。

前端构建使用 `DEVHUB_BASE_PATH=/vault/`，后端仍接收 `/api/...`、`/assets/...`。
该变量和 HTTPS 开关已明确写入 local-vault 的本地 `.env`（不进入 Git）。
本次运行中的容器已经使用 `/vault/assets/...`，所以没有重建容器或修改 Vault 数据库。

## 私有 CA 与访问设备

没有域名/现成证书时，本次采用私有 CA 签名，**不意味着所有浏览器自动信任**。
访问设备需要先通过可信渠道取得并安装 `rootCA.crt`，再启用 SSL/TLS 信任。
只信任你核对过指纹的这份证书，不使用“关闭证书检查”作为长期访问方式。

本机文件（目录 0700，文件 0600，整个 `.local` 已被 Git 忽略）：

| 文件 | 用途 |
| --- | --- |
| `.local/vault-tls/rootCA.crt` | 可分发给自己的访问设备的 CA 公共证书 |
| `.local/vault-tls/rootCA.key` | CA 私钥，绝不能发送、上传或提交 |
| `.local/vault-tls/server.crt` | Janus 使用的服务端证书 |
| `.local/vault-tls/server.key` | 服务端私钥，不能分发给客户端 |

CA SHA-256 指纹：

```text
F2:E0:11:6D:D2:05:91:61:18:27:D5:AB:C6:33:3B:0E:07:43:E9:7B:1E:52:E2:BD:B1:11:68:4E:12:F5:37:3C
```

服务端证书包含 `39.104.66.49`、`127.0.0.1` 和 `localhost`；有效期至
**2027-09-21 15:01:44 UTC**，到期前需要续签。证书匹配主机/IP，不绑定端口或路径。
CA 有效期十年；保护好 CA 私钥，泄露后应从所有设备移除信任并更换 CA。
本次没有自动修改 macOS/iOS 的系统证书信任。

- Mac：导入 CA 到“钥匙串访问”，找到 `Janus local-vault private CA`，双击并展开“信任”，配置 SSL 信任。[Apple 操作说明](https://support.apple.com/guide/keychain-access/change-the-trust-settings-of-a-certificate-kyca11871/mac)
- iPhone/iPad：安装证书描述文件后，还需要在“设置 → 通用 → 关于本机 → 证书信任设置”中启用该根证书的完全信任。[Apple 操作说明](https://support.apple.com/en-us/102390)

`sh scripts/create-vault-local-cert.sh` 是首次生成脚本（OpenSSL 3），已有证书目录时
会拒绝覆盖，以免意外更换 CA、破坏已安装设备的信任。它不是自动续期任务。

## 构建、启动、验证

默认配置仍为 `configs/janus-admin.example.json`。TLS 相对路径按配置文件目录解析。
管理台使用 `/janus` 前缀。`JANUS_UI_BASE_URL` 是构建参数，运行时设置不会改变
已编译二进制的管理台挂载路径。重新构建时必须保留此前缀，不能直接用普通
`go build` 替换部署二进制。先在原终端停止运行中的 Janus，再构建并启动：

```sh
cd /Users/jim/Desktop/Janus
JANUS_UI_BASE_URL=/janus JANUS_OUTPUT=./janus ./scripts/build-app.sh
./janus
```

若二进制已经按上述参数构建，正常启动只需 `./janus`。没有创建开机启动项。

原文件服务读取 `/Users/jim/Downloads`。新版按 generation 打开目录句柄，
macOS 必须允许启动它的终端访问 Downloads；本次从工具进程启动被拒绝，
由用户在原终端启动后恢复。不能通过删除文件路由或扩大目录权限掩盖此问题。

匿名 HTTPS 验证（不登录，不解锁，不读取条目）：

```sh
node scripts/verify-vault-proxy.mjs \
  https://39.104.66.49:44091/vault/ .local/vault-tls/rootCA.crt
```

此脚本验证证书、首页、JS/CSS、Vault-only 状态、斜杠跳转、同源 POST 到不存在的探针端点、
跨源拒绝和其他路由隔离。它使用显式 CA，未启用 insecure/跳过证书校验。
实际用户登录与解锁需要用户安装 CA 后完成；测试不会索取密码。

2026-09-21 验收结果：上述公网脚本 PASS；本机 8081 的旧 `/vault/` 返回 404，
9090 `/readyz` 返回 200。完整 Go 测试、config/proxy/gateway race、go vet、
前端构建和 embed parity 均通过；证书验证未使用 `-k`。

原二进制、原配置和旧内嵌前端文件保存在 `.local/backups/vault-https-20260921/`。
若回滚，先停服务并确认文件目标；旧配置把 Vault 指向维护端口 8787，**不要把旧配置恢复为公网部署**。
证书、私钥、本机二进制及其构建清单不进入 Git；源码、配置、脚本和文档纳入版本管理。
