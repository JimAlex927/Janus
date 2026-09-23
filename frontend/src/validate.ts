import type { JanusConfig, LimenTLS, Middleware, MiddlewareCapability } from "./types";
import { routeService } from "./model";

export function validateLimenTLS(tls?: LimenTLS): string[] {
  if (!tls) return [];
  const errors: string[] = [];
  if (!tls.cert_file?.trim() || !tls.key_file?.trim()) errors.push("TLS 需要填写 Certificate file 和 Key file");
  const mode = tls.client_auth || "none";
  if (mode !== "none" && mode !== "require_and_verify") errors.push("client_auth 仅支持 none 或 require_and_verify");
  if (mode === "require_and_verify" && !tls.client_ca_file?.trim()) errors.push("开启 mTLS 必须填写 Client CA file");
  if (mode === "none" && tls.client_ca_file) errors.push("Client CA file 需要同时开启 mTLS");
  return errors;
}

/** 前端本地快速检查：只拦截明显会 422 的问题，细则以后端校验为准。 */
export function validateLocal(config: JanusConfig, catalog: MiddlewareCapability[] = []): string[] {
  const errors: string[] = [];
  const routes = config.routes || [];
  const services = config.services || {};
  const middlewares = config.middlewares || {};
  const limens = config.limens || {};
  for (const [name, limen] of Object.entries(limens)) {
    errors.push(...validateLimenTLS(limen.tls).map((error) => `Limen ${name}: ${error}`));
  }
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
      const missing = value === undefined
        || value === null
        || value === ""
        || (field.kind === "string_list" && Array.isArray(value) && value.length === 0)
        || ((field.kind === "string_map" || field.kind === "json_object") && value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0);
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
      } else if (field.kind === "json_object" && (typeof value !== "object" || value === null || Array.isArray(value))) {
        errors.push(`Middleware ${name} 的 ${field.label} 必须是 JSON 对象`);
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

  if (JSON.stringify(oldConfig.limens || {}) !== JSON.stringify(newConfig.limens || {})) changes.push("入口（Limen）配置有变更，需写入文件并重启生效");
  if (JSON.stringify(oldConfig.settings) !== JSON.stringify(newConfig.settings)) changes.push("全局设置有变更");
  if (JSON.stringify(oldConfig.discovery) !== JSON.stringify(newConfig.discovery)) changes.push("注册中心配置有变更");

  return changes.length ? changes : ["配置无实质变更"];
}
