export type JsonObject = Record<string, any>;
export type Config = JsonObject & { routes?: JsonObject[]; limens?: Record<string, JsonObject>; services?: Record<string, JsonObject>; middlewares?: Record<string, JsonObject> };
export type NodeKind = "limen" | "route" | "middleware" | "service" | "action";
export type Position = { x: number; y: number };
export type GraphNode = { id: string; kind: NodeKind; name: string; subtitle: string; badges: string[]; x: number; y: number };
export type GraphEdge = { from: string; to: string };

export const NODE_WIDTH = 218;
export const NODE_HEIGHT = 92;
export const CANVAS_WIDTH = 1800;
export const CANVAS_HEIGHT = 1040;

export const KIND_META: Record<NodeKind, { label: string; icon: string; description: string; color: string }> = {
  limen: { label: "Limen", icon: "◉", description: "协议入口与监听地址", color: "#0f766e" },
  route: { label: "Route", icon: "↗", description: "匹配规则与请求分发", color: "#536dfe" },
  middleware: { label: "Middleware", icon: "◆", description: "限流、缓存和请求策略", color: "#c2410c" },
  service: { label: "Service", icon: "▣", description: "上游池与健康检查", color: "#7c3aed" },
  action: { label: "Action", icon: "→", description: "重定向或直接响应", color: "#be123c" },
};

// The graph is a view of the draft, not a second configuration format. The
// canvas intentionally models only the inbound topology: Limen -> Route.
// Services and Middleware are reusable resources managed in their own pages
// and referenced from the Route editor.
export function buildGraph(draft: Config): { nodes: GraphNode[]; edges: GraphEdge[] } {
  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  const limens = Object.entries(draft.limens || {});
  const routes = draft.routes || [];
  limens.forEach(([name, value], index) => { const position = stackPosition(index, limens.length, 28, 40, 150); nodes.push({ id: `limen:${name}`, kind: "limen", name, subtitle: value.address || "未配置地址", badges: value.protocols || [], ...position }); });
  routes.forEach((route, index) => { const position = stackPosition(index, routes.length, 300, 34, 112); nodes.push({ id: `route:${route.name}`, kind: "route", name: route.name || `route-${index + 1}`, subtitle: route.match || "未配置匹配规则", badges: route.middlewares || [], ...position }); });
  routes.forEach(route => {
    const routeID = `route:${route.name}`;
    if (route.limen && (draft.limens || {})[route.limen]) edges.push({ from: `limen:${route.limen}`, to: routeID });
  });
  return { nodes, edges };
}

// Keep the default graph readable without allowing a large configuration to
// place nodes outside the drawable world. Columns are intentionally compact;
// fitView can still zoom the complete graph for larger configurations.
function stackPosition(index: number, count: number, baseX: number, top: number, rowGap: number): Position {
  const rows = Math.max(1, Math.floor((CANVAS_HEIGHT - top - NODE_HEIGHT - 12) / rowGap) + 1);
  const column = Math.floor(index / rows);
  const row = index % rows;
  const columns = Math.max(1, Math.ceil(count / rows));
  const available = Math.max(0, CANVAS_WIDTH - NODE_WIDTH - 12 - baseX);
  const columnGap = columns <= 1 ? 0 : Math.min(250, available / (columns - 1));
  return { x: baseX + column * columnGap, y: top + row * rowGap };
}

export function addConfigNode(draft: Config, kind: NodeKind): { config: Config; id: string } {
  if (kind === "route") { const name = uniqueName("route", (draft.routes || []).map(item => item.name)); return { config: { ...draft, routes: [...(draft.routes || []), { name, match: "PathPrefix(`/new`)", action: { forward: { service: "" } }, middlewares: [] }] }, id: `route:${name}` }; }
  if (kind === "service") { const name = uniqueName("service", Object.keys(draft.services || {})); return { config: { ...draft, services: { ...(draft.services || {}), [name]: { upstreams: ["http://127.0.0.1:9000"], middlewares: [] } } }, id: `service:${name}` }; }
  if (kind === "middleware") { const name = uniqueName("middleware", Object.keys(draft.middlewares || {})); return { config: { ...draft, middlewares: { ...(draft.middlewares || {}), [name]: { scope: "route", buffer: { max_response_body_bytes: 1048576 } } } }, id: `middleware:${name}` }; }
  const name = uniqueName("limen", Object.keys(draft.limens || {})); return { config: { ...draft, limens: { ...(draft.limens || {}), [name]: { address: "127.0.0.1:8080", protocols: ["http1"] } } }, id: `limen:${name}` };
}

