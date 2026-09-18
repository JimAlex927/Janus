// Janus 配置的 TypeScript 视图，与 internal/config 的 JSON 字段一一对应。
// 有意保持贴近后端：未知字段不丢失（靠 JSON 页兜底），表单只编辑已知字段。

export interface LimenTLS {
  cert_file: string;
  key_file: string;
  min_version?: string;
}

export interface LimenHTTP3 {
  max_concurrent_streams?: number;
}

export interface Limen {
  address: string;
  protocols: string[];
  trusted_proxies?: string[];
  tls?: LimenTLS;
  http3?: LimenHTTP3;
}

export interface ForwardAction {
  service: string;
}

export interface RedirectAction {
  status?: number;
  location: string;
}

export interface RespondAction {
  status?: number;
  body?: string;
  headers?: Record<string, string>;
}

export interface RouteAction {
  forward?: ForwardAction;
  redirect?: RedirectAction;
  respond?: RespondAction;
}

export interface Route {
  name: string;
  limen?: string;
  match?: string;
  priority?: number;
  host?: string;
  path_prefix?: string;
  /** 兼容老配置的扁平 service 字段，新配置优先用 action.forward */
  service?: string;
  middlewares?: string[];
  action?: RouteAction;
}

export interface HealthCheck {
  path: string;
  interval?: string;
  timeout?: string;
  jitter?: string;
  unhealthy_threshold?: number;
  healthy_threshold?: number;
  expected_status?: number;
}

export interface NacosRef {
  registry: string;
  service_name: string;
  group_name?: string;
  clusters?: string[];
  scheme?: string;
}

export interface Service {
  upstreams?: string[];
  nacos?: NacosRef;
  middlewares?: string[];
  health_check?: HealthCheck;
}

export interface Middleware {
  scope?: string;
  buffer?: { max_response_body_bytes?: number };
  body_limit?: { max_bytes?: number };
  in_flight?: { max_concurrent?: number };
  headers?: {
    request_set?: Record<string, string>;
    request_remove?: string[];
    response_set?: Record<string, string>;
    response_remove?: string[];
  };
  strip_prefix?: { prefix?: string };
  [policy: string]: unknown;
}

export type MiddlewareFieldKind = "integer" | "string" | "string_list" | "string_map" | "boolean";

export interface MiddlewareFieldCapability {
  name: string;
  label: string;
  description?: string;
  kind: MiddlewareFieldKind;
  required?: boolean;
  default?: unknown;
  min?: number;
  max?: number;
}

export interface MiddlewareCapability {
  type: string;
  label: string;
  description: string;
  scopes: Array<"route" | "service">;
  fields: MiddlewareFieldCapability[];
}

export interface NacosServer {
  address: string;
  port: number;
  grpc_port?: number;
}

export interface NacosRegistry {
  servers: NacosServer[];
  namespace_id?: string;
  username?: string;
  /** 读取时后端会脱敏为空；发布时留空表示沿用旧凭据 */
  password?: string;
  password_env?: string;
  timeout?: string;
  stale_after?: string;
}

export interface JanusConfig {
  version?: number;
  limens?: Record<string, Limen>;
  settings?: Record<string, unknown>;
  middlewares?: Record<string, Middleware>;
  services?: Record<string, Service>;
  routes?: Route[];
  discovery?: {
    nacos?: Record<string, NacosRegistry>;
  };
  [extra: string]: unknown;
}

export interface ConfigSnapshot {
  config: JanusConfig;
  revision: number;
}

export interface MetricsSummary {
  requests: number;
  errors: number;
  in_flight: number;
}

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}
