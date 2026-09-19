import type { JanusConfig, Middleware, Route, Service } from "./types";

export function cloneConfig<T>(value: T): T {
  return JSON.parse(JSON.stringify(value ?? null)) as T;
}

export function limenNames(config: JanusConfig | null): string[] {
  return Object.keys(config?.limens || {});
}

export function serviceNames(config: JanusConfig | null): string[] {
  return Object.keys(config?.services || {});
}

export function middlewareNames(config: JanusConfig | null): string[] {
  return Object.keys(config?.middlewares || {});
}

export function registryNames(config: JanusConfig | null): string[] {
  return Object.keys(config?.discovery?.nacos || {});
}

export function uniqueName(prefix: string, taken: string[]): string {
  let index = 1;
  let name = `${prefix}-${index}`;
  while (taken.includes(name)) {
    index += 1;
    name = `${prefix}-${index}`;
  }
  return name;
}

export function routeService(route: Route): string {
  return route.action?.forward?.service || route.service || "";
}

export function routeActionLabel(route: Route): string {
  if (route.action?.redirect) return "Redirect";
  if (route.action?.respond) return "Direct response";
  if (route.action?.static) return "Static files";
  const service = routeService(route);
  return service ? `→ ${service}` : "→ 未选择 Service";
}

export function routeMatchLabel(route: Route): string {
  if (route.match) return route.match;
  const host = route.host || "*";
  return `${host} · ${route.path_prefix || "/"}`;
}

export function middlewareKind(def: Middleware | undefined): string {
  if (!def) return "未定义";
  return Object.keys(def).find((key) => key !== "scope") || "未配置";
}

export function serviceSubtitle(service: Service | undefined): string {
  if (!service) return "未配置";
  if (service.nacos) return `Nacos · ${service.nacos.service_name || "未填服务名"}`;
  return `${service.upstreams?.length || 0} upstream`;
}

/** 前端本地快速检查已移至 validate.ts（附带发布前 diff）；此处保留模型 helpers。 */
export function splitLines(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}
