import type { BuiltinMiddlewareOverrides, JanusConfig, Route } from "./types";

export type BuiltinRequestKind = "http" | "sse" | "websocket";

export type BuiltinMiddlewareView = {
  name: string;
  detail: string;
  scope: "全局" | "Route";
  overridden?: boolean;
  editable?: boolean;
  appliesTo: BuiltinRequestKind[];
};

const allRequestKinds: BuiltinRequestKind[] = ["http", "sse", "websocket"];

export function setBuiltinMiddlewareOverride(
  current: BuiltinMiddlewareOverrides | undefined,
  group: keyof BuiltinMiddlewareOverrides,
  field: string,
  raw: string,
  numeric = false,
): BuiltinMiddlewareOverrides | undefined {
  const groupValue = { ...((current?.[group] || {}) as Record<string, unknown>) };
  if (raw.trim() === "") delete groupValue[field];
  else groupValue[field] = numeric ? Number(raw) : raw;

  const next = { ...(current || {}), [group]: Object.keys(groupValue).length > 0 ? groupValue : undefined } as BuiltinMiddlewareOverrides;
  for (const key of Object.keys(next) as Array<keyof BuiltinMiddlewareOverrides>) {
    if (!next[key]) delete next[key];
  }
  return Object.keys(next).length > 0 ? next : undefined;
}

export function clearBuiltinMiddlewareOverride(
  current: BuiltinMiddlewareOverrides | undefined,
  group: keyof BuiltinMiddlewareOverrides,
): BuiltinMiddlewareOverrides | undefined {
  if (!current) return undefined;
  const next = { ...current };
  delete next[group];
  return Object.keys(next).length > 0 ? next : undefined;
}

function settingValue(settings: Record<string, unknown> | undefined, section: string, key: string, fallback = "默认"): string {
  const group = settings?.[section];
  if (!group || typeof group !== "object") return fallback;
  const value = (group as Record<string, unknown>)[key];
  return value === undefined || value === null || value === "" ? fallback : String(value);
}

export function globalBuiltinMiddlewareViews(config: JanusConfig): BuiltinMiddlewareView[] {
  return [
    { name: "Observe", detail: "请求观测与指标", scope: "全局", appliesTo: allRequestKinds },
    { name: "RejectUnsupportedProtocols", detail: "拒绝不支持的协议", scope: "全局", appliesTo: allRequestKinds },
    { name: "Admission", detail: `总并发 max_in_flight=${settingValue(config.settings, "request", "max_in_flight")}`, scope: "全局", appliesTo: allRequestKinds },
  ];
}

export function routeBuiltinMiddlewareViews(config: JanusConfig, route: Route): BuiltinMiddlewareView[] {
  const overrides = route.builtin_middleware_overrides;
  const maximumDuration = overrides?.timeout?.maximum_duration || settingValue(config.settings, "request", "maximum_duration");
  const maxInFlight = overrides?.admission?.max_in_flight ?? Number(settingValue(config.settings, "request", "max_in_flight", "1024"));
  const admissionOverridden = overrides?.admission?.max_in_flight !== undefined;
  const streamMax = overrides?.stream_timeout?.max_duration || settingValue(config.settings, "stream", "max_duration");
  const streamIdle = overrides?.stream_timeout?.idle_timeout || settingValue(config.settings, "stream", "idle_timeout");
  const writeTimeout = overrides?.write_timeout?.timeout || settingValue(config.settings, "server", "write_timeout");
  const result: BuiltinMiddlewareView[] = [
    { name: "RouteMetadata", detail: `标记 route=${route.name}`, scope: "Route", appliesTo: allRequestKinds },
  ];
  result.push(
    {
      name: "Admission",
      detail: `Route 并发 max_in_flight=${maxInFlight}${admissionOverridden ? "" : "（继承全局）"}`,
      scope: "Route",
      overridden: admissionOverridden,
      editable: true,
      appliesTo: allRequestKinds,
    },
    { name: "WriteTimeout", detail: `timeout=${writeTimeout}`, scope: "Route", overridden: Boolean(overrides?.write_timeout?.timeout), editable: true, appliesTo: ["http"] },
    { name: "Timeout", detail: `HTTP maximum_duration=${maximumDuration}`, scope: "Route", overridden: Boolean(overrides?.timeout?.maximum_duration), editable: true, appliesTo: ["http"] },
    {
      name: "StreamTimeout",
      detail: `SSE/WS max_duration=${streamMax} · idle_timeout=${streamIdle}`,
      scope: "Route",
      overridden: Boolean(overrides?.stream_timeout?.max_duration || overrides?.stream_timeout?.idle_timeout),
      editable: true,
      appliesTo: ["sse", "websocket"],
    },
    { name: "ClearStreamingWriteDeadline", detail: "SSE/WS 清除响应写入 deadline", scope: "Route", appliesTo: ["sse", "websocket"] },
  );
  return result;
}

export function builtinMiddlewareViews(config: JanusConfig, route: Route): BuiltinMiddlewareView[] {
  return [...globalBuiltinMiddlewareViews(config), ...routeBuiltinMiddlewareViews(config, route)];
}
