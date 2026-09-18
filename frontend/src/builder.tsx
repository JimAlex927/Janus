import { useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent as ReactDragEvent, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from "react";
import { addConfigNode, buildGraph, canConnectNodes, CANVAS_HEIGHT, CANVAS_WIDTH, clampPosition, connectNodes, KIND_META, NODE_HEIGHT, NODE_WIDTH, removeNode, updateNode, type Config, type GraphEdge, type GraphNode, type JsonObject, type NodeKind, type Position } from "./graph-model";
import { InspectorModal } from "./inspector";
import { simulateRequest, type SimulationResult } from "./simulation";
import "./canvas-wheel.css";
import "./canvas-interaction.css";
import "./route-topology.css";

const POSITION_KEY = "janus-console-canvas-positions-v1";

export function BuilderPage({ draft, revision, dirty = false, jsonText, jsonError, onChange, onJSONChange, onValidate }: { draft: Config | null; revision?: number; dirty?: boolean; jsonText: string; jsonError: string; onChange: (next: Config) => void; onJSONChange: (text: string) => void; onValidate: () => void }) {
  const [mode, setMode] = useState<"visual" | "json">("visual");
  const [selectedId, setSelectedId] = useState("");
  const [positions, setPositions] = useState<Record<string, Position>>(() => readPositions());
  const [zoom, setZoom] = useState(0.68);
  const [canvasActive, setCanvasActive] = useState(false);
  const [pan, setPan] = useState<Position>({ x: 24, y: 20 });
  const [moving, setMoving] = useState<{ id: string; start: Position; origin: Position } | null>(null);
  const [panning, setPanning] = useState<{ start: Position; origin: Position } | null>(null);
  const [connectingFrom, setConnectingFrom] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [wirePosition, setWirePosition] = useState<Position | null>(null);
  const [notice, setNotice] = useState("");
  const [history, setHistory] = useState<Config[]>([]);
  const [future, setFuture] = useState<Config[]>([]);
  const lastHistoryAt = useRef(0);
  const canvasRef = useRef<HTMLDivElement>(null);

  const { nodes, edges } = useMemo(() => buildGraph(draft || {}), [draft]);
  const selected = nodes.find(node => node.id === selectedId) || nodes[0];
  const editing = nodes.find(node => node.id === editingId);
  const visiblePosition = (node: GraphNode): Position => positions[node.id] || { x: node.x, y: node.y };
  const connectionTargets = useMemo(() => {
    if (!draft || !connectingFrom) return new Set<string>();
    const source = nodes.find(node => node.id === connectingFrom);
    return new Set(nodes.filter(node => node.id !== connectingFrom && canConnectNodes(draft, source, node)).map(node => node.id));
  }, [draft, nodes, connectingFrom]);

  useEffect(() => {
    if (!selected || nodes.some(node => node.id === selectedId)) return;
    setSelectedId(selected.id);
  }, [nodes, selected, selectedId]);
  useEffect(() => {
    window.localStorage.setItem(POSITION_KEY, JSON.stringify(positions));
  }, [positions]);
  useEffect(() => {
    setHistory([]);
    setFuture([]);
    lastHistoryAt.current = 0;
  }, [revision]);
  useEffect(() => {
    if (!editingId) return;
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") setEditingId(null); };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [editingId]);
  useEffect(() => {
    const frame = window.requestAnimationFrame(fitView);
    return () => window.cancelAnimationFrame(frame);
  }, [nodes.length]);
  useEffect(() => {
    const viewport = canvasRef.current;
    if (!viewport || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => window.requestAnimationFrame(fitView));
    observer.observe(viewport);
    return () => observer.disconnect();
  }, [nodes.length]);
  useEffect(() => {
    const deactivateCanvas = (event: PointerEvent) => {
      const viewport = canvasRef.current;
      if (viewport && !viewport.contains(event.target as Node)) setCanvasActive(false);
    };
    document.addEventListener("pointerdown", deactivateCanvas);
    return () => document.removeEventListener("pointerdown", deactivateCanvas);
  }, []);

  function fitView() {
    const viewport = canvasRef.current;
    if (!viewport || nodes.length === 0) return;
    const bounds = nodes.reduce((result, node) => {
      const position = visiblePosition(node);
      return { minX: Math.min(result.minX, position.x), minY: Math.min(result.minY, position.y), maxX: Math.max(result.maxX, position.x + NODE_WIDTH), maxY: Math.max(result.maxY, position.y + NODE_HEIGHT) };
    }, { minX: Number.POSITIVE_INFINITY, minY: Number.POSITIVE_INFINITY, maxX: 0, maxY: 0 });
    const rect = viewport.getBoundingClientRect();
    const scale = Math.min(.88, Math.max(.35, Math.min((rect.width - 56) / (bounds.maxX - bounds.minX), (rect.height - 72) / (bounds.maxY - bounds.minY))));
    setZoom(scale);
    setPan({ x: Math.max(18, (rect.width - (bounds.maxX - bounds.minX) * scale) / 2 - bounds.minX * scale), y: Math.max(34, (rect.height - (bounds.maxY - bounds.minY) * scale) / 2 - bounds.minY * scale) });
  }

  function moveNode(event: ReactPointerEvent<HTMLDivElement>, node: GraphNode) {
    event.stopPropagation();
    const position = visiblePosition(node);
    event.currentTarget.setPointerCapture(event.pointerId);
    setSelectedId(node.id);
    setMoving({ id: node.id, start: { x: event.clientX, y: event.clientY }, origin: position });
  }
  function moveCanvas(event: ReactPointerEvent<HTMLDivElement>) {
    if (connectingFrom) setWirePosition(pointerWorld(event));
    if (moving) {
      const next = clampPosition({ x: moving.origin.x + (event.clientX - moving.start.x) / zoom, y: moving.origin.y + (event.clientY - moving.start.y) / zoom });
      setPositions(old => ({ ...old, [moving.id]: next }));
    } else if (panning) {
      setPan({ x: panning.origin.x + event.clientX - panning.start.x, y: panning.origin.y + event.clientY - panning.start.y });
    }
  }
  function endCanvas() { setMoving(null); setPanning(null); }
  function startPan(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.target !== event.currentTarget) return;
    setPanning({ start: { x: event.clientX, y: event.clientY }, origin: pan });
  }
  function zoomCanvas(event: ReactWheelEvent<HTMLDivElement>) {
    if (!canvasActive) return;
    event.preventDefault();
    event.stopPropagation();
    setZoom(value => Math.min(1.35, Math.max(0.55, value + (event.deltaY < 0 ? 0.06 : -0.06))));
  }
  function activateCanvas() {
    setCanvasActive(true);
    canvasRef.current?.focus({ preventScroll: true });
  }
  function addNode(kind: NodeKind, position?: Position) {
    if (!draft || !["limen", "route"].includes(kind)) return;
    const next = addConfigNode(draft, kind);
    commitConfig(next.config);
    setPositions(old => ({ ...old, [next.id]: clampPosition(position || { x: 120 + nodes.length * 24, y: 120 + nodes.length * 18 }) }));
    setSelectedId(next.id);
  }
  function updateMiddleware(name: string, definition: JsonObject) {
    if (!draft) return;
    commitConfig({ ...draft, middlewares: { ...(draft.middlewares || {}), [name]: definition } });
  }
  function formatGraph() {
    const next = Object.fromEntries(nodes.map(node => [node.id, { x: node.x, y: node.y }])) as Record<string, Position>;
    setPositions(next);
    window.requestAnimationFrame(() => fitView());
    setNotice("已按流程关系重新排版");
  }
  function dropNode(event: ReactDragEvent<HTMLDivElement>) {
    event.preventDefault();
    const kind = event.dataTransfer.getData("application/x-janus-node") as NodeKind;
    if (!kind || kind === "action") return;
    const rect = event.currentTarget.getBoundingClientRect();
    addNode(kind, clampPosition({ x: (event.clientX - rect.left - pan.x) / zoom, y: (event.clientY - rect.top - pan.y) / zoom }));
  }
  function pointerWorld(event: ReactPointerEvent<HTMLDivElement>): Position {
    const rect = event.currentTarget.getBoundingClientRect();
    return { x: (event.clientX - rect.left - pan.x) / zoom, y: (event.clientY - rect.top - pan.y) / zoom };
  }
  function handlePort(nodeId: string, direction: "in" | "out") {
    if (direction === "out") {
      setConnectingFrom(nodeId);
      setSelectedId(nodeId);
      setWirePosition(null);
      setNotice("已选择起点，请点击虚线高亮节点或它左侧的输入端口完成连接");
      return;
    }
    if (!draft || !connectingFrom) return;
    const from = nodes.find(node => node.id === connectingFrom);
    const to = nodes.find(node => node.id === nodeId);
    const result = connectNodes(draft, from, to);
    if (result.error) {
      setNotice(result.error);
      return;
    }
    commitConfig(result.config);
    setConnectingFrom(null);
    setWirePosition(null);
    setNotice(`已连接 ${from?.name || "节点"} → ${to?.name || "节点"}`);
  }
  function openEditor(nodeId: string) { setSelectedId(nodeId); setEditingId(nodeId); }
  function updateSelected(patch: JsonObject) {
    if (!draft || !selected || selected.kind === "action" || selected.kind === "limen") return;
    commitConfig(updateNode(draft, selected, patch));
  }
  function removeSelected(): boolean {
    if (!draft || !selected || selected.kind === "action" || selected.kind === "limen") return false;
    commitConfig(removeNode(draft, selected));
    setSelectedId("");
    return true;
  }
  function commitConfig(next: Config) {
    if (!draft || JSON.stringify(next) === JSON.stringify(draft)) return;
    const now = Date.now();
    const groupWithPrevious = now - lastHistoryAt.current < 650;
    setHistory(old => groupWithPrevious && old.length > 0 ? old : [...old, draft].slice(-30));
    lastHistoryAt.current = now;
    setFuture([]);
    onChange(next);
  }
  function undo() {
    if (!draft || history.length === 0) return;
    const previous = history[history.length - 1];
    setHistory(old => old.slice(0, -1));
    setFuture(old => [draft, ...old].slice(0, 30));
    lastHistoryAt.current = 0;
    onChange(previous);
    setNotice("已撤销上一步画布操作");
  }
  function redo() {
    if (!draft || future.length === 0) return;
    const next = future[0];
    setFuture(old => old.slice(1));
    setHistory(old => [...old, draft].slice(-30));
    lastHistoryAt.current = 0;
    onChange(next);
    setNotice("已重做画布操作");
  }
  function changeJSON(text: string) {
    setHistory([]);
    setFuture([]);
    lastHistoryAt.current = 0;
    onJSONChange(text);
  }

  if (!draft) return <section className="builder-empty">正在加载配置…</section>;
  return <section className="builder-shell">
    <div className="builder-toolbar">
      <div><span className="eyebrow">CONFIGURATION WORKSPACE</span><p>只展示入口到路由的请求拓扑；Service 和 Middleware 在资源页面中管理。</p></div>
      <div className="builder-actions"><div className="history-actions"><button className="icon-action" title="撤销" disabled={history.length === 0} onClick={undo}>↶</button><button className="icon-action" title="重做" disabled={future.length === 0} onClick={redo}>↷</button></div><div className="mode-switch"><button className={mode === "visual" ? "active" : ""} onClick={() => setMode("visual")}>画布</button><button className={mode === "json" ? "active" : ""} onClick={() => setMode("json")}>JSON</button></div><span className={`builder-draft-state ${dirty ? "dirty" : ""}`}>{dirty ? "草稿未发布" : "已同步"}</span><button className="ghost" onClick={onValidate}>校验</button></div>
    </div>
    {mode === "json" ? <div className="builder-json"><div className="builder-json-head"><div><h3>原始配置</h3><p>JSON 与路由列表共享同一份草稿，适合批量调整高级字段。</p></div>{jsonError && <span className="builder-error">{jsonError}</span>}</div><textarea className="json-editor" value={jsonText} onChange={event => changeJSON(event.target.value)} spellCheck={false} /></div> : <RouteTopology draft={draft} onChange={onChange} />}
  </section>;
}

function RouteTopology({ draft, onChange }: { draft: Config; onChange: (next: Config) => void }) {
  const limens = Object.entries(draft.limens || {});
  const [selectedLimen, setSelectedLimen] = useState("all");
  const [editingId, setEditingId] = useState<string | null>(null);
  const routeNodes = buildGraph(draft).nodes.filter(node => node.kind === "route");
  const editing = routeNodes.find(node => node.id === editingId);
  const selectedName = selectedLimen === "all" ? "全部 Limen" : selectedLimen;
  const routes = (draft.routes || []).filter(route => selectedLimen === "all" || route.limen === selectedLimen || (!route.limen && limens.length === 1));

  function addRoute() {
    const result = addConfigNode(draft, "route");
    const existingRoutes = result.config.routes || [];
    const route = existingRoutes[existingRoutes.length - 1];
    if (!route) return;
    const nextRoute = selectedLimen !== "all" ? { ...route, limen: selectedLimen } : route;
    onChange({ ...result.config, routes: [...existingRoutes.slice(0, -1), nextRoute] });
    setEditingId(result.id);
  }
  function updateSelected(patch: JsonObject) {
    if (editing) onChange(updateNode(draft, editing, patch));
  }
  function updateMiddleware(name: string, definition: JsonObject) {
    onChange({ ...draft, middlewares: { ...(draft.middlewares || {}), [name]: definition } });
  }
  function removeSelected() {
    if (!editing) return;
    onChange(removeNode(draft, editing));
    setEditingId(null);
  }
  return (
    <div className="route-topology">
      <aside className="limen-directory">
        <div className="directory-head"><div><strong>Limen</strong><small>选择入口查看 Route</small></div><span>{limens.length}</span></div>
        <button className={`limen-item ${selectedLimen === "all" ? "active" : ""}`} onClick={() => setSelectedLimen("all")}><span className="list-icon registry-icon">∑</span><span><strong>全部入口</strong><small>显示所有 Route</small></span></button>
        {limens.map(([name, limen]) => <button className={`limen-item ${selectedLimen === name ? "active" : ""}`} key={name} onClick={() => setSelectedLimen(name)}><span className="list-icon limen-directory-icon">◉</span><span><strong>{name}</strong><small>{limen.address || "未配置地址"}</small><small>{(limen.protocols || []).join(" · ")}</small></span></button>)}
        {limens.length === 0 && <div className="empty">暂无 Limen</div>}
      </aside>
      <section className="route-directory">
        <div className="route-directory-head"><div><span className="eyebrow">ROUTE RULES</span><h3>{selectedName}</h3><p>按入口查看请求规则；Service 和 Middleware 在 Route 编辑器中配置。</p></div><button className="primary" onClick={addRoute}>＋ 新建 Route</button></div>
        <div className="route-cards">
          {routes.map(route => {
            const node = routeNodes.find(item => item.name === route.name);
            if (!node) return null;
            const action = route.action?.forward ? `→ ${route.action.forward.service || "未选择 Service"}` : route.action?.redirect ? `↗ ${route.action.redirect.location || "Redirect"}` : "响应";
            return <button className="route-rule-card" key={route.name} onClick={() => setEditingId(node.id)}><span className="route-rule-icon">↗</span><span className="route-rule-main"><strong>{route.name}</strong><small>{route.match || `${route.host || "*"} · ${route.path_prefix || "/"}`}</small><small>{action}</small>{(route.middlewares || []).length > 0 && <span className="route-rule-badges">{route.middlewares.map(name => <em key={name}>{name}</em>)}</span>}</span><span className="chevron">›</span></button>;
          })}
          {routes.length === 0 && <div className="empty route-empty">当前 Limen 暂无 Route，点击右上角创建。</div>}
        </div>
        <SimulationPanel draft={draft} onSelectRoute={name => { const node = routeNodes.find(item => item.name === name); if (node) setEditingId(node.id); }} />
      </section>
      {editing && <InspectorModal node={editing} draft={draft} onUpdate={updateSelected} onUpdateMiddleware={updateMiddleware} onRemove={removeSelected} onClose={() => setEditingId(null)} />}
    </div>
  );
}

function SimulationPanel({ draft, onSelectRoute }: { draft: Config; onSelectRoute: (name: string) => void }) {
  const limens = Object.keys(draft.limens || {});
  const [open, setOpen] = useState(false);
  const [method, setMethod] = useState("GET");
  const [target, setTarget] = useState("/api");
  const [host, setHost] = useState("");
  const [headersText, setHeadersText] = useState("");
  const [protocol, setProtocol] = useState("http");
  const [limen, setLimen] = useState(limens[0] || "");
  const [result, setResult] = useState<SimulationResult | null>(null);
  const limenKey = limens.join("\0");
  useEffect(() => {
    if (limen && !limens.includes(limen)) setLimen(limens[0] || "");
  }, [limenKey, limen]);
  function run() {
    const headers = Object.fromEntries(headersText.split(/\r?\n/).map(line => line.match(/^\s*([^:]+):\s*(.*)$/)).filter((match): match is RegExpMatchArray => Boolean(match)).map(match => [match[1], match[2]]));
    const next = simulateRequest(draft, { method, target, host, headers, protocol, limen });
    setResult(next);
    if (next.matchedRoute) onSelectRoute(next.matchedRoute.name);
  }
  return <div className={`simulation-panel ${open ? "open" : ""}`}><button type="button" className="simulation-toggle" onClick={() => setOpen(value => !value)} aria-expanded={open}>⌁ 模拟请求</button>{open && <div className="simulation-card"><div className="simulation-head"><div><strong>Request simulation</strong><small>只检查当前草稿，不发送真实请求</small></div><button type="button" aria-label="关闭模拟请求" onClick={() => setOpen(false)}>×</button></div><div className="simulation-grid"><label>Method<select value={method} onChange={event => setMethod(event.target.value)}><option>GET</option><option>POST</option><option>PUT</option><option>PATCH</option><option>DELETE</option><option>OPTIONS</option></select></label><label>Protocol<select value={protocol} onChange={event => setProtocol(event.target.value)}><option value="http">HTTP</option><option value="sse">SSE</option><option value="websocket">WebSocket</option></select></label></div><label className="simulation-field">URL or path<input value={target} onChange={event => setTarget(event.target.value)} placeholder="https://api.example.com/api/users" /></label><label className="simulation-field">Host（相对路径时使用）<input value={host} onChange={event => setHost(event.target.value)} placeholder="api.example.com" /></label><label className="simulation-field">Headers（每行 Name: value）<textarea value={headersText} onChange={event => setHeadersText(event.target.value)} placeholder="X-Tenant: demo" rows={2} /></label><label className="simulation-field">Limen<select value={limen} onChange={event => setLimen(event.target.value)}><option value="">全部入口</option>{limens.map(name => <option value={name} key={name}>{name}</option>)}</select></label><button type="button" className="primary simulation-run" disabled={!target.trim()} onClick={run}>运行模拟</button>{result && <SimulationResultView result={result} />}</div>}</div>;
}

function SimulationResultView({ result }: { result: SimulationResult }) {
  return <div className="simulation-result">{result.error ? <div className="simulation-error">{result.error}</div> : result.matchedRoute ? <div className="simulation-hit"><span>命中 Route</span><strong>{result.matchedRoute.name}</strong><small>{result.matchedRoute.action}{result.matchedRoute.service ? ` → ${result.matchedRoute.service}` : ""}</small>{result.matchedRoute.middlewares.length > 0 && <div className="simulation-chips">{result.matchedRoute.middlewares.map(name => <span key={name}>{name}</span>)}</div>}</div> : <div className="simulation-miss"><strong>没有匹配的 Route</strong><small>当前请求不会进入任何后端 Service。</small></div>} {result.candidates.length > 0 && <details><summary>查看 {result.candidates.length} 条候选规则</summary><div className="simulation-candidates">{result.candidates.map(candidate => <div className={candidate.matched ? "matched" : ""} key={candidate.routeName}><span>{candidate.matched ? "✓" : "—"}</span><strong>{candidate.routeName}</strong><small>{candidate.reason}</small></div>)}</div></details>}</div>;
}

function NodePalette({ onAdd }: { onAdd: (kind: NodeKind) => void }) {
  return <aside className="node-palette"><div className="palette-head"><h3>节点</h3><span>点击添加或拖入画布</span></div>{(["limen", "route"] as NodeKind[]).map(kind => <button key={kind} className="palette-item" draggable onDragStart={event => event.dataTransfer.setData("application/x-janus-node", kind)} onClick={() => onAdd(kind)}><span className="palette-icon" style={{ background: KIND_META[kind].color }}>{KIND_META[kind].icon}</span><span><strong>{KIND_META[kind].label}</strong><small>{KIND_META[kind].description}</small></span><b>＋</b></button>)}<div className="palette-note"><strong>连接关系</strong><span>Limen → Route</span><small>Service 和 Middleware 在各自资源页面创建，再由 Route 编辑器引用。</small></div></aside>;
}

function GraphNodeCard({ node, position, selected, connectionMode, connectable, onPointerDown, onPointerMove, onPointerUp, onSelect, onEdit, onPort }: { node: GraphNode; position: Position; selected: boolean; connectionMode: boolean; connectable: boolean; onPointerDown: (event: ReactPointerEvent<HTMLDivElement>, node: GraphNode) => void; onPointerMove: (event: ReactPointerEvent<HTMLDivElement>) => void; onPointerUp: () => void; onSelect: (id: string) => void; onEdit: (id: string) => void; onPort: (nodeId: string, direction: "in" | "out") => void }) {
  const style: CSSProperties = { left: position.x, top: position.y, borderColor: selected ? KIND_META[node.kind].color : undefined };
  return <div className={`graph-node ${selected ? "selected" : ""} ${connectable ? "connectable" : ""} ${connectionMode && !connectable ? "not-connectable" : ""}`} style={style} onPointerDown={event => onPointerDown(event, node)} onPointerMove={event => { event.stopPropagation(); onPointerMove(event); }} onPointerUp={event => { event.stopPropagation(); onPointerUp(); }} onPointerCancel={event => { event.stopPropagation(); onPointerUp(); }} onClick={() => connectionMode ? onPort(node.id, "in") : onSelect(node.id)}><div className="graph-node-head"><span className="graph-node-icon" style={{ background: KIND_META[node.kind].color }}>{KIND_META[node.kind].icon}</span><div><small>{KIND_META[node.kind].label}</small><strong>{node.name}</strong></div><button type="button" className="node-kebab" aria-label={`编辑 ${node.name}`} title="编辑节点" onPointerDown={event => event.stopPropagation()} onClick={event => { event.stopPropagation(); onEdit(node.id); }}>•••</button></div><p>{node.subtitle}</p>{node.badges.length > 0 && <div className="node-badges">{node.badges.slice(0, 3).map(badge => <span key={badge}>{badge}</span>)}</div>}<button type="button" className="port port-in" aria-label={`连接到 ${node.name}`} onPointerDown={event => event.stopPropagation()} onPointerUp={event => { event.stopPropagation(); onPort(node.id, "in"); }} onClick={event => { event.stopPropagation(); onPort(node.id, "in"); }} /><button type="button" className="port port-out" aria-label={`从 ${node.name} 连出`} onPointerDown={event => { event.stopPropagation(); onPort(node.id, "out"); }} onClick={event => event.stopPropagation()} /></div>;
}

function GraphEdge({ from, to, preview = false }: { from: Position; to: Position; preview?: boolean }) {
  const startX = from.x + NODE_WIDTH;
  const startY = from.y + NODE_HEIGHT / 2;
  const endX = to.x;
  const endY = to.y + NODE_HEIGHT / 2;
  const bend = Math.max(50, Math.abs(endX - startX) * .45);
  return <path className={`graph-edge ${preview ? "preview" : ""}`} d={`M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`} />;
}

function readPositions(): Record<string, Position> { try { return JSON.parse(window.localStorage.getItem(POSITION_KEY) || "{}"); } catch { return {}; } }
