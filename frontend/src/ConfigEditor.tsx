import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import {
  Background,
  Controls,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  ApiError,
  getStoredConfig,
  publishStoredConfig,
  saveStoredConfig,
  stageLimens,
  validateConfig,
  type StoredConfig,
} from "./api";
import { LimenEditor, ServiceEditor, type MwType } from "./editors";
import { cloneConfig, limenNames, routeActionLabel, routeMatchLabel, serviceSubtitle, uniqueName } from "./model";
import { MiddlewareManagerModal, classDefaults, type MiddlewareScopeFilter } from "./MiddlewareManager";
import { RegistryManagerModal } from "./RegistryManager";
import { RouteEditor } from "./RouteEditor";
import { Badge, Drawer, Empty } from "./ui";
import type { ConfigStore } from "./useConfig";
import { validateLocal } from "./validate";
import type { JanusConfig, Limen, Middleware, NacosRegistry, Route, Service } from "./types";

type NodeKind = "limen" | "route" | "service";
type NodeData = {
  kind: NodeKind;
  name: string;
  subtitle: string;
  detail: string;
  match?: string;
  middlewares?: string[];
  action?: string;
  onOpen?: () => void;
  onOpenMiddleware?: () => void;
};
type FlowNode = Node<NodeData, "janus">;

const KIND_META: Record<NodeKind, { label: string; color: string; icon: string }> = {
  limen: { label: "Limen", color: "#0f766e", icon: "◉" },
  route: { label: "Route", color: "#536dfe", icon: "↗" },
  service: { label: "Service", color: "#7c3aed", icon: "▣" },
};

const COLUMN_X: Record<NodeKind, number> = { limen: 0, route: 340, service: 700 };
const ROW_GAP: Record<NodeKind, number> = { limen: 110, route: 240, service: 150 };

function nodeId(kind: NodeKind, name: string): string {
  return `${kind}:${name}`;
}

function parseNodeId(id: string): { kind: NodeKind; name: string } | null {
  const index = id.indexOf(":");
  if (index < 0) return null;
  const kind = id.slice(0, index) as NodeKind;
  if (!KIND_META[kind]) return null;
  return { kind, name: id.slice(index + 1) };
}

function describe(draft: JanusConfig, kind: NodeKind, name: string): { subtitle: string; detail: string; match?: string; middlewares?: string[]; action?: string } {
  if (kind === "limen") {
    const limen = draft.limens?.[name];
    return { subtitle: limen?.address || "未配置地址", detail: (limen?.protocols || []).join(" · ") };
  }
  if (kind === "route") {
    const route = draft.routes?.find((r) => r.name === name);
    if (!route) return { subtitle: "", detail: "" };
    return {
      subtitle: routeMatchLabel(route),
      detail: routeActionLabel(route),
      match: route.match || `${route.host || "*"} ${route.path_prefix || "/"}`,
      middlewares: route.middlewares || [],
      action: routeActionLabel(route),
    };
  }
  if (kind === "service") {
    const service = draft.services?.[name];
    return { subtitle: serviceSubtitle(service), detail: `${(service?.middlewares || []).length} middleware` };
  }
  return { subtitle: "", detail: "" };
}

function autoPosition(kind: NodeKind, index: number): { x: number; y: number } {
  return { x: COLUMN_X[kind], y: 20 + index * ROW_GAP[kind] };
}

/**
 * 一键整理：按 Limen → Route → Service 分层，同层内按引用关系重心排序，
 * 让边尽量不交叉。结果是确定性的（同名排序），反复点击布局不变。
 */
function computeTidyPositions(draft: JanusConfig): Record<string, { x: number; y: number }> {
  const positions: Record<string, { x: number; y: number }> = {};
  const limens = Object.keys(draft.limens || {}).sort();
  limens.forEach((name, index) => {
    positions[nodeId("limen", name)] = { x: COLUMN_X.limen, y: 20 + index * ROW_GAP.limen };
  });
  const limenRank = new Map(limens.map((name, index) => [name, index]));
  const routes = [...(draft.routes || [])].sort((a, b) => {
    const rank = (limenRank.get(a.limen || "") ?? limens.length) - (limenRank.get(b.limen || "") ?? limens.length);
    return rank !== 0 ? rank : a.name.localeCompare(b.name);
  });
  routes.forEach((route, index) => {
    positions[nodeId("route", route.name)] = { x: COLUMN_X.route, y: 20 + index * ROW_GAP.route };
  });
  const routeRank = new Map(routes.map((route, index) => [route.name, index]));
  const routeServiceOf = (routeName: string) => {
    const route = (draft.routes || []).find((r) => r.name === routeName);
    return route?.action?.forward?.service || route?.service || "";
  };
  const services = Object.keys(draft.services || {}).sort((a, b) => {
    const rankA = Math.min(...(draft.routes || []).filter((r) => routeServiceOf(r.name) === a).map((r) => routeRank.get(r.name) ?? routes.length), routes.length);
    const rankB = Math.min(...(draft.routes || []).filter((r) => routeServiceOf(r.name) === b).map((r) => routeRank.get(r.name) ?? routes.length), routes.length);
    return rankA !== rankB ? rankA - rankB : a.localeCompare(b);
  });
  services.forEach((name, index) => {
    positions[nodeId("service", name)] = { x: COLUMN_X.service, y: 20 + index * ROW_GAP.service };
  });
  return positions;
}

