import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import {
  Background,
  BaseEdge,
  Controls,
  getSmoothStepPath,
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
  type EdgeProps,
  type Node,
  type NodeChange,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  ApiError,
  getMiddlewareCapabilities,
  getStoredConfig,
  publishStoredConfig,
  saveStoredConfig,
  stageLimens,
  validateConfig,
  type StoredConfig,
} from "./api";
import { createMiddlewareDefinition, LimenEditor, ServiceEditor } from "./editors";
import { cloneConfig, limenNames, routeActionLabel, routeMatchLabel, serviceSubtitle, uniqueName } from "./model";
import { MiddlewareManagerModal, type MiddlewareScopeFilter } from "./MiddlewareManager";
import { RegistryManagerModal } from "./RegistryManager";
import { RouteEditor } from "./RouteEditor";
import { Badge, Drawer, Empty, useDialogController } from "./ui";
import type { ConfigStore } from "./useConfig";
import { validateLocal } from "./validate";
import type { JanusConfig, Limen, Middleware, MiddlewareCapability, NacosRegistry, Route, Service } from "./types";

type NodeKind = "limen" | "route" | "service";
type EditorView = "canvas" | "rules" | "json";
type RuleSort = "priority" | "name" | "order";
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
        type: "janus",
      });
    }
    const service = route.action?.forward?.service || route.service || "";
    if (service && draft.services?.[service]) {
      edges.push({
        id: `route:${route.name}--svc->service:${service}`,
        source: `route:${route.name}`,
        sourceHandle: "svc",
        target: `service:${service}`,
        type: "janus",
        animated: true,
      });
    }
  }
  return edges;
}

function JanusEdge({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition, selected }: EdgeProps) {
  const [edgePath] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition, borderRadius: 12, offset: 22 });
  return <BaseEdge path={edgePath} interactionWidth={28} style={{ stroke: selected ? "#356fcf" : "#91a7bf", strokeWidth: selected ? 4.5 : 2.5, filter: selected ? "drop-shadow(0 0 0.5px #fff) drop-shadow(0 0 3px #356fcf66)" : "none" }} />;
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
const edgeTypes = { janus: JanusEdge };

type EditorState =
  | { kind: "route"; name: string; originalName: string; value: Route; isNew: boolean }
  | { kind: "service"; name: string; originalName: string; value: Service; isNew: boolean }
  | { kind: "limen"; name: string; originalName: string; value: Limen; isNew: boolean };

type ManagerSnapshot = { draft: JanusConfig; editing: EditorState | null };

export function ConfigEditorPage({ store, id, onBack, onStatusChange }: { store: ConfigStore; id: number; onBack: () => void; onStatusChange: () => void }) {
  return (
    <ReactFlowProvider>
      <ConfigEditor store={store} id={id} onBack={onBack} onStatusChange={onStatusChange} />
    </ReactFlowProvider>
  );
}

