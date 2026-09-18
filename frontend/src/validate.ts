import type { JanusConfig, Middleware, MiddlewareCapability } from "./types";
import { routeService } from "./model";

/** 前端本地快速检查：只拦截明显会 422 的问题，细则以后端校验为准。 */
export function validateLocal(config: JanusConfig, catalog: MiddlewareCapability[] = []): string[] {
  const errors: string[] = [];
  const routes = config.routes || [];
  const services = config.services || {};
  const middlewares = config.middlewares || {};
  const limens = config.limens || {};
  const capabilityOf = (def: Middleware) => {
    const type = Object.keys(def).find((key) => key !== "scope");
    return catalog.find((item) => item.type === type);
  };
  const validatePolicy = (name: string, def: Middleware) => {
    const type = Object.keys(def).find((key) => key !== "scope");
    const capability = capabilityOf(def);
    if (!type || !capability) return;
    const policy = def[type] && typeof def[type] === "object" ? def[type] as Record<string, unknown> : {};
    for (const field of capability.fields) {
      const value = policy[field.name];
      const missing = value === undefined || value === null || value === "";
      if (field.required && missing) {
        errors.push(`Middleware ${name} 的 ${field.label} 不能为空`);
        continue;
      }
      if (missing) continue;
      if (field.kind === "integer") {
        if (typeof value !== "number" || !Number.isInteger(value)) {
          errors.push(`Middleware ${name} 的 ${field.label} 必须是整数`);
        } else if ((field.min !== undefined && value < field.min) || (field.max !== undefined && value > field.max)) {
          errors.push(`Middleware ${name} 的 ${field.label} 必须在 ${field.min ?? "-∞"} 到 ${field.max ?? "∞"} 之间`);
        }
      } else if (field.kind === "string" && typeof value !== "string") {
        errors.push(`Middleware ${name} 的 ${field.label} 必须是文本`);
      } else if (field.kind === "boolean" && typeof value !== "boolean") {
        errors.push(`Middleware ${name} 的 ${field.label} 必须是 true 或 false`);
      } else if (field.kind === "string_list" && (!Array.isArray(value) || value.some((item) => typeof item !== "string"))) {
        errors.push(`Middleware ${name} 的 ${field.label} 必须是文本列表`);
      } else if (field.kind === "string_map" && (typeof value !== "object" || Array.isArray(value) || Object.values(value as Record<string, unknown>).some((item) => typeof item !== "string"))) {
        errors.push(`Middleware ${name} 的 ${field.label} 必须是文本键值对`);
      }
    }
  };

  if (routes.length === 0) errors.push("至少需要 1 条 Route");
  const seen = new Set<string>();
  for (const route of routes) {
    if (!route.name) { errors.push("存在未命名的 Route"); continue; }
    if (seen.has(route.name)) errors.push(`Route 名称重复：${route.name}`);
    seen.add(route.name);
    if (route.limen && !limens[route.limen]) errors.push(`Route ${route.name} 引用的 Limen 不存在：${route.limen}`);
    if (Object.keys(limens).length > 1 && !route.limen) errors.push(`Route ${route.name} 必须选择 Limen（当前有多个入口）`);
    if (route.match && (route.host || route.path_prefix)) errors.push(`Route ${route.name} 不能同时填写 match 与 host/path_prefix`);
    const service = routeService(route);
    if (route.action?.redirect) {
      if (!route.action.redirect.location) errors.push(`Route ${route.name} 的 redirect 缺少 location`);
    } else if (route.action?.respond) {
      // direct response 无需 service
    } else if (route.action?.forward && !service) {
      errors.push(`Route ${route.name} 的转发目标为空，请连线到 Service 或删除该边`);
    } else if (service && !services[service]) {
      errors.push(`Route ${route.name} 引用的 Service 不存在：${service}`);
    }
    for (const name of route.middlewares || []) {
      const def = middlewares[name];
      if (!def) { errors.push(`Route ${route.name} 引用的 Middleware 不存在：${name}`); continue; }
      if (def.scope === "service") errors.push(`Route ${route.name} 不能引用 service 专用 Middleware：${name}`);
      const capability = capabilityOf(def);
      if (catalog.length > 0 && !capability) errors.push(`Middleware ${name} 的类型不受当前后端支持`);
      else if (capability && !capability.scopes.includes("route")) errors.push(`Route ${route.name} 不能引用 ${capability.type} Middleware：${name}`);
    }
  }
  for (const [name, service] of Object.entries(services)) {
    const svc = service as import("./types").Service;
    if (svc.nacos && svc.upstreams?.length) errors.push(`Service ${name} 不能同时配置 upstreams 与 nacos`);
    if (!svc.nacos && (!svc.upstreams || svc.upstreams.length === 0)) errors.push(`Service ${name} 需要配置 upstreams 或 nacos`);
    if (svc.nacos && !svc.nacos.service_name) errors.push(`Service ${name} 的 nacos 缺少 service_name`);
    if (svc.nacos && svc.health_check) errors.push(`Service ${name} 使用 Nacos 时暂不支持 health_check`);
    for (const mw of svc.middlewares || []) {
      const def = middlewares[mw];
      if (!def) { errors.push(`Service ${name} 引用的 Middleware 不存在：${mw}`); continue; }
      if (def.scope === "route") errors.push(`Service ${name} 不能引用 route 专用 Middleware：${mw}`);
      const capability = capabilityOf(def);
      if (catalog.length > 0 && !capability) errors.push(`Middleware ${mw} 的类型不受当前后端支持`);
      else if (capability && !capability.scopes.includes("service")) errors.push(`Service ${name} 不能引用 ${capability.type} Middleware：${mw}`);
    }
  }
  for (const [name, definition] of Object.entries(middlewares)) {
    validatePolicy(name, definition);
  }
  return errors.slice(0, 12);
}