/** 边完全由 draft 派生：入口归属、转发目标、中间件链。 */
function deriveEdges(draft: JanusConfig): Edge[] {
  const edges: Edge[] = [];
  for (const route of draft.routes || []) {
    if (route.limen && draft.limens?.[route.limen]) {
      edges.push({
        id: `${nodeId("limen", route.limen)}-->route:${route.name}`,
        source: nodeId("limen", route.limen),
        target: `route:${route.name}`,
        type: "smoothstep",
      });
    }
    const service = route.action?.forward?.service || route.service || "";
    if (service && draft.services?.[service]) {
      edges.push({
        id: `route:${route.name}--svc->service:${service}`,
        source: `route:${route.name}`,
        sourceHandle: "svc",
        target: `service:${service}`,
        type: "smoothstep",
        animated: true,
      });
    }
  }
  return edges;
}

function JanusNode({ data, selected }: { data: NodeData; selected?: boolean }) {
  const meta = KIND_META[data.kind];
  if (data.kind === "route") {
    return (
      <div className={`flow-node flow-route ${selected ? "selected" : ""}`} style={{ borderTopColor: meta.color }}>
        <Handle type="target" position={Position.Left} />
        <div className="flow-node-head">
          <span className="flow-node-icon" style={{ background: meta.color }}>
            {meta.icon}
          </span>
          <div>
            <small>{meta.label}</small>
            <strong>{data.name}</strong>
          </div>
        </div>
        <button type="button" className="flow-section" title="编辑匹配规则" onClick={(e) => { e.stopPropagation(); data.onOpen?.(); }}>
          <small className="flow-section-title">◈ 匹配</small>
          <span className="flow-section-body mono">{data.match || "未配置"}</span>
        </button>
        <button type="button" className="flow-section" title="管理中间件" onClick={(e) => { e.stopPropagation(); data.onOpenMiddleware?.(); }}>
          <small className="flow-section-title">◈ 中间件（{(data.middlewares || []).length}）</small>
          {(data.middlewares || []).length === 0 ? (
            <span className="flow-section-empty">未挂载，点击添加＋</span>
          ) : (
            <span className="flow-section-chips">
              {(data.middlewares || []).map((m, i) => <em key={`${m}-${i}`}>{i + 1}.{m}</em>)}
            </span>
          )}
        </button>
        <button type="button" className="flow-section" title="编辑动作" onClick={(e) => { e.stopPropagation(); data.onOpen?.(); }}>
          <small className="flow-section-title">◈ 动作</small>
          <span className="flow-section-body">{data.action || "未配置"}</span>
        </button>
        <Handle type="source" position={Position.Right} id="svc" title="转发到 Service" />
      </div>
    );
  }
  return (
    <div className={`flow-node ${selected ? "selected" : ""}`} style={{ borderTopColor: meta.color }}>
      <Handle type="target" position={Position.Left} />
      <div className="flow-node-head">
        <span className="flow-node-icon" style={{ background: meta.color }}>
          {meta.icon}
        </span>
        <div>
          <small>{meta.label}</small>
          <strong>{data.name}</strong>
        </div>
      </div>
      <p className="flow-node-sub">{data.subtitle || "尚未配置"}</p>
      {data.detail && <small className="flow-node-detail">{data.detail}</small>}
      {data.kind === "limen" && <Handle type="source" position={Position.Right} id="out" />}
    </div>
  );
}

const nodeTypes = { janus: JanusNode };

type EditorState =
  | { kind: "route"; name: string; value: Route; isNew: boolean }
  | { kind: "service"; name: string; value: Service; isNew: boolean }
  | { kind: "limen"; name: string; value: Limen; isNew: boolean };

export function ConfigEditorPage({ store, id, onBack, onStatusChange }: { store: ConfigStore; id: number; onBack: () => void; onStatusChange: () => void }) {
  return (
    <ReactFlowProvider>
      <ConfigEditor store={store} id={id} onBack={onBack} onStatusChange={onStatusChange} />
    </ReactFlowProvider>
  );
}

