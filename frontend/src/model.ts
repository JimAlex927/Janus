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
  if (def.buffer) return "buffer";
  if (def.body_limit) return "body_limit";
  if (def.in_flight) return "in_flight";
  return "未配置";
}

export function serviceSubtitle(service: Service | undefined): string {
  if (!service) return "未配置";
  if (service.nacos) return `Nacos · ${service.nacos.service_name || "未填服务名"}`;
  return `${service.upstreams?.length || 0} upstream`;
}

/** 前端本地快速检查：只拦明显会 422 的问题，细则仍以后端校验为准。 */
export function validateLocal(config: JanusConfig): string[] {
  const errors: string[] = [];
  const routes = config.routes || [];
  const services = config.services || {};
  const middlewares = config.middlewares || {};
  const limens = config.limens || {};

  if (routes.length === 0) errors.push("至少需要 1 条 Route");
  const seen = new Set<string>();
  for (const route of routes) {
    if (!route.name) {
      errors.push("存在未命名的 Route");
      continue;
    }
    if (seen.has(route.name)) errors.push(`Route 名称重复：${route.name}`);
    seen.add(route.name);
    if (route.limen && !limens[route.limen]) errors.push(`Route ${route.name} 引用的 Limen 不存在：${route.limen}`);
    if (Object.keys(limens).length > 1 && !route.limen)
      errors.push(`Route ${route.name} 必须选择 Limen（当前有多个入口）`);
    if (route.match && (route.host || route.path_prefix))
      errors.push(`Route ${route.name} 不能同时填写 match 与 host/path_prefix`);
    const service = routeService(route);
    if (route.action?.redirect) {
      if (!route.action.redirect.location) errors.push(`Route ${route.name} 的 redirect 缺少 location`);
    } else if (route.action?.respond) {
      // direct response 无需 service
    } else if (service && !services[service]) {
      errors.push(`Route ${route.name} 引用的 Service 不存在：${service}`);
    }
    for (const name of route.middlewares || []) {
      const def = middlewares[name];
      if (!def) {
        errors.push(`Route ${route.name} 引用的 Middleware 不存在：${name}`);
        continue;
      }
      if (def.scope === "service") errors.push(`Route ${route.name} 不能引用 service 专用 Middleware：${name}`);
      if (def.in_flight) errors.push(`Route ${route.name} 不能引用 in_flight Middleware：${name}`);
    }
  }
  for (const [name, service] of Object.entries(services)) {
    if (service.nacos && service.upstreams?.length)
      errors.push(`Service ${name} 不能同时配置 upstreams 与 nacos`);
    if (!service.nacos && (!service.upstreams || service.upstreams.length === 0))
      errors.push(`Service ${name} 需要配置 upstreams 或 nacos`);
    if (service.nacos && !service.nacos.service_name) errors.push(`Service ${name} 的 nacos 缺少 service_name`);
    if (service.nacos && service.health_check)
      errors.push(`Service ${name} 使用 Nacos 时暂不支持 health_check`);
    for (const mw of service.middlewares || []) {
      if (!middlewares[mw]) errors.push(`Service ${name} 引用的 Middleware 不存在：${mw}`);
    }
  }
  return errors.slice(0, 12);
}

export function splitLines(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}