/** 生成配置差异摘要（用于发布前预览）。 */
export function diffSummary(oldConfig: JanusConfig | null, newConfig: JanusConfig): string[] {
  if (!oldConfig) return ["首次加载配置"];
  const changes: string[] = [];
  const oldRoutes = oldConfig.routes || [];
  const newRoutes = newConfig.routes || [];
  const oldRouteNames = new Set(oldRoutes.map((r) => r.name));
  const newRouteNames = new Set(newRoutes.map((r) => r.name));
  const addedRoutes = newRoutes.filter((r) => !oldRouteNames.has(r.name));
  const removedRoutes = oldRoutes.filter((r) => !newRouteNames.has(r.name));
  if (addedRoutes.length) changes.push(`新增 ${addedRoutes.length} 条路由：${addedRoutes.map((r) => r.name).join("、")}`);
  if (removedRoutes.length) changes.push(`删除 ${removedRoutes.length} 条路由：${removedRoutes.map((r) => r.name).join("、")}`);

  const oldServices = Object.keys(oldConfig.services || {});
  const newServices = Object.keys(newConfig.services || {});
  const addedServices = newServices.filter((s) => !oldServices.includes(s));
  const removedServices = oldServices.filter((s) => !newServices.includes(s));
  if (addedServices.length) changes.push(`新增 ${addedServices.length} 个服务：${addedServices.join("、")}`);
  if (removedServices.length) changes.push(`删除 ${removedServices.length} 个服务：${removedServices.join("、")}`);

  const oldMw = Object.keys(oldConfig.middlewares || {});
  const newMw = Object.keys(newConfig.middlewares || {});
  if (newMw.filter((m) => !oldMw.includes(m)).length) changes.push("中间件有新增");
  if (oldMw.filter((m) => !newMw.includes(m)).length) changes.push("中间件有删除");

  const oldLimens = Object.keys(oldConfig.limens || {});
  const newLimens = Object.keys(newConfig.limens || {});
  if (JSON.stringify(oldLimens.sort()) !== JSON.stringify(newLimens.sort())) changes.push("入口（Limen）配置有变更");
  if (JSON.stringify(oldConfig.settings) !== JSON.stringify(newConfig.settings)) changes.push("全局设置有变更");
  if (JSON.stringify(oldConfig.discovery) !== JSON.stringify(newConfig.discovery)) changes.push("注册中心配置有变更");

  return changes.length ? changes : ["配置无实质变更"];
}
