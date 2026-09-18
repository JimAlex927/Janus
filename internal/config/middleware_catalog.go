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
	case m.StripPrefix != nil:
		return "strip_prefix"
	case m.AddPrefix != nil:
		return "add_prefix"
	default:
		return ""
	}
}