function ConfigEditor({ store, id, onBack, onStatusChange }: { store: ConfigStore; id: number; onBack: () => void; onStatusChange: () => void }) {
  const [workingId, setWorkingId] = useState(id);
  const [meta, setMeta] = useState<{ name: string; status: string } | null>(null);
  const [draft, setDraft] = useState<JanusConfig | null>(null);
  const [saved, setSaved] = useState("");
  const [savedLayout, setSavedLayout] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<EditorState | null>(null);
  const [mwManager, setMwManager] = useState<{ scope: MiddlewareScopeFilter; snapshot: ManagerSnapshot } | null>(null);
  const [middlewareCatalog, setMiddlewareCatalog] = useState<MiddlewareCapability[]>([]);
  const [regManager, setRegManager] = useState<{ allowSelect: boolean; snapshot: ManagerSnapshot } | null>(null);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [viewMode, setViewMode] = useState<EditorView>("canvas");
  const [ruleSort, setRuleSort] = useState<RuleSort>("priority");
  const [limenFilter, setLimenFilter] = useState("all");
  const [jsonText, setJsonText] = useState("");
  const [jsonError, setJsonError] = useState("");
  const [nodes, setNodes, onNodesChangeDefault] = useNodesState<FlowNode>([]);
  const [edges, setEdges, onEdgesChangeDefault] = useEdgesState<Edge>([]);
  const { screenToFlowPosition } = useReactFlow();
  const dialogs = useDialogController();
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
    setWorkingId(id);
    (async () => {
      setLoading(true);
      try {
        const [record, capabilities]: [StoredConfig, { middlewares: MiddlewareCapability[] }] = await Promise.all([
          getStoredConfig(id),
          getMiddlewareCapabilities(),
        ]);
        if (!alive) return;
        setMeta({ name: record.name, status: record.status });
        setMiddlewareCatalog(capabilities.middlewares);
        setDraft(record.content);
        setJsonText(JSON.stringify(record.content, null, 2));
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

  function managerSnapshot(editor: EditorState | null = editing): ManagerSnapshot | null {
    const current = draftRef.current;
    if (!current) return null;
    return { draft: cloneConfig(current), editing: editor ? cloneConfig(editor) : null };
  }

  function openMiddlewareManager(scope: MiddlewareScopeFilter, editor: EditorState | null = editing) {
    const snapshot = managerSnapshot(editor);
    if (snapshot) setMwManager({ scope, snapshot });
  }

  function openRegistryManager(allowSelect: boolean) {
    const snapshot = managerSnapshot();
    if (snapshot) setRegManager({ allowSelect, snapshot });
  }

  function restoreManager(snapshot: ManagerSnapshot) {
    const restored = cloneConfig(snapshot.draft);
    draftRef.current = restored;
    setDraft(restored);
    refreshFlow(restored);
    setEditing(snapshot.editing ? cloneConfig(snapshot.editing) : null);
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
      store.setMessage("Limen 是启动级入口，不能热发布；双击已有 Limen 可编辑，确认后写入草稿，写入生效文件并重启后生效。");
      return;
    }
    if (kind === "route") {
      const name = uniqueName("route", (draft.routes || []).map((r) => r.name));
      const limens = limenNames(draft);
      const services = Object.keys(draft.services || {});
      const value: Route = { name, limen: limens[0] || "", match: "PathPrefix(`/api`)", action: { forward: { service: services[0] || "" } }, middlewares: [] };
      mutate((prev) => ({ ...prev, routes: [...(prev.routes || []), value] }));
      const nid = nodeId("route", name);
      const desc = describe({ ...draft, routes: [...(draft.routes || []), value] }, "route", name);
      setNodes((old) => [...old, { id: nid, type: "janus", position: position || autoPosition("route", old.length), data: { kind, name, ...desc, onOpen: () => openEditor(nid), onOpenMiddleware: () => openRouteMiddleware(name) } }]);
      setEditing({ kind: "route", name, originalName: name, value, isNew: true });
    } else if (kind === "service") {
      const name = uniqueName("service", Object.keys(draft.services || {}));
      const value: Service = { upstreams: ["http://127.0.0.1:9000"], middlewares: [] };
      mutate((prev) => ({ ...prev, services: { ...(prev.services || {}), [name]: value } }));
      const nid = nodeId("service", name);
      setNodes((old) => [...old, { id: nid, type: "janus", position: position || autoPosition("service", old.length), data: { kind, name, subtitle: "1 upstream", detail: "0 middleware" } }]);
      setEditing({ kind: "service", name, originalName: name, value, isNew: true });
    }
  }

  function onDrop(event: DragEvent) {
    event.preventDefault();
    const kind = event.dataTransfer.getData("application/janus-node") as NodeKind;
    if (!kind || !KIND_META[kind]) return;
    addFlowNode(kind, screenToFlowPosition({ x: event.clientX, y: event.clientY }));
  }

  function openRouteMiddleware(routeName: string) {
    const current = draftRef.current;
    const route = current?.routes?.find((item) => item.name === routeName);
    if (!route) return;
    const editor: EditorState = { kind: "route", name: routeName, originalName: routeName, value: cloneConfig(route), isNew: false };
    setEditing(editor);
    openMiddlewareManager("route", editor);
  }

  function openEditor(nid: string) {
    const parsed = parseNodeId(nid);
    const draft = draftRef.current;
    if (!parsed || !draft) return;
    if (parsed.kind === "route") {
      const route = draft.routes?.find((r) => r.name === parsed.name);
      if (route) setEditing({ kind: "route", name: parsed.name, originalName: parsed.name, value: cloneConfig(route), isNew: false });
    } else if (parsed.kind === "service") {
      const service = draft.services?.[parsed.name];
      if (service) setEditing({ kind: "service", name: parsed.name, originalName: parsed.name, value: cloneConfig(service), isNew: false });
    } else {
      const limen = draft.limens?.[parsed.name];
      if (limen) setEditing({ kind: "limen", name: parsed.name, originalName: parsed.name, value: cloneConfig(limen), isNew: false });
    }
  }

  function confirmEditing() {
    const draft = draftRef.current;
    if (!editing || !draft) return;
    if (editing.kind === "limen") {
      const name = editing.name.trim();
      const originalName = editing.originalName;
      if (!name) {
        store.setMessage("入口名称不能为空。");
        return;
      }
      if (name !== originalName && draft.limens?.[name]) {
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
      mutate((prev) => {
        const next = { ...(prev.limens || {}) };
        if (name !== originalName) {
          delete next[originalName];
          if (!editing.isNew) {
            const rename = (limen: string) => (limen === originalName ? name : limen);
            return {
              ...prev,
              limens: { ...next, [name]: editing.value },
              routes: (prev.routes || []).map((r) => (r.limen === originalName ? { ...r, limen: rename(r.limen || "") } : r)),
            };
          }
        }
        return { ...prev, limens: { ...next, [name]: editing.value } };
      });
      if (name !== originalName) {
        const newID = nodeId("limen", name);
        const description = describe(draftRef.current || draft, "limen", name);
        setNodes((old) => old.map((n) => (n.id === nodeId("limen", originalName) ? { ...n, id: newID, data: { ...n.data, name, ...description, onOpen: () => openEditor(newID) } } : n)));
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
      // 新建节点会先以 editing.name 作为临时资源写入 draft，供画布和
      // 嵌套编辑器即时预览。判重时必须排除这个临时资源。
      if (editing.isNew && value.name !== editing.originalName && (draft.routes || []).some((r) => r.name === value.name)) {
        store.setMessage(`Route 已存在：${value.name}`);
        return;
      }
      mutate((prev) => {
        const list = [...(prev.routes || [])];
        const lookupName = editing.isNew ? editing.originalName : value.name;
        const index = list.findIndex((r) => r.name === lookupName);
        if (index >= 0) list[index] = value;
        else list.push(value);
        return { ...prev, routes: list };
      });
      if (editing.isNew) {
        const oldId = nodeId("route", editing.originalName);
        const newId = nodeId("route", value.name);
        if (oldId !== newId) {
          const description = describe(draftRef.current || draft, "route", value.name);
          setNodes((old) => old.map((n) => (n.id === oldId ? {
            ...n,
            id: newId,
            data: {
              ...n.data,
              name: value.name,
              ...description,
              onOpen: () => openEditor(newId),
              onOpenMiddleware: () => openRouteMiddleware(value.name),
            },
          } : n)));
        }
      }
    } else if (editing.kind === "service") {
      const name = editing.name.trim();
      const originalName = editing.originalName;
      if (!name) {
        store.setMessage("Service 名称不能为空。");
        return;
      }
      if (editing.isNew && name !== originalName && draft.services?.[name]) {
        store.setMessage(`Service 已存在：${name}`);
        return;
      }
      mutate((prev) => {
        const services = { ...(prev.services || {}) };
        if (editing.isNew && name !== originalName) delete services[originalName];
        services[name] = editing.value;
        return { ...prev, services };
      });
      if (editing.isNew && name !== originalName) {
        const description = describe(draftRef.current || draft, "service", name);
        setNodes((old) => old.map((n) => (n.id === nodeId("service", originalName) ? { ...n, id: nodeId("service", name), data: { ...n.data, name, ...description } } : n)));
      }
    }
    setEditing(null);
  }

  async function deleteEditing() {
    if (!editing || editing.kind === "limen") return;
    const nid = nodeId(editing.kind, editing.originalName);
    if (editing.isNew) {
      // 新建未确认：直接移除刚创建的节点与数据
      removeFlowNode(nid);
    } else if (await dialogs.confirm({
      title: `删除 ${editing.kind === "route" ? "Route" : "Service"}`,
      message: `删除「${editing.name}」后，相关引用会同步清理。该操作不可撤销。`,
      confirmLabel: "确认删除",
      tone: "danger",
    })) {
      removeFlowNode(nid);
    } else {
      return;
    }
    setEditing(null);
  }

  // ---- 中间件管理弹窗的回调：class 实例化，instance 改参/改名/删除 ----

  /** 从 class 实例化一个具名定义，返回实例名（调用方决定是否加入 flow）。 */
  function instantiateMiddleware(type: string): string | undefined {
    const draft = draftRef.current;
    if (!draft) return undefined;
    const capability = middlewareCatalog.find((item) => item.type === type);
    if (!capability) {
      store.setMessage(`后端未提供 Middleware 类型：${type}`);
      return undefined;
    }
    const taken = Object.keys(draft.middlewares || {});
    let index = 1;
    let name = `${type}-${index}`;
    while (taken.includes(name)) {
      index += 1;
      name = `${type}-${index}`;
    }
    mutate((prev) => ({ ...prev, middlewares: { ...(prev.middlewares || {}), [name]: createMiddlewareDefinition(capability) } }));
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
  async function removeMiddlewareDef(original: string) {
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
    if (!(await dialogs.confirm({
      title: "删除 Middleware",
      message: `删除「${original}」后，会同步从 ${total} 处引用中移除。该操作不可撤销。`,
      confirmLabel: "确认删除",
      tone: "danger",
    }))) return;
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

  async function deleteRegistry(name: string) {
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
    if (!(await dialogs.confirm({
      title: "删除 Registry",
      message: `确定删除「${name}」吗？请先确认没有 Service 依赖它。`,
      confirmLabel: "确认删除",
      tone: "danger",
    }))) return;
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

  async function deleteSelected() {
    if (selectedIds.length === 0) return;
    if (!(await dialogs.confirm({
      title: "删除选中节点",
      message: `确定删除选中的 ${selectedIds.length} 个节点吗？引用会同步清理，被引用的节点会拒绝并提示。`,
      confirmLabel: "删除节点",
      tone: "danger",
    }))) return;
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

  async function switchView(next: EditorView) {
    if (viewMode === "json" && draft && jsonText.trim() !== JSON.stringify(draft, null, 2)) {
      if (!(await dialogs.confirm({
        title: "放弃 JSON 修改？",
        message: "当前 JSON 还有未应用的修改，切换视图会丢失这些内容。",
        confirmLabel: "放弃并切换",
        tone: "warning",
      }))) return;
    }
    if (next === "json" && draft) setJsonText(JSON.stringify(draft, null, 2));
    setJsonError("");
    setViewMode(next);
  }

  function applyJson() {
    try {
      const parsed = JSON.parse(jsonText) as JanusConfig;
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("根节点必须是 JSON 对象。");
      if (!Array.isArray(parsed.routes)) throw new Error("配置必须包含 routes 数组。");
      const next = cloneConfig(parsed);
      draftRef.current = next;
      setDraft(next);
      setNodes(buildNodes(next));
      setEdges(deriveEdges(next));
      setJsonText(JSON.stringify(next, null, 2));
      setJsonError("");
      store.setMessage("JSON 已应用到草稿，请保存或发布以继续。");
    } catch (error) {
      setJsonError(error instanceof Error ? error.message : "JSON 解析失败。");
    }
  }

  function reorderRoute(name: string, direction: -1 | 1) {
    mutate((prev) => {
      const routes = [...(prev.routes || [])];
      const index = routes.findIndex((route) => route.name === name);
      const nextIndex = index + direction;
      if (index < 0 || nextIndex < 0 || nextIndex >= routes.length) return prev;
      [routes[index], routes[nextIndex]] = [routes[nextIndex], routes[index]];
      return { ...prev, routes };
    });
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
    if (!draft) return 0;
    const problems = validateLocal(draft, middlewareCatalog);
    if (problems.length > 0) {
      store.setMessage(`本地检查未通过：${problems[0]}`);
      return 0;
    }
    setBusy(true);
    try {
      const layout = { nodes: currentLayout() };
      const result = await saveStoredConfig(workingId, { content: draft, layout });
      if (result.id !== workingId) {
        setWorkingId(result.id);
        setMeta((old) => (old ? { ...old, status: result.status || "draft" } : old));
        onStatusChange();
      }
      setSaved(JSON.stringify(draft));
      setSavedLayout(JSON.stringify(layout.nodes));
      store.setMessage(result.forked_from ? "已从生效配置创建可编辑草稿。" : "草稿已保存。");
      return result.id;
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
      else if (error instanceof ApiError && error.status === 409) {
        store.setMessage("配置版本发生冲突，请刷新后重试。");
      } else store.setMessage(error instanceof Error ? error.message : String(error));
      return 0;
    } finally {
      setBusy(false);
    }
  }

  /** 将已保存草稿的入口写入生效文件（运行不受影响，重启后生效）。 */
  async function stageSavedLimens() {
    if (editing?.kind === "limen") {
      store.setMessage("请先点击入口编辑器的“确认”保存草稿，再写入生效文件。取消则不会保留本次修改。");
      return;
    }
    if (dirty) {
      store.setMessage("草稿有未保存的修改，请先确认抽屉并保存草稿，再写入文件。");
      return;
    }
    if (!(await dialogs.confirm({
      title: "写入入口配置",
      message: "将已保存的入口写入生效文件。当前运行不受影响，重启 Janus 后才会生效。",
      confirmLabel: "写入文件",
      tone: "warning",
    }))) return;
    setBusy(true);
    try {
      const result = await stageLimens(workingId);
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
    const problems = validateLocal(draft, middlewareCatalog);
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
    const targetId = dirty ? await save() : workingId;
    if (!targetId) return;
    if (!(await dialogs.confirm({
      title: "发布配置",
      message: `当前生效配置将被「${meta?.name}」替换，路由变化会立即进入新的 generation。`,
      confirmLabel: "确认发布",
      tone: "warning",
    }))) return;
    setBusy(true);
    try {
      const result = await publishStoredConfig(targetId, store.revision);
      setMeta((old) => (old ? { ...old, status: "active" } : old));
      await store.load(true);
      store.setMessage(
        result.warning
          ? `已发布，网关 generation ${result.revision}。注意：${translateWarning(result.warning)}`
          : `已发布，网关 generation ${result.revision}。`,
      );
      onStatusChange();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        await store.load(true);
        store.setMessage("版本冲突：远端已被他人更新。已刷新当前配置，请确认后再发布。");
        return;
      }
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

  async function leaveEditor() {
    if (!dirty || await dialogs.confirm({
      title: "离开配置编辑",
      message: "当前有未保存的草稿，离开后这些修改会丢失。",
      confirmLabel: "放弃并离开",
      tone: "warning",
    })) {
      onBack();
    }
  }

  if (loading || !draft) return <Empty text="正在加载配置…" />;

  const routes = [...(draft.routes || [])];
  const limenOptions = Object.keys(draft.limens || {}).sort();
  const visibleRoutes = routes
    .map((route, index) => ({ route, index }))
    .filter(({ route }) => {
      const boundLimen = route.limen || (limenOptions.length === 1 ? limenOptions[0] : "");
      return limenFilter === "all" || (limenFilter === "__unbound" ? !boundLimen : boundLimen === limenFilter);
    })
    .sort((a, b) => {
      if (ruleSort === "name") return a.route.name.localeCompare(b.route.name);
      if (ruleSort === "order") return a.index - b.index;
      return (b.route.priority || 0) - (a.route.priority || 0) || a.index - b.index;
    })
    .map((entry, displayIndex) => ({ ...entry, displayIndex }));

  return (
    <section className="editor-shell editor-page">
      <div className="editor-topbar">
        <button type="button" className="btn ghost" onClick={leaveEditor}>
          ← 返回列表
        </button>
        <div className="editor-title">
          <strong>{meta?.name}</strong>
          <Badge text={meta?.status || ""} variant={meta?.status === "active" ? "success" : "warn"} />
          <span className={`draft-state ${dirty ? "dirty" : ""}`}>{dirty ? "未保存" : "已保存"}</span>
        </div>
        <div className="editor-view-switcher" role="tablist" aria-label="配置视图">
          {([ ["canvas", "画布"], ["rules", "规则"], ["json", "JSON"] ] as const).map(([mode, label]) => (
            <button key={mode} type="button" role="tab" aria-selected={viewMode === mode} className={viewMode === mode ? "active" : ""} onClick={() => switchView(mode)}>
              {label}
            </button>
          ))}
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
      {viewMode === "canvas" && <div className="editor-body">
        <aside className="palette">
          <div className="palette-head"><h3>节点</h3><span>拖入画布或点击添加</span></div>
          {(["route", "service"] as const).map((kind) => (
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
            <span>Limen 展示并编辑当前启动入口</span>
            <span>Limen → Route 指定入口</span>
            <span>Route → Service 设置转发</span>
            <small>中间件在 Route / Service 的编辑器中新建、改参、排序。</small>
            <small>入口地址、协议和 TLS 可在入口编辑器修改；确认后写入文件并重启才生效。Route / Service 节点可拖动、删除和双击编辑。</small>
          </div>
          <button type="button" className="btn ghost" onClick={() => openRegistryManager(false)}>注册中心管理</button>
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
            edgeTypes={edgeTypes}
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
      </div>}
      {viewMode === "rules" && (
        <section className="rules-mode" aria-label="路由规则列表">
          <div className="rules-mode-head">
            <div>
              <span className="overview-section-label">ROUTE RULES</span>
              <h2>路由规则</h2>
              <p>按优先级快速检查匹配、策略与转发目标；点击编辑进入完整规则抽屉。</p>
            </div>
            <div className="rules-filters">
              <label className="rules-sort">入口 Limen
                <select value={limenFilter} onChange={(event) => setLimenFilter(event.target.value)}>
                  <option value="all">全部入口（{limenOptions.length}）</option>
                  {limenOptions.map((name) => <option key={name} value={name}>{name}</option>)}
                  {limenOptions.length > 1 && <option value="__unbound">未绑定入口</option>}
                </select>
              </label>
              <label className="rules-sort">排序
              <select value={ruleSort} onChange={(event) => setRuleSort(event.target.value as RuleSort)}>
                <option value="priority">优先级</option>
                <option value="name">名称</option>
                <option value="order">配置顺序</option>
              </select>
              </label>
            </div>
          </div>
          <div className="rules-list">
            {visibleRoutes.length === 0 ? <Empty text="还没有符合筛选条件的路由规则。" /> : visibleRoutes.map(({ route, index, displayIndex }) => (
              <article className="rule-row" key={route.name}>
                <div className="rule-order">{String(displayIndex + 1).padStart(2, "0")}</div>
                <div className="rule-main">
                  <div className="rule-title"><strong>{route.name}</strong><span>priority {route.priority || 0}</span></div>
                  <div className={`rule-limen ${(route.limen || (limenOptions.length === 1 ? limenOptions[0] : "")) ? "" : "missing"}`}>
                    <span>入口</span>
                    <strong>{route.limen || (limenOptions.length === 1 ? limenOptions[0] : "未绑定入口")}</strong>
                    {route.limen && draft.limens?.[route.limen] && <small>{draft.limens[route.limen].address}</small>}
                  </div>
                  <code>{routeMatchLabel(route)}</code>
                  <div className="rule-meta"><span>{routeActionLabel(route)}</span><span>{(route.middlewares || []).length} middleware</span></div>
                </div>
                <div className="rule-actions">
                  <button type="button" className="btn small" onClick={() => openEditor(nodeId("route", route.name))}>编辑</button>
                  <button type="button" className="btn small" onClick={() => openRouteMiddleware(route.name)}>中间件</button>
                  {ruleSort === "order" && limenFilter === "all" && <>
                    <button type="button" className="btn small icon-text" disabled={index === 0} onClick={() => reorderRoute(route.name, -1)} aria-label="上移">↑</button>
                    <button type="button" className="btn small icon-text" disabled={index === routes.length - 1} onClick={() => reorderRoute(route.name, 1)} aria-label="下移">↓</button>
                  </>}
                </div>
              </article>
            ))}
          </div>
        </section>
      )}
      {viewMode === "json" && (
        <section className="json-mode" aria-label="JSON 配置编辑器">
          <div className="json-mode-head">
            <div>
              <span className="overview-section-label">RAW CONFIGURATION</span>
              <h2>JSON 编辑</h2>
              <p>直接编辑完整配置。应用前会检查 JSON 语法；保存和发布仍会执行完整配置校验。</p>
            </div>
            <div className="json-mode-actions">
              <button type="button" className="btn ghost" onClick={() => draft && setJsonText(JSON.stringify(draft, null, 2))}>还原草稿</button>
              <button type="button" className="btn primary" onClick={applyJson}>应用 JSON</button>
            </div>
          </div>
          <textarea className="json-editor config-json-editor" value={jsonText} onChange={(event) => { setJsonText(event.target.value); setJsonError(""); }} spellCheck={false} aria-label="JSON 配置内容" />
          {jsonError && <p className="json-mode-error" role="alert">{jsonError}</p>}
        </section>
      )}
      {editing?.kind === "route" && (
        <RouteEditor
          draft={draft}
          value={editing.value}
          isNew={editing.isNew}
          onChange={(value) => setEditing({ ...editing, value })}
          onConfirm={confirmEditing}
          onCancel={() => { if (editing.isNew) removeFlowNode(nodeId("route", editing.originalName)); setEditing(null); }}
          onClose={() => { if (editing.isNew) removeFlowNode(nodeId("route", editing.originalName)); setEditing(null); }}
          onDelete={editing.isNew ? undefined : deleteEditing}
          onOpenMiddlewareManager={() => openMiddlewareManager("route")}
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
          onCancel={() => { if (editing.isNew) removeFlowNode(nodeId("service", editing.originalName)); setEditing(null); }}
          onClose={() => { if (editing.isNew) removeFlowNode(nodeId("service", editing.originalName)); setEditing(null); }}
          onDelete={editing.isNew ? undefined : deleteEditing}
          onOpenMiddlewareManager={() => openMiddlewareManager("service")}
          onOpenRegistryManager={() => openRegistryManager(true)}
        />
      )}
      {editing?.kind === "limen" && (
        <LimenEditor
          name={editing.name}
          limen={editing.value}
          isNew={editing.isNew}
          onName={(name) => setEditing((old) => old && old.kind === "limen" ? { ...old, name } : old)}
          onChange={(value) => setEditing((old) => old && old.kind === "limen" ? { ...old, value } : old)}
          onConfirm={confirmEditing}
          onCancel={() => setEditing(null)}
          onClose={() => setEditing(null)}
          onDelete={editing.isNew ? deleteEditing : undefined}
          onStageLimens={stageSavedLimens}
        />
      )}
      {mwManager && editing && (editing.kind === "route" || editing.kind === "service") && (
        <MiddlewareManagerModal
          title={`${editing.kind === "route" ? "Route" : "Service"} ${editing.name} · 中间件管理`}
          scope={mwManager.scope}
          coreLabel={editing.kind === "route" ? routeCoreLabel(editing.value) : `Service ${editing.name} → 上游`}
          instances={draft.middlewares || {}}
          catalog={middlewareCatalog}
          flow={editing.value.middlewares || []}
          onFlowChange={(flow) => {
            if (editing.kind === "route") setEditing({ ...editing, value: { ...editing.value, middlewares: flow } });
            else setEditing({ ...editing, value: { ...editing.value, middlewares: flow } });
          }}
          onInstantiate={instantiateMiddleware}
          onUpdateInstance={updateMiddlewareInstance}
          onDeleteInstance={removeMiddlewareDef}
          onRenameInstance={renameMiddlewareInstance}
          onConfirm={() => {
            setMwManager(null);
            store.setMessage("中间件修改已保留；请继续确认当前节点。");
          }}
          onCancel={() => {
            restoreManager(mwManager.snapshot);
            setMwManager(null);
            store.setMessage("已撤销本次中间件修改。");
          }}
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
          onConfirm={() => {
            setRegManager(null);
            store.setMessage("Registry 修改已写入当前配置草稿。");
          }}
          onCancel={() => {
            restoreManager(regManager.snapshot);
            setRegManager(null);
            store.setMessage("已撤销本次 Registry 修改。");
          }}
          notify={store.setMessage}
        />
      )}
      {dialogs.dialog}
    </section>
  );
}
