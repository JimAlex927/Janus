package config

// MiddlewareCapability is the admin-facing description of one built-in
// middleware. The console consumes this catalog instead of duplicating the
// supported types, defaults and field limits in TypeScript.
type MiddlewareCapability struct {
	Type        string                      `json:"type"`
	Label       string                      `json:"label"`
	Description string                      `json:"description"`
	Scopes      []string                    `json:"scopes"`
	Fields      []MiddlewareFieldCapability `json:"fields"`
}

// MiddlewareFieldCapability describes a field inside the middleware policy
// object. Kind is intentionally a small UI-neutral vocabulary.
type MiddlewareFieldCapability struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	Min         *int64 `json:"min,omitempty"`
	Max         *int64 `json:"max,omitempty"`
}

func int64Pointer(value int64) *int64 { return &value }

// MiddlewareCapabilities returns a newly allocated, stable-order catalog.
// Callers may safely encode or mutate the returned slice.
func MiddlewareCapabilities() []MiddlewareCapability {
	return []MiddlewareCapability{
		{
			Type: "buffer", Label: "Buffer", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "缓存完整响应后再提交，适合需要在超时前保持状态码可变的小型 API 响应。",
			Fields: []MiddlewareFieldCapability{{
				Name: "max_response_body_bytes", Label: "最大响应体", Kind: "integer", Required: true,
				Description: "超过此大小时拒绝缓冲响应。", Default: int64(1 << 20), Min: int64Pointer(1), Max: int64Pointer(MaxBufferedResponseBytes),
			}},
		},
		{
			Type: "body_limit", Label: "Body Limit", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "限制请求体大小，已知或流式请求体超过上限时拒绝。",
			Fields: []MiddlewareFieldCapability{{
				Name: "max_bytes", Label: "最大请求体", Kind: "integer", Required: true,
				Default: int64(10 << 20), Min: int64Pointer(1), Max: int64Pointer(MaxRequestBodyBytes),
			}},
		},
		{
			Type: "in_flight", Label: "In Flight", Scopes: []string{MiddlewareScopeService},
			Description: "限制服务并发请求数；达到上限后立即返回 503，不排队。",
			Fields: []MiddlewareFieldCapability{{
				Name: "max_concurrent", Label: "最大并发", Kind: "integer", Required: true,
				Default: int64(100), Min: int64Pointer(1), Max: int64Pointer(MaxBackendConnections),
			}},
		},
		{
			Type: "headers", Label: "Headers", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "在转发前修改请求头，并在普通响应提交前修改响应头；101 升级握手不修改响应头。禁止修改逐跳和报文分帧头。",
			Fields: []MiddlewareFieldCapability{
				{Name: "request_set", Label: "设置请求头", Kind: "string_map", Description: "每行填写 Header: value；同名头会被覆盖。"},
				{Name: "request_remove", Label: "删除请求头", Kind: "string_list", Description: "每行一个 Header 名称。"},
				{Name: "response_set", Label: "设置响应头", Kind: "string_map", Description: "在响应提交前覆盖同名 Header。"},
				{Name: "response_remove", Label: "删除响应头", Kind: "string_list", Description: "每行一个 Header 名称。"},
			},
		},
		{
			Type: "cors", Label: "CORS", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "为浏览器跨域请求设置响应头，并在允许的 OPTIONS 预检时直接返回，不访问上游。Origin 仅支持精确 HTTP(S) Origin 或单独的 *。",
			Fields: []MiddlewareFieldCapability{
				{Name: "allow_origins", Label: "允许来源", Kind: "string_list", Required: true, Default: []string{"https://app.example.com"}, Description: "每行一个精确 Origin；使用 * 时不能启用凭据。"},
				{Name: "allow_methods", Label: "允许方法", Kind: "string_list", Required: true, Default: []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, Description: "每行一个大写 HTTP 方法；用于预检响应。"},
				{Name: "allow_headers", Label: "允许请求头", Kind: "string_list", Default: []string{"Content-Type", "Authorization", "X-Request-ID"}, Description: "预检中允许浏览器发送的非简单请求头，每行一个。"},
				{Name: "expose_headers", Label: "暴露响应头", Kind: "string_list", Description: "允许浏览器脚本读取的响应头，每行一个。"},
				{Name: "allow_credentials", Label: "允许凭据", Kind: "boolean", Default: false, Description: "允许 Cookie/认证信息；不能与 * 来源同时使用。"},
				{Name: "max_age_seconds", Label: "预检缓存秒数", Kind: "integer", Default: 600, Min: int64Pointer(0), Max: int64Pointer(MaxCORSMaxAgeSeconds), Description: "浏览器缓存成功预检的时长；0 表示不发送 Max-Age。"},
			},
		},
		{
			Type: "jwt", Label: "JWT Authentication", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "验证 Authorization Bearer JWT；校验签名、有效期、issuer、audience 和必需 claims，不向上游自动转发 claims。",
			Fields: []MiddlewareFieldCapability{
				{Name: "key_source", Label: "密钥来源", Kind: "json_object", Required: true, Description: "jwks_url、public_key_file、secret_env 三选一。"},
				{Name: "algorithms", Label: "允许算法", Kind: "string_list", Required: true, Description: "显式算法白名单，禁止根据 token header 推断。"},
				{Name: "issuer", Label: "Issuer", Kind: "string", Required: true},
				{Name: "audience", Label: "Audience", Kind: "string_list", Required: true},
				{Name: "required_claims", Label: "必需 Claims", Kind: "string_list", Description: "认证通过后只在请求上下文中提供，不自动转发。"},
				{Name: "clock_skew", Label: "时钟偏差", Kind: "string", Default: "30s"},
			},
		},
		{
			Type: "strip_prefix", Label: "Strip Prefix", Scopes: []string{MiddlewareScopeRoute},
			Description: "将匹配的路径前缀移除后再交给路由动作；转发到上游时会写入可信的 X-Forwarded-Prefix。",
			Fields: []MiddlewareFieldCapability{{
				Name: "prefix", Label: "路径前缀", Kind: "string", Required: true, Default: "/api",
				Description: "必须是以 / 开头的绝对路径前缀。",
			}},
		},
		{
			Type: "add_prefix", Label: "Add Prefix", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "在执行路由动作前为请求路径增加固定前缀；可用于为整个 Service 补充统一的上游基础路径。",
			Fields: []MiddlewareFieldCapability{{
				Name: "prefix", Label: "路径前缀", Kind: "string", Required: true, Default: "/internal",
				Description: "必须是以 / 开头且不以 / 结尾的非根路径前缀。",
			}},
		},
		{
			Type: "basic_auth", Label: "Basic Authentication", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "使用 bcrypt 密码哈希验证 HTTP Basic Auth；认证失败返回 401 challenge。",
			Fields: []MiddlewareFieldCapability{
				{Name: "realm", Label: "Realm", Kind: "string", Required: true, Default: "Janus"},
				{Name: "users", Label: "用户哈希", Kind: "string_map", Required: true, Description: "用户名到 bcrypt 哈希的映射，禁止明文密码。"},
				{Name: "remove_header", Label: "移除认证头", Kind: "boolean", Default: false, Description: "认证成功后不把 Authorization 传给上游。"},
			},
		},
		{
			Type: "ip_allowlist", Label: "IP Allow List", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "只允许来自配置 CIDR 的客户端 IP；客户端 IP 使用 Janus 可信代理策略解析。",
			Fields:      []MiddlewareFieldCapability{{Name: "source_ranges", Label: "来源 CIDR", Kind: "string_list", Required: true, Description: "每行一个 IPv4/IPv6 CIDR。"}},
		},
		{
			Type: "rate_limit", Label: "Rate Limit", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "按可信客户端 IP 进行有界 token-bucket 限流，拒绝时返回 429 和 Retry-After。",
			Fields: []MiddlewareFieldCapability{
				{Name: "average", Label: "平均速率", Kind: "integer", Required: true, Default: int64(DefaultRateLimitAverage), Min: int64Pointer(1), Max: int64Pointer(MaxRateLimitAverage)},
				{Name: "period", Label: "周期", Kind: "string", Required: true, Default: "1s", Description: "average 个令牌在此周期内补充。"},
				{Name: "burst", Label: "突发容量", Kind: "integer", Required: true, Default: int64(DefaultRateLimitBurst), Min: int64Pointer(1), Max: int64Pointer(MaxRateLimitBurst)},
				{Name: "max_keys", Label: "最大客户端数", Kind: "integer", Default: int64(DefaultRateLimitMaxKeys), Min: int64Pointer(1), Max: int64Pointer(MaxRateLimitKeys)},
			},
		},
		{
			Type: "compress", Label: "Compress", Scopes: []string{MiddlewareScopeRoute, MiddlewareScopeService},
			Description: "协商 gzip 压缩普通响应；自动跳过 WebSocket、SSE、空响应和已编码响应。",
			Fields:      []MiddlewareFieldCapability{},
		},
	}
}

// MiddlewareType returns the configured policy type. Validation guarantees
// that exactly one policy exists before a generation is built.
func MiddlewareType(m Middleware) string {
	switch {
	case m.Buffer != nil:
		return "buffer"
	case m.BodyLimit != nil:
		return "body_limit"
	case m.InFlight != nil:
		return "in_flight"
	case m.Headers != nil:
		return "headers"
	case m.CORS != nil:
		return "cors"
	case m.JWT != nil:
		return "jwt"
	case m.StripPrefix != nil:
		return "strip_prefix"
	case m.AddPrefix != nil:
		return "add_prefix"
	case m.BasicAuth != nil:
		return "basic_auth"
	case m.IPAllowList != nil:
		return "ip_allowlist"
	case m.RateLimit != nil:
		return "rate_limit"
	case m.Compress != nil:
		return "compress"
	default:
		return ""
	}
}