export function renameNode(draft: Config, node: GraphNode, requestedName: string): { config: Config; error?: string } {
  if (node.kind !== "middleware") return { config: draft, error: "只有 Middleware 支持在此修改名称" };
  const name = requestedName.trim();
  if (!name) return { config: draft, error: "Middleware 名称不能为空" };
  if (name === node.name) return { config: draft };
  if (draft.middlewares?.[name]) return { config: draft, error: `Middleware 名称 ${name} 已存在` };
  const definition = draft.middlewares?.[node.name];
  if (!definition) return { config: draft, error: `找不到 Middleware ${node.name}` };
  const middlewares = { ...(draft.middlewares || {}) };
  delete middlewares[node.name];
  middlewares[name] = definition;
  const replace = (items: string[] | undefined) => (items || []).map(item => item === node.name ? name : item);
  return {
    config: {
      ...draft,
      middlewares,
      routes: (draft.routes || []).map(route => ({ ...route, middlewares: replace(route.middlewares) })),
      services: Object.fromEntries(Object.entries(draft.services || {}).map(([serviceName, service]) => [serviceName, { ...service, middlewares: replace(service.middlewares) }])),
    },
  };
}

export function updateNode(draft: Config, node: GraphNode, patch: JsonObject): Config {
  if (node.kind === "route") return { ...draft, routes: (draft.routes || []).map(item => item.name === node.name ? { ...item, ...patch } : item) };
  if (node.kind === "middleware") { const definition = patch.definition || draft.middlewares?.[node.name] || {}; const rest = { ...patch }; delete rest.definition; return { ...draft, middlewares: { ...(draft.middlewares || {}), [node.name]: Object.keys(rest).length ? { ...definition, ...rest } : definition } }; }
  if (node.kind === "service") return { ...draft, services: { ...(draft.services || {}), [node.name]: { ...(draft.services?.[node.name] || {}), ...patch } } };
  return { ...draft, limens: { ...(draft.limens || {}), [node.name]: { ...(draft.limens?.[node.name] || {}), ...patch } } };
}

export function connectNodes(draft: Config, from?: GraphNode, to?: GraphNode): { config: Config; error?: string } {
  // Keep connection rules explicit. Unsupported or ambiguous relations return
  // the original draft so a failed gesture can never partially mutate config.
  const error = connectionError(draft, from, to);
  if (error) return { config: draft, error };
  if (!from || !to || from.id === to.id) return { config: draft, error: "请选择两个不同的节点" };
  if (from.kind === "limen" && to.kind === "route") return { config: updateNode(draft, to, { limen: from.name }) };
  if (from.kind === "route" && to.kind === "middleware") {
    const route = draft.routes?.find(item => item.name === from.name);
    if (!route) return { config: draft, error: "找不到来源 Route" };
    if ((route.middlewares || []).includes(to.name)) return { config: draft, error: "这个 Middleware 已经连接到该 Route" };
    return { config: updateNode(draft, from, { middlewares: [...(route.middlewares || []), to.name] }) };
  }
  if (from.kind === "middleware" && to.kind === "middleware") {
    const routeResult = uniqueRouteForMiddleware(draft, from.name);
    if (routeResult.error) return { config: draft, error: routeResult.error };
    const route = routeResult.route;
    if (!route) return { config: draft, error: "找不到来源 Route" };
    const chain = (route.middlewares || []).filter((name: string) => name !== to.name);
    const index = chain.indexOf(from.name);
    if (index < 0) return { config: draft, error: "来源 Middleware 不在 Route 链中" };
    chain.splice(index + 1, 0, to.name);
    return { config: updateNode(draft, { ...from, kind: "route", name: route.name, id: `route:${route.name}` }, { middlewares: chain }) };
  }
  if ((from.kind === "route" || from.kind === "middleware") && to.kind === "service") {
    const routeResult = from.kind === "route" ? { route: draft.routes?.find(item => item.name === from.name) } : uniqueRouteForMiddleware(draft, from.name);
    if (routeResult.error) return { config: draft, error: routeResult.error };
    const route = routeResult.route;
    if (!route) return { config: draft, error: "找不到需要转发的 Route" };
    return { config: updateNode(draft, { ...from, kind: "route", name: route.name, id: `route:${route.name}` }, { action: { forward: { service: to.name } } }) };
  }
  if (from.kind === "service" && to.kind === "middleware") {
    const service = draft.services?.[from.name];
    if (!service) return { config: draft, error: "找不到来源 Service" };
    if ((service.middlewares || []).includes(to.name)) return { config: draft, error: "这个 Middleware 已经连接到该 Service" };
    return { config: updateNode(draft, from, { middlewares: [...(service.middlewares || []), to.name] }) };
  }
  return { config: draft, error: `${KIND_META[from.kind].label} 不能连接到 ${KIND_META[to.kind].label}` };
}

// The canvas uses the same predicate before highlighting a target. Keeping
// this next to connectNodes prevents visual affordances from drifting away
// from the mutation rules below.
export function canConnectNodes(draft: Config, from?: GraphNode, to?: GraphNode): boolean {
  return !connectionError(draft, from, to);
}