function ConfigEditor({ store, id, onBack, onStatusChange }: { store: ConfigStore; id: number; onBack: () => void; onStatusChange: () => void }) {
  const [meta, setMeta] = useState<{ name: string; status: string } | null>(null);
  const [draft, setDraft] = useState<JanusConfig | null>(null);
  const [saved, setSaved] = useState("");
  const [savedLayout, setSavedLayout] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<EditorState | null>(null);
  const [mwManager, setMwManager] = useState<{ scope: MiddlewareScopeFilter } | null>(null);
  const [regManager, setRegManager] = useState<{ allowSelect: boolean } | null>(null);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [nodes, setNodes, onNodesChangeDefault] = useNodesState<FlowNode>([]);
  const [edges, setEdges, onEdgesChangeDefault] = useEdgesState<Edge>([]);
  const { screenToFlowPosition } = useReactFlow();
  const draftRef = useRef<JanusConfig | null>(null);
  draftRef.current = draft;

  const dirty = useMemo(() => {
    if (!draft) return false;
    if (JSON.stringify(draft) !== saved) return true;
    return JSON.stringify(currentLayout()) !== savedLayout;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft, saved, savedLayout, nodes]);

  useEffect(() => {
    let alive = true;
    (async () => {
      setLoading(true);
      try {
        const record: StoredConfig = await getStoredConfig(id);
        if (!alive) return;
        setMeta({ name: record.name, status: record.status });
        setDraft(record.content);
        setSaved(JSON.stringify(record.content));
        setSavedLayout(JSON.stringify(record.layout?.nodes || {}));
        setNodes(buildNodes(record.content, record.layout?.nodes));
        setEdges(deriveEdges(record.content));
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
        else store.setMessage(error instanceof Error ? error.message : String(error));
      } finally {
        if (alive) setLoading(false);
      }
    })().catch(() => undefined);
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  function buildNodes(content: JanusConfig, layout?: Record<string, { x: number; y: number }>): FlowNode[] {
    const at = (nid: string, kind: NodeKind, index: number) => layout?.[nid] || autoPosition(kind, index);
    const list: FlowNode[] = [];
    Object.keys(content.limens || {}).forEach((name, index) => {
      const nid = nodeId("limen", name);
      const desc = describe(content, "limen", name);
      list.push({ id: nid, type: "janus", position: at(nid, "limen", index), data: { kind: "limen", name, ...desc, onOpen: () => openEditor(nid) } });
    });
    (content.routes || []).forEach((route, index) => {
      const nid = nodeId("route", route.name);
      const desc = describe(content, "route", route.name);
      list.push({
        id: nid,
        type: "janus",
        position: at(nid, "route", index),
        data: { kind: "route", name: route.name, ...desc, onOpen: () => openEditor(nid), onOpenMiddleware: () => openRouteMiddleware(route.name) },
      });
    });
    Object.keys(content.services || {}).forEach((name, index) => {
      const nid = nodeId("service", name);
      const desc = describe(content, "service", name);
      list.push({ id: nid, type: "janus", position: at(nid, "service", index), data: { kind: "service", name, ...desc } });
    });
    return list;
  }

  /** 每次 draft 变更后调用：刷新边与节点副标题/分段（坐标与回调保留）。 */
  function refreshFlow(next: JanusConfig) {
    setEdges(deriveEdges(next));
    setNodes((old) =>
      old.map((node) => {
        const parsed = parseNodeId(node.id);
        if (!parsed) return node;
        const desc = describe(next, parsed.kind, parsed.name);
        if (
          desc.subtitle === node.data.subtitle &&
          desc.detail === node.data.detail &&
          desc.match === node.data.match &&
          desc.action === node.data.action &&
          JSON.stringify(desc.middlewares || []) === JSON.stringify(node.data.middlewares || [])
        ) {
          return node;
        }
        return { ...node, data: { ...node.data, ...desc } };
      }),
    );
  }

  /** 所有 draft 变更的唯一入口：同步更新画布边与节点副标题（坐标保留）。 */
  function mutate(fn: (prev: JanusConfig) => JanusConfig) {
    const prev = draftRef.current;
    if (!prev) return;
    const next = fn(cloneConfig(prev));
    draftRef.current = next;
    setDraft(next);
    refreshFlow(next);
  }

  const onConnect = useCallback(
    (connection: { source: string | null; target: string | null; sourceHandle?: string | null }) => {
      const from = connection.source ? parseNodeId(connection.source) : null;
      const to = connection.target ? parseNodeId(connection.target) : null;
      if (!from || !to) return;
      const draft = draftRef.current;
      if (!draft) return;
      if (from.kind === "limen" && to.kind === "route") {
        mutate((prev) => ({ ...prev, routes: (prev.routes || []).map((r) => (r.name === to.name ? { ...r, limen: from.name } : r)) }));
        return;
      }
      if (from.kind === "route" && to.kind === "service" && connection.sourceHandle === "svc") {
        const route = draft.routes?.find((r) => r.name === from.name);
        if (!route) return;
        if (route.action?.redirect || route.action?.respond) {
          store.setMessage(`Route ${from.name} 当前是直接响应动作，无法转发到 Service。`);
          return;
        }
        mutate((prev) => ({
          ...prev,
          routes: (prev.routes || []).map((r) => (r.name === from.name ? { ...r, action: { forward: { service: to.name } } } : r)),
        }));
        return;
      }
      store.setMessage(`不支持的连接：${from.kind} → ${to.kind}。中间件请在 Route / Service 的编辑器中管理。`);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  function onNodesChange(changes: NodeChange<FlowNode>[]) {
    const removals = changes.filter((c) => c.type === "remove");
    if (removals.length > 0) {
      const removedIds = new Set(removals.filter((c) => c.type === "remove").map((c) => (c as { id: string }).id));
      for (const change of removals) {
        if (change.type === "remove") removeFlowNode(change.id);
      }
      setSelectedIds((old) => old.filter((nid) => !removedIds.has(nid)));
      onNodesChangeDefault(changes.filter((c) => c.type !== "remove"));
      return;
    }
    onNodesChangeDefault(changes);
  }

  function onEdgesChange(changes: EdgeChange[]) {
    const removals = changes.filter((c) => c.type === "remove");
    const rest = changes.filter((c) => c.type !== "remove");
    if (rest.length > 0) onEdgesChangeDefault(rest);
    for (const change of removals) {
      if (change.type !== "remove") continue;
      const edge = edges.find((e) => e.id === change.id);
      if (edge) removeEdgeEffect(edge);
    }
  }

  function removeEdgeEffect(edge: Edge) {
    const from = parseNodeId(edge.source);
    const to = parseNodeId(edge.target);
    if (!from || !to) return;
    if (from.kind === "limen" && to.kind === "route") {
      mutate((prev) => ({ ...prev, routes: (prev.routes || []).map((r) => (r.name === to.name ? { ...r, limen: "" } : r)) }));
    } else if (from.kind === "route" && to.kind === "service") {
      mutate((prev) => ({
        ...prev,
        routes: (prev.routes || []).map((r) => (r.name === from.name && r.action?.forward ? { ...r, action: { forward: { service: "" } } } : r)),
      }));
    }
  }

  function removeFlowNode(nid: string) {
    const parsed = parseNodeId(nid);
    if (!parsed || !draftRef.current) return;
    const draft = draftRef.current;
    if (parsed.kind === "limen") {
      const usedBy = (draft.routes || []).filter((r) => r.limen === parsed.name).map((r) => r.name);
      if (usedBy.length > 0) {
        store.setMessage(`入口 ${parsed.name} 仍被路由引用：${usedBy.join("、")}，请先修改这些路由。`);
        return;
      }
      if (Object.keys(draft.limens || {}).length <= 1) {
        store.setMessage("至少保留 1 个入口。");
        return;
      }
      mutate((prev) => {
        const next = { ...(prev.limens || {}) };
        delete next[parsed.name];
        return { ...prev, limens: next };
      });
    } else if (parsed.kind === "route") {
      mutate((prev) => ({ ...prev, routes: (prev.routes || []).filter((r) => r.name !== parsed.name) }));
    } else if (parsed.kind === "service") {
      const usedBy = (draft.routes || []).filter((r) => (r.action?.forward?.service || r.service) === parsed.name);
      if (usedBy.length > 0) {
        store.setMessage(`Service ${parsed.name} 仍被路由引用：${usedBy.map((r) => r.name).join("、")}。`);
        return;
      }
      mutate((prev) => {
        const next = { ...(prev.services || {}) };
        delete next[parsed.name];
        return { ...prev, services: next };
      });
    }
    setNodes((old) => old.filter((n) => n.id !== nid));
  }

  function addFlowNode(kind: NodeKind, position?: { x: number; y: number }) {
    const draft = draftRef.current;
    if (!draft) return;
    if (kind === "limen") {
      addLimenNode(position);
      return;
    }
    if (kind === "route") {
      const name = uniqueName("route", (draft.routes || []).map((r) => r.name));
      const limens = limenNames(draft);
      const value: Route = { name, limen: limens[0] || "", match: "PathPrefix(`/api`)", action: { forward: { service: "" } }, middlewares: [] };
      mutate((prev) => ({ ...prev, routes: [...(prev.routes || []), value] }));
      const nid = nodeId("route", name);
      const desc = describe({ ...draft, routes: [...(draft.routes || []), value] }, "route", name);
      setNodes((old) => [...old, { id: nid, type: "janus", position: position || autoPosition("route", old.length), data: { kind, name, ...desc, onOpen: () => openEditor(nid), onOpenMiddleware: () => openRouteMiddleware(name) } }]);
      setEditing({ kind: "route", name, value, isNew: true });
    } else if (kind === "service") {
      const name = uniqueName("service", Object.keys(draft.services || {}));
      const value: Service = { upstreams: ["http://127.0.0.1:9000"], middlewares: [] };
      mutate((prev) => ({ ...prev, services: { ...(prev.services || {}), [name]: value } }));
      const nid = nodeId("service", name);
      setNodes((old) => [...old, { id: nid, type: "janus", position: position || autoPosition("service", old.length), data: { kind, name, subtitle: "1 upstream", detail: "0 middleware" } }]);
      setEditing({ kind: "service", name, value, isNew: true });
    }
  }

  function addLimenNode(position?: { x: number; y: number }) {
    const draft = draftRef.current;
    if (!draft) return;
    const name = uniqueName("limen", Object.keys(draft.limens || {}));
    const value: Limen = { address: "127.0.0.1:8080", protocols: ["http1"] };
    mutate((prev) => ({ ...prev, limens: { ...(prev.limens || {}), [name]: value } }));
    const nid = nodeId("limen", name);
    const desc = describe({ ...draft, limens: { ...(draft.limens || {}), [name]: value } }, "limen", name);
    setNodes((old) => [...old, { id: nid, type: "janus", position: position || autoPosition("limen", old.length), data: { kind: "limen", name, ...desc, onOpen: () => openEditor(nid) } }]);
    setEditing({ kind: "limen", name, value, isNew: true });
  }

  function onDrop(event: DragEvent) {
    event.preventDefault();
    const kind = event.dataTransfer.getData("application/janus-node") as NodeKind;
    if (!kind || !KIND_META[kind]) return;
    addFlowNode(kind, screenToFlowPosition({ x: event.clientX, y: event.clientY }));
  }

  function openRouteMiddleware(routeName: string) {
    openEditor(nodeId("route", routeName));
    setMwManager({ scope: "route" });
  }

  function openEditor(nid: string) {
    const parsed = parseNodeId(nid);
    const draft = draftRef.current;
    if (!parsed || !draft) return;
    if (parsed.kind === "route") {
      const route = draft.routes?.find((r) => r.name === parsed.name);
      if (route) setEditing({ kind: "route", name: parsed.name, value: cloneConfig(route), isNew: false });
    } else if (parsed.kind === "service") {
      const service = draft.services?.[parsed.name];
      if (service) setEditing({ kind: "service", name: parsed.name, value: cloneConfig(service), isNew: false });
    } else {
      const limen = draft.limens?.[parsed.name];
      if (limen) setEditing({ kind: "limen", name: parsed.name, value: cloneConfig(limen), isNew: false });
    }
  }

  function confirmEditing() {
    const draft = draftRef.current;
    if (!editing || !draft) return;
    if (editing.kind === "limen") {
      const name = editing.name.trim();
      if (!name) {
        store.setMessage("入口名称不能为空。");
        return;
      }
      if (name !== editing.name && draft.limens?.[name]) {
        store.setMessage(`入口已存在：${name}`);
        return;
      }
      const protocols = editing.value.protocols || [];
      if (!editing.value.address) {
        store.setMessage("请填写监听地址（host:port）。");
        return;
      }
      if (protocols.length === 0) {
        store.setMessage("至少启用 1 个协议。");
        return;
      }
      const tlsOn = Boolean(editing.value.tls);
      if ((protocols.includes("http2") || protocols.includes("http3")) && !tlsOn) {
        store.setMessage("HTTP/2 与 HTTP/3 需要先启用 TLS。");
        return;
      }
      if (protocols.includes("h2c") && tlsOn) {
        store.setMessage("h2c 不能与 TLS 共存。");
        return;
      }
      if (protocols.includes("http3") && !protocols.includes("http1") && !protocols.includes("http2")) {
        store.setMessage("HTTP/3 需要同时保留 HTTP/1.1 或 HTTP/2 作为 TCP 回退。");
        return;
      }
      const createdName = editing.name;
      mutate((prev) => {
        const next = { ...(prev.limens || {}) };
        if (name !== createdName) {
          delete next[createdName];
          if (!editing.isNew) {
            const rename = (limen: string) => (limen === createdName ? name : limen);
            return {
              ...prev,
              limens: { ...next, [name]: editing.value },
              routes: (prev.routes || []).map((r) => (r.limen === createdName ? { ...r, limen: rename(r.limen || "") } : r)),
            };
          }
        }
        return { ...prev, limens: { ...next, [name]: editing.value } };
      });
      if (name !== createdName) {
        setNodes((old) => old.map((n) => (n.id === nodeId("limen", createdName) ? { ...n, id: nodeId("limen", name), data: { ...n.data, name } } : n)));
      }
      setEditing(null);
      return;
    }
    if (editing.kind === "route") {
      const value = { ...editing.value, name: editing.value.name.trim() };
      if (!value.name) {
        store.setMessage("Route 名称不能为空。");
        return;
      }
      if (editing.isNew && (draft.routes || []).some((r) => r.name === value.name)) {
        store.setMessage(`Route 已存在：${value.name}`);
        return;
      }
      mutate((prev) => {
        const list = [...(prev.routes || [])];
        if (editing.isNew) list.push(value);
        else {
          const index = list.findIndex((r) => r.name === value.name);
          if (index >= 0) list[index] = value;
        }
        return { ...prev, routes: list };
      });
      if (editing.isNew) {
        const oldId = nodeId("route", editing.name);
        const newId = nodeId("route", value.name);
        if (oldId !== newId) {
          setNodes((old) => old.map((n) => (n.id === oldId ? { ...n, id: newId, data: { ...n.data, name: value.name } } : n)));
        }
      }
    } else if (editing.kind === "service") {
      const name = editing.name.trim();
      if (!name) {
        store.setMessage("Service 名称不能为空。");
        return;
      }
      if (editing.isNew && draft.services?.[name]) {
        store.setMessage(`Service 已存在：${name}`);
        return;
      }
      mutate((prev) => ({ ...prev, services: { ...(prev.services || {}), [name]: editing.value } }));
      if (editing.isNew && name !== editing.name) {
        setNodes((old) => old.map((n) => (n.id === nodeId("service", editing.name) ? { ...n, id: nodeId("service", name), data: { ...n.data, name } } : n)));
      }
    }
    setEditing(null);
  }

  function deleteEditing() {
    if (!editing || editing.kind === "limen") return;
    const nid = nodeId(editing.kind, editing.name);
    if (editing.isNew) {
      // 新建未确认：直接移除刚创建的节点与数据
      removeFlowNode(nid);
    } else if (window.confirm(`删除 ${editing.name}？该操作会同步清理引用。`)) {
      removeFlowNode(nid);
    } else {
      return;
    }
    setEditing(null);
  }

  // ---- 中间件管理弹窗的回调：class 实例化，instance 改参/改名/删除 ----

  /** 从 class 实例化一个具名定义，返回实例名（调用方决定是否加入 flow）。 */
  function instantiateMiddleware(type: MwType): string | undefined {
    const draft = draftRef.current;
    if (!draft) return undefined;
    const taken = Object.keys(draft.middlewares || {});
    let index = 1;
    let name = `${type}-${index}`;
    while (taken.includes(name)) {
      index += 1;
      name = `${type}-${index}`;
    }
    mutate((prev) => ({ ...prev, middlewares: { ...(prev.middlewares || {}), [name]: classDefaults(type) } }));
    return name;
  }

  function updateMiddlewareInstance(name: string, def: Middleware) {
    mutate((prev) => {
      if (!prev.middlewares?.[name]) return prev;
      return { ...prev, middlewares: { ...prev.middlewares, [name]: def } };
    });
  }

  /** 改名返回错误信息，成功返回 undefined；同步草稿与打开中抽屉的所有引用。 */
  function renameMiddlewareInstance(original: string, nextName: string): string | undefined {
    const name = nextName.trim();
    const draft = draftRef.current;
    if (!draft) return "配置尚未加载。";
    if (!name) return "名称不能为空。";
    if (name !== original && draft.middlewares?.[name]) return `Middleware 已存在：${name}`;
    if (name === original) return undefined;
    const def = draft.middlewares?.[original];
    if (!def) return `找不到 Middleware ${original}。`;
    mutate((prev) => {
      const nextMiddlewares = { ...(prev.middlewares || {}) };
      delete nextMiddlewares[original];
      const rename = (list: string[] | undefined) => (list || []).map((m) => (m === original ? name : m));
      return {
        ...prev,
        middlewares: { ...nextMiddlewares, [name]: prev.middlewares?.[original] || def },
        routes: (prev.routes || []).map((r) => ({ ...r, middlewares: rename(r.middlewares) })),
        services: Object.fromEntries(Object.entries(prev.services || {}).map(([serviceName, service]) => [serviceName, { ...service, middlewares: rename(service.middlewares) }])),
      };
    });
    const rename = (list: string[] | undefined) => (list || []).map((m) => (m === original ? name : m));
    setEditing((old) => {
      if (!old) return old;
      if (old.kind === "route") return { ...old, value: { ...old.value, middlewares: rename(old.value.middlewares) } };
      if (old.kind === "service") return { ...old, value: { ...old.value, middlewares: rename(old.value.middlewares) } };
      return old;
    });
    return undefined;
  }

  /** 删除中间件定义并同步清理草稿与打开中抽屉内的所有引用。 */
  function removeMiddlewareDef(original: string) {
    const draft = draftRef.current;
    if (!draft) return;
    const usedRoutes = (draft.routes || []).filter((r) => (r.middlewares || []).includes(original)).map((r) => r.name);
    const usedServices = Object.entries(draft.services || {}).filter(([, s]) => (s.middlewares || []).includes(original)).map(([serviceName]) => serviceName);
    // 包含正打开的抽屉中尚未确认的引用
    const openRefs: string[] = [];
    if (editing && (editing.kind === "route" || editing.kind === "service") && (editing.value.middlewares || []).includes(original)) {
      openRefs.push(editing.kind === "route" ? `Route ${editing.name}（编辑中）` : `Service ${editing.name}（编辑中）`);
    }
    const total = usedRoutes.length + usedServices.length + openRefs.length;
    if (!window.confirm(`删除 Middleware ${original}？会同步从 ${total} 处引用移除。`)) return;
    const stale = original;
    mutate((prev) => {
      const next = { ...(prev.middlewares || {}) };
      delete next[stale];
      const strip = (list: string[] | undefined) => (list || []).filter((m) => m !== stale);
      return {
        ...prev,
        middlewares: next,
        routes: (prev.routes || []).map((r) => ({ ...r, middlewares: strip(r.middlewares) })),
        services: Object.fromEntries(Object.entries(prev.services || {}).map(([serviceName, service]) => [serviceName, { ...service, middlewares: strip(service.middlewares) }])),
      };
    });
    setEditing((old) => {
      if (!old) return old;
      if (old.kind === "route") return { ...old, value: { ...old.value, middlewares: (old.value.middlewares || []).filter((m) => m !== stale) } };
      if (old.kind === "service") return { ...old, value: { ...old.value, middlewares: (old.value.middlewares || []).filter((m) => m !== stale) } };
      return old;
    });
  }

  // ---- 注册中心管理弹窗的回调 ----

  function createRegistry(): string | undefined {
    const draft = draftRef.current;
    if (!draft) return undefined;
    const name = uniqueName("registry", Object.keys(draft.discovery?.nacos || {}));
    mutate((prev) => ({
      ...prev,
      discovery: {
        ...(prev.discovery || {}),
        nacos: {
          ...(prev.discovery?.nacos || {}),
          [name]: { servers: [{ address: "127.0.0.1", port: 8848 }], namespace_id: "public", timeout: "5s", stale_after: "2m" },
        },
      },
    }));
    return name;
  }

  function renameNewRegistry(oldName: string, newName: string): string | undefined {
    const name = newName.trim();
    const draft = draftRef.current;
    if (!draft) return "配置尚未加载。";
    if (!name) return "名称不能为空。";
    if (name !== oldName && draft.discovery?.nacos?.[name]) return `Registry 已存在：${name}`;
    if (name === oldName) return undefined;
    const def = draft.discovery?.nacos?.[oldName];
    if (!def) return `找不到 Registry ${oldName}。`;
    mutate((prev) => {
      const next = { ...(prev.discovery?.nacos || {}) };
      delete next[oldName];
      next[name] = prev.discovery?.nacos?.[oldName] || def;
      return { ...prev, discovery: { ...(prev.discovery || {}), nacos: next } };
    });
    return undefined;
  }

  function updateRegistry(name: string, reg: NacosRegistry) {
    mutate((prev) => {
      if (!prev.discovery?.nacos?.[name]) return prev;
      return { ...prev, discovery: { ...(prev.discovery || {}), nacos: { ...(prev.discovery?.nacos || {}), [name]: reg } } };
    });
  }

  function deleteRegistry(name: string) {
    const draft = draftRef.current;
    if (!draft) return;
    const usedBy = Object.entries(draft.services || {}).filter(([, s]) => s.nacos?.registry === name).map(([serviceName]) => serviceName);
    let openName: string | null = null;
    if (editing && editing.kind === "service" && editing.value.nacos?.registry === name) {
      openName = editing.name;
    }
    if (usedBy.length > 0 || openName != null) {
      const who = [...usedBy.map((s) => `Service ${s}`), ...(openName != null ? [`Service ${openName}（编辑中）`] : [])];
      store.setMessage(`Registry ${name} 仍被引用：${who.join("、")}，请先修改这些 Service。`);
      return;
    }
    if (!window.confirm(`删除 Registry ${name}？`)) return;
    mutate((prev) => {
      const next = { ...(prev.discovery?.nacos || {}) };
      delete next[name];
      return { ...prev, discovery: { ...(prev.discovery || {}), nacos: next } };
    });
  }

  function selectRegistry(name: string) {
    setEditing((old) => {
      if (!old || old.kind !== "service" || !old.value.nacos) return old;
      return { ...old, value: { ...old.value, nacos: { ...old.value.nacos, registry: name } } };
    });
    setRegManager(null);
  }

  function deleteSelected() {
    if (selectedIds.length === 0) return;
    if (!window.confirm(`删除选中的 ${selectedIds.length} 个节点？引用会同步清理，被引用的节点会拒绝并提示。`)) return;
    for (const nid of selectedIds) removeFlowNode(nid);
    setSelectedIds([]);
  }

  function tidyLayout() {
    const draft = draftRef.current;
    if (!draft) return;
    const positions = computeTidyPositions(draft);
    setNodes((old) => old.map((node) => (positions[node.id] ? { ...node, position: positions[node.id] } : node)));
    store.setMessage("已按引用关系整理布局，保存草稿后生效。");
  }

  function currentLayout(): Record<string, { x: number; y: number }> {
  return Object.fromEntries(nodes.map((n) => [n.id, { x: Math.round(n.position.x), y: Math.round(n.position.y) }]));
}

/** 洋葱栈的核心：中间件包裹的是动作，而不一定是 Service。 */
function routeCoreLabel(route: Route): string {
  if (route.action?.redirect) return `↗ Redirect → ${route.action.redirect.location || ""}`;
  if (route.action?.respond) return `直接响应 ${route.action.respond.status ?? 200}`;
  const service = route.action?.forward?.service || route.service || "未选择";
  return `→ Service ${service}`;
}

  async function save() {
    const draft = draftRef.current;
    if (!draft) return;
    const problems = validateLocal(draft);
    if (problems.length > 0) {
      store.setMessage(`本地检查未通过：${problems[0]}`);
      return false;
    }
    setBusy(true);
    try {
      const layout = { nodes: currentLayout() };
      await saveStoredConfig(id, { content: draft, layout });
      setSaved(JSON.stringify(draft));
      setSavedLayout(JSON.stringify(layout.nodes));
      store.setMessage("草稿已保存。");
      return true;
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
      else store.setMessage(error instanceof Error ? error.message : String(error));
      return false;
    } finally {
      setBusy(false);
    }
  }

  /** 将已保存草稿的入口写入生效文件（运行不受影响，重启后生效）。 */
  async function stageSavedLimens() {
    if (dirty) {
      store.setMessage("草稿有未保存的修改，请先确认抽屉并保存草稿，再写入文件。");
      return;
    }
    if (!window.confirm("将本配置已保存的入口写入生效文件？运行不受影响，重启 Janus 后生效。")) return;
    setBusy(true);
    try {
      const result = await stageLimens(id);
      store.setMessage(
        result.warning
          ? `入口已写入文件，重启后生效。注意：${result.warning}`
          : "入口已写入文件，重启 Janus 后生效。",
      );
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
      else store.setMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  async function validate() {
    const draft = draftRef.current;
    if (!draft) return;
    const problems = validateLocal(draft);
    if (problems.length > 0) {
      store.setMessage(`本地检查未通过：${problems[0]}`);
      return;
    }
    setBusy(true);
    try {
      await validateConfig(draft);
      store.setMessage("配置校验通过。");
    } catch (error) {
      store.setMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  async function publish() {
    if (dirty && !(await save())) return;
    if (!window.confirm(`发布配置「${meta?.name}」？当前生效配置将被替换。`)) return;
    setBusy(true);
    try {
      const result = await publishStoredConfig(id);
      setMeta((old) => (old ? { ...old, status: "active" } : old));
      store.setMessage(
        result.warning
          ? `已发布，网关 generation ${result.revision}。注意：${translateWarning(result.warning)}`
          : `已发布，网关 generation ${result.revision}。`,
      );
      onStatusChange();
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      store.setMessage(
        /limen/i.test(message)
          ? `${message}（入口是启动级的：新增/修改入口需改文件并重启后再生效）`
          : message,
      );
    } finally {
      setBusy(false);
    }
  }

function translateWarning(warning: string): string {
  if (/limens or settings/i.test(warning)) {
    return "入口或全局设置与线上不同，已按线上版本发布路由部分；入口/设置变更需改文件并重启。";
  }
  return warning;
}

  if (loading || !draft) return <Empty text="正在加载配置…" />;

  return (
    <section className="editor-shell">
      <div className="editor-topbar">
        <button type="button" className="btn ghost" onClick={() => { if (!dirty || window.confirm("有未保存的草稿，离开会丢失。继续吗？")) onBack(); }}>
          ← 返回列表
        </button>
        <div className="editor-title">
          <strong>{meta?.name}</strong>
          <Badge text={meta?.status || ""} variant={meta?.status === "active" ? "success" : "warn"} />
          <span className={`draft-state ${dirty ? "dirty" : ""}`}>{dirty ? "未保存" : "已保存"}</span>
        </div>
        <div className="header-actions">
          <button type="button" className="btn ghost" disabled={busy} onClick={validate}>校验</button>
          <button type="button" className="btn ghost" disabled={busy} onClick={tidyLayout}>整理布局</button>
          <button type="button" className="btn ghost" disabled={busy || !dirty} onClick={() => save()}>保存草稿</button>
          <button type="button" className="btn primary" disabled={busy} onClick={publish}>发布</button>
          {selectedIds.length > 0 && (
            <button type="button" className="btn danger" disabled={busy} onClick={deleteSelected}>
              删除选中（{selectedIds.length}）
            </button>
          )}
        </div>
      </div>
      <div className="editor-body">
        <aside className="palette">
          <div className="palette-head"><h3>节点</h3><span>拖入画布或点击添加</span></div>
          {(["limen", "route", "service"] as const).map((kind) => (
            <div
              key={kind}
              className="palette-item"
              draggable
              onDragStart={(e) => e.dataTransfer.setData("application/janus-node", kind)}
              onClick={() => addFlowNode(kind)}
              role="button"
              tabIndex={0}
            >
              <span className="palette-icon" style={{ background: KIND_META[kind].color }}>{KIND_META[kind].icon}</span>
              <span><strong>{KIND_META[kind].label}</strong><small>{kind === "route" ? "匹配规则与分发" : kind === "service" ? "上游池" : "监听入口（启动级）"}</small></span>
              <b>＋</b>
            </div>
          ))}
          <div className="palette-note">
            <strong>连线规则</strong>
            <span>Limen → Route 指定入口</span>
            <span>Route → Service 设置转发</span>
            <small>中间件在 Route / Service 的编辑器中新建、改参、排序。</small>
            <small>入口监听配置是启动级的，新增/修改后需重启；选中节点按 Backspace/Delete 删除，双击编辑。</small>
          </div>
          <button type="button" className="btn ghost" onClick={() => setRegManager({ allowSelect: false })}>注册中心管理</button>
        </aside>
        <div className="flow-wrap" onDrop={onDrop} onDragOver={(e) => e.preventDefault()}>
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeDoubleClick={(_, node) => openEditor(node.id)}
            onSelectionChange={({ nodes: selected }) => setSelectedIds(selected.map((n) => n.id))}
            nodeTypes={nodeTypes}
            deleteKeyCode={["Backspace", "Delete"]}
            fitView
            fitViewOptions={{ padding: 0.2 }}
            minZoom={0.3}
            maxZoom={1.5}
          >
            <Background gap={20} size={1} />
            <Controls position="bottom-right" />
            <MiniMap position="bottom-left" nodeColor={(n) => KIND_META[(n.data as NodeData)?.kind || "route"].color} />
          </ReactFlow>
        </div>
      </div>
      {editing?.kind === "route" && (
        <RouteEditor
          draft={draft}
          value={editing.value}
          isNew={editing.isNew}
          onChange={(value) => setEditing({ ...editing, value })}
          onConfirm={confirmEditing}
          onCancel={() => { if (editing.isNew) removeFlowNode(nodeId("route", editing.name)); setEditing(null); }}
          onClose={() => { if (editing.isNew) removeFlowNode(nodeId("route", editing.name)); setEditing(null); }}
          onDelete={editing.isNew ? undefined : deleteEditing}
          onOpenMiddlewareManager={() => setMwManager({ scope: "route" })}
        />
      )}
      {editing?.kind === "service" && (
        <ServiceEditor
          draft={draft}
          name={editing.name}
          value={editing.value}
          isNew={editing.isNew}
          onName={editing.isNew ? (name) => setEditing({ ...editing, name }) : undefined}
          onChange={(value) => setEditing({ ...editing, value })}
          onConfirm={confirmEditing}
          onCancel={() => { if (editing.isNew) removeFlowNode(nodeId("service", editing.name)); setEditing(null); }}
          onClose={() => { if (editing.isNew) removeFlowNode(nodeId("service", editing.name)); setEditing(null); }}
          onDelete={editing.isNew ? undefined : deleteEditing}
          onOpenMiddlewareManager={() => setMwManager({ scope: "service" })}
          onOpenRegistryManager={() => setRegManager({ allowSelect: true })}
        />
      )}
      {editing?.kind === "limen" && (
        <LimenEditor
          name={editing.name}
          value={editing.value}
          isNew={editing.isNew}
          activeExists={Boolean(store.draft?.limens?.[editing.name])}
          onName={(name) => setEditing({ ...editing, name })}
          onChange={(value) => setEditing({ ...editing, value })}
          onConfirm={confirmEditing}
          onCancel={() => { if (editing.isNew) removeFlowNode(nodeId("limen", editing.name)); setEditing(null); }}
          onClose={() => { if (editing.isNew) removeFlowNode(nodeId("limen", editing.name)); setEditing(null); }}
          onDelete={editing.isNew ? undefined : deleteEditing}
          onStageLimens={stageSavedLimens}
        />
      )}
      {mwManager && editing && (editing.kind === "route" || editing.kind === "service") && (
        <MiddlewareManagerModal
          title={`${editing.kind === "route" ? "Route" : "Service"} ${editing.name} · 中间件管理`}
          scope={mwManager.scope}
          coreLabel={editing.kind === "route" ? routeCoreLabel(editing.value) : `Service ${editing.name} → 上游`}
          instances={draft.middlewares || {}}
          flow={editing.value.middlewares || []}
          onFlowChange={(flow) => {
            if (editing.kind === "route") setEditing({ ...editing, value: { ...editing.value, middlewares: flow } });
            else setEditing({ ...editing, value: { ...editing.value, middlewares: flow } });
          }}
          onInstantiate={instantiateMiddleware}
          onUpdateInstance={updateMiddlewareInstance}
          onDeleteInstance={removeMiddlewareDef}
          onRenameInstance={renameMiddlewareInstance}
          onClose={() => setMwManager(null)}
          notify={store.setMessage}
        />
      )}
      {(regManager) && (
        <RegistryManagerModal
          title="注册中心管理"
          registries={draft.discovery?.nacos || {}}
          allowSelect={regManager.allowSelect}
          onSelect={selectRegistry}
          onCreate={createRegistry}
          onRenameNew={renameNewRegistry}
          onUpdate={updateRegistry}
          onDelete={deleteRegistry}
          onClose={() => setRegManager(null)}
          notify={store.setMessage}
        />
      )}
    </section>
  );
}