function connectionError(draft: Config, from?: GraphNode, to?: GraphNode): string | undefined {
  if (!from || !to || from.id === to.id) return "请选择两个不同的节点";
  if (from.kind === "limen" && to.kind === "route") return undefined;
  if (from.kind === "route" && to.kind === "middleware") {
    const route = draft.routes?.find(item => item.name === from.name);
    if (!route) return "找不到来源 Route";
    if ((route.middlewares || []).includes(to.name)) return "这个 Middleware 已经连接到该 Route";
    return undefined;
  }
  if (from.kind === "middleware" && to.kind === "middleware") {
    const routeResult = uniqueRouteForMiddleware(draft, from.name);
    if (routeResult.error) return routeResult.error;
    const chain: string[] = routeResult.route?.middlewares || [];
    if (!chain.includes(from.name)) return "来源 Middleware 不在 Route 链中";
    return undefined;
  }
  if ((from.kind === "route" || from.kind === "middleware") && to.kind === "service") {
    if (from.kind === "route" && !draft.routes?.some(item => item.name === from.name)) return "找不到来源 Route";
    const routeResult = from.kind === "route" ? { route: draft.routes?.find(item => item.name === from.name) } : uniqueRouteForMiddleware(draft, from.name);
    if (routeResult.error) return routeResult.error;
    return routeResult.route ? undefined : "找不到需要转发的 Route";
  }
  if (from.kind === "service" && to.kind === "middleware") {
    const service = draft.services?.[from.name];
    if (!service) return "找不到来源 Service";
    if ((service.middlewares || []).includes(to.name)) return "这个 Middleware 已经连接到该 Service";
    return undefined;
  }
  return `${KIND_META[from.kind].label} 不能连接到 ${KIND_META[to.kind].label}`;
}

function uniqueRouteForMiddleware(draft: Config, middleware: string): { route?: JsonObject; error?: string } {
  const matches = (draft.routes || []).filter(route => (route.middlewares || []).includes(middleware));
  if (matches.length === 0) return { error: "只有被 Route 引用的 Middleware 才能继续连接" };
  if (matches.length > 1) return { error: "这个 Middleware 被多个 Route 共享，请从具体 Route 节点连接" };
  return { route: matches[0] };
}

export function removeNode(draft: Config, node: GraphNode): Config {
  if (node.kind === "route") return { ...draft, routes: (draft.routes || []).filter(item => item.name !== node.name) };
  if (node.kind === "middleware") return { ...draft, middlewares: without(draft.middlewares, node.name), routes: (draft.routes || []).map(route => ({ ...route, middlewares: (route.middlewares || []).filter((name: string) => name !== node.name) })), services: Object.fromEntries(Object.entries(draft.services || {}).map(([name, service]) => [name, { ...service, middlewares: (service.middlewares || []).filter((item: string) => item !== node.name) }])) };
  if (node.kind === "service") return { ...draft, services: without(draft.services, node.name), routes: (draft.routes || []).map(route => route.action?.forward?.service === node.name ? { ...route, action: { ...route.action, forward: { service: "" } } } : route) };
  return { ...draft, limens: without(draft.limens, node.name), routes: (draft.routes || []).map(route => route.limen === node.name ? { ...route, limen: "" } : route) };
}

export function actionForType(type: string, route: JsonObject, draft: Config): JsonObject { if (type === "redirect") return { redirect: { status: 308, location: "https://example.com" } }; if (type === "respond") return { respond: { status: 200, body: "ok\n", headers: { "Content-Type": "text/plain; charset=utf-8" } } }; return { forward: { service: route.action?.forward?.service || Object.keys(draft.services || {})[0] || "" } }; }
export function defaultMiddlewareSettings(type: string): JsonObject { if (type === "body_limit") return { max_bytes: 10485760 }; if (type === "in_flight") return { max_concurrent: 100 }; return { max_response_body_bytes: 1048576 }; }
function uniqueName(prefix: string, names: string[]): string { let index = 1; let name = `${prefix}-${index}`; while (names.includes(name)) name = `${prefix}-${++index}`; return name; }
export function clampPosition(position: Position): Position { return { x: Math.min(CANVAS_WIDTH - NODE_WIDTH - 12, Math.max(12, position.x)), y: Math.min(CANVAS_HEIGHT - NODE_HEIGHT - 12, Math.max(32, position.y)) }; }
export function splitComma(value: string): string[] { return value.split(",").map(item => item.trim()).filter(Boolean); }
export function splitLines(value: string): string[] { return value.split(/\r?\n/).map(item => item.trim()).filter(Boolean); }
function without<T>(source: Record<string, T> | undefined, key: string): Record<string, T> { const result = { ...(source || {}) }; delete result[key]; return result; }
