import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";
import "./login.css";
import "./chart.css";
import "./builder.css";
import "./connections.css";
import "./inspector.css";
import "./state.css";
import "./modal.css";
import "./simulation.css";
import "./resource.css";
import { BuilderPage } from "./builder";
import { addConfigNode, KIND_META, removeNode, renameNode, updateNode, type GraphNode, type NodeKind } from "./graph-model";
import { InspectorModal } from "./inspector";
import { RegistryPage } from "./registry";

type JsonObject = Record<string, any>;
type Config = JsonObject & { routes?: JsonObject[]; services?: Record<string, JsonObject>; middlewares?: Record<string, JsonObject> };
type Snapshot = { config: Config; revision: number };
type Summary = { requests: number; errors: number; in_flight: number };
type APIError = Error & { status?: number };

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { ...init, headers: { "Content-Type": "application/json", ...(init?.headers || {}) } });
  if (!response.ok) { const error = new Error((await response.text()) || `${response.status}`) as APIError; error.status = response.status; throw error; }
  return response.json();
}

function App() {
  const [page, setPage] = useState("overview");
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [draft, setDraft] = useState<Config | null>(null);
  const [jsonText, setJsonText] = useState("");
  const [jsonError, setJsonError] = useState("");
  const [message, setMessage] = useState("");
  const [summary, setSummary] = useState<Summary>({ requests: 0, errors: 0, in_flight: 0 });
  const [points, setPoints] = useState<{ time: string; value: number }[]>([]);
  const [loggedIn, setLoggedIn] = useState(true);
  const [dirty, setDirty] = useState(false);
  const dirtyRef = useRef(false);

  const load = async (force = false) => {
    if (dirtyRef.current && !force) { setMessage("运行时配置已变化，当前草稿未刷新；请先发布或手动刷新"); return; }
    const value = await api<Snapshot>("/api/v1/config");
    dirtyRef.current = false; setSnapshot(value); setDraft(value.config); setJsonText(JSON.stringify(value.config, null, 2)); setJsonError(""); setDirty(false);
  };
  useEffect(() => {
    if (!loggedIn) return;
    load().catch((error: APIError) => { if (error.status === 401) setLoggedIn(false); else setMessage(error.message); });
    const source = new EventSource("/api/v1/events"); source.addEventListener("generation_changed", () => load().catch(() => undefined)); source.onerror = () => undefined;
    return () => source.close();
  }, [loggedIn]);
  useEffect(() => {
    if (!loggedIn) return;
    const sample = async () => { try { const value = await api<Summary>("/api/v1/metrics"); setSummary(value); setPoints(old => [...old.slice(-59), { time: new Date().toLocaleTimeString(), value: value.requests }]); } catch (error) { if ((error as APIError).status === 401) setLoggedIn(false); } };
    sample(); const timer = window.setInterval(sample, 5000); return () => window.clearInterval(timer);
  }, [loggedIn]);

  const updateDraft = (next: Config) => { dirtyRef.current = true; setDraft(next); setJsonText(JSON.stringify(next, null, 2)); setJsonError(""); setDirty(true); };
  const validate = async () => { if (!draft || jsonError) { setMessage(jsonError || "没有可校验的配置"); return; } try { await api("/api/v1/config/validate", { method: "POST", body: JSON.stringify(draft) }); setMessage("配置校验通过"); } catch (error) { setMessage(String(error)); } };
  const publish = async () => { if (!draft || !snapshot || jsonError) { setMessage(jsonError || "请先修复配置"); return; } if (!dirty) { setMessage("当前没有未发布变更"); return; } try { const value = await api<{ revision: number }>("/api/v1/config/publish", { method: "POST", headers: { "X-Janus-Revision": String(snapshot.revision) }, body: JSON.stringify(draft) }); setMessage(`已发布配置版本 ${value.revision}`); await load(true); } catch (error) { setMessage(String(error)); } };
  const logout = async () => { await api("/api/v1/auth/logout", { method: "POST" }).catch(() => undefined); setLoggedIn(false); };
  const onJSONChange = (text: string) => { dirtyRef.current = true; setJsonText(text); setDirty(true); try { setDraft(JSON.parse(text) as Config); setJsonError(""); } catch { setJsonError("JSON 尚未完成，当前文本已保留，修复后才能校验或发布"); } };
  const refresh = () => { if (dirty && !window.confirm("当前有未发布变更，刷新会丢弃草稿。继续吗？")) return; load(true).catch((error: APIError) => setMessage(error.message)); };
  const title = ({ overview: "系统概览", builder: "路由配置", registries: "Nacos Registry", services: "Services", middlewares: "Middleware" } as Record<string, string>)[page];
  if (!loggedIn) return <Login onLogin={() => setLoggedIn(true)} />;

  return <div className="app-shell"><aside><div className="brand"><div className="brand-mark">J</div><div><strong>Janus</strong><small>Gateway Console</small></div></div><nav>{[["overview", "概览"], ["builder", "路由配置"], ["registries", "Nacos Registry"], ["services", "Services"], ["middlewares", "Middleware"]].map(([id, label]) => <button className={page === id ? "active" : ""} onClick={() => setPage(id)} key={id}><span className="nav-dot" />{label}</button>)}</nav><div className="side-foot"><span className="status-dot" />运行中<br /><small>Revision {snapshot?.revision ?? "—"}</small></div></aside><main><header><div><span className="eyebrow">CONTROL PLANE</span><div className="title-line"><h1>{title}</h1><span className={`draft-state ${dirty ? "dirty" : ""}`}>{dirty ? "未发布变更" : "已同步"}</span></div></div><div className="header-actions"><button className="ghost" onClick={refresh}>刷新</button><button className="ghost" onClick={logout}>退出</button><button className="primary" disabled={!dirty || Boolean(jsonError)} onClick={publish}>发布变更</button></div></header>{message && <div className="toast">{message}<button onClick={() => setMessage("")}>×</button></div>}{page === "overview" && <Overview points={points} snapshot={snapshot} summary={summary} />}{page === "builder" && <BuilderPage draft={draft} revision={snapshot?.revision} dirty={dirty} jsonText={jsonText} jsonError={jsonError} onChange={updateDraft} onJSONChange={onJSONChange} onValidate={validate} />}{page === "registries" && <RegistryPage draft={draft} onChange={updateDraft} />}{page === "services" && <ResourcePage kind="service" draft={draft} onChange={updateDraft} />}{page === "middlewares" && <ResourcePage kind="middleware" draft={draft} onChange={updateDraft} />}</main></div>;
}

function Overview({ points, snapshot, summary }: { points: { time: string; value: number }[]; snapshot: Snapshot | null; summary: Summary }) {
  return <section className="content"><div className="hero"><div><span className="eyebrow">JANUS RUNTIME</span><h2>清晰地看见每一次请求。</h2><p>路由、上游和发布状态都在一个安静的工作台里。</p></div><div className="hero-orb"><span>{Object.keys(snapshot?.config.services || {}).length}</span><small>Services</small></div></div><div className="stat-grid"><Stat label="运行状态" value="Healthy" accent="green" /><Stat label="已完成请求" value={String(summary.requests)} /><Stat label="在途请求" value={String(summary.in_flight)} /><Stat label="Revision" value={String(snapshot?.revision || 0)} /></div><div className="card chart-card"><div className="section-head"><div><h3>请求活动</h3><p>最近 5 分钟 · 浏览器内存窗口</p></div><span className="live-label"><span className="status-dot" />LIVE</span></div><ActivityChart points={points} /></div></section>;
}
function ActivityChart({ points }: { points: { time: string; value: number }[] }) {
  const width = 1000;
  const height = 240;
  const padding = 14;
  const values = points.length > 0 ? points.map(point => point.value) : [0];
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const coordinates = values.map((value, index) => {
    const x = values.length === 1 ? width / 2 : (index / (values.length - 1)) * width;
    const y = height - padding - ((value - min) / range) * (height - padding * 2);
    return `${x},${y}`;
  });
  const area = `0,${height} ${coordinates.join(" ")} ${width},${height}`;
  return <div className="chart"><svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="请求数量趋势" preserveAspectRatio="none"><defs><linearGradient id="request-area" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stopColor="#536dfe" stopOpacity=".24" /><stop offset="1" stopColor="#536dfe" stopOpacity="0" /></linearGradient></defs><polygon points={area} fill="url(#request-area)" /><polyline points={coordinates.join(" ")} fill="none" stroke="#536dfe" strokeWidth="4" strokeLinecap="round" strokeLinejoin="round" vectorEffect="non-scaling-stroke" /></svg>{points.length === 0 && <span className="chart-empty">等待请求数据…</span>}</div>;
}
function Stat({ label, value, accent }: { label: string; value: string; accent?: string }) { return <div className="stat-card"><span>{label}</span><strong className={accent || ""}>{value}</strong><small>当前快照</small></div>; }
function ResourcePage({ kind, draft, onChange }: { kind: "service" | "middleware"; draft: Config | null; onChange: (next: Config) => void }) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [creatingId, setCreatingId] = useState<string | null>(null);
  const [beforeCreate, setBeforeCreate] = useState<Config | null>(null);
  if (!draft) return <section className="content"><div className="empty">正在加载配置…</div></section>;
  const config = draft;
  const entries = kind === "service" ? Object.entries(config.services || {}) : Object.entries(config.middlewares || {});
  const nodes = entries.map(([name, value], index) => {
    const policy = ["buffer", "body_limit", "in_flight"].find(type => value[type] != null) || "尚未配置";
    const scope = value.scope === "route" ? "Route" : value.scope === "service" ? "Service" : "Route + Service";
    const serviceSubtitle = value.nacos ? `Nacos · ${value.nacos.service_name || "未配置服务"}` : `${value.upstreams?.length || 0} upstream`;
    return { id: `${kind}:${name}`, kind, name, subtitle: kind === "service" ? serviceSubtitle : `${scope} · ${policy}`, badges: kind === "service" ? value.middlewares || [] : [], x: 0, y: index * 100 } as GraphNode;
  });
  const editing = nodes.find(node => node.id === editingId);
  const description = kind === "service" ? "先在这里创建和维护上游 Service，Route 只负责引用。" : "先在这里创建可复用策略，Route 再编排已有 Middleware。";
  function create() {
    const result = addConfigNode(config, kind as NodeKind);
    setBeforeCreate(config);
    setCreatingId(result.id);
    onChange(result.config);
    setEditingId(result.id);
  }
  function update(patch: JsonObject) {
    if (!editing) return;
    onChange(updateNode(config, editing, patch));
  }
  function rename(nextName: string) {
    if (!editing) return "找不到正在编辑的 Middleware";
    const result = renameNode(config, editing, nextName);
    if (result.error) return result.error;
    onChange(result.config);
    setEditingId(`${kind}:${nextName.trim()}`);
    return undefined;
  }
  function remove() {
    if (!editing) return;
    onChange(removeNode(config, editing));
    setCreatingId(null);
    setBeforeCreate(null);
    setEditingId(null);
  }
  function confirmCreate() {
    setCreatingId(null);
    setBeforeCreate(null);
    setEditingId(null);
  }
  function cancelCreate() {
    if (creatingId && beforeCreate) onChange(beforeCreate);
    setCreatingId(null);
    setBeforeCreate(null);
    setEditingId(null);
  }
  function closeEditor() {
    if (creatingId) cancelCreate();
    else setEditingId(null);
  }
  return <section className="content resource-page"><div className="section-head"><div><p>{description}</p></div><button className="primary" onClick={create}>＋ 新建 {kind === "service" ? "Service" : "Middleware"}</button></div><div className="list">{nodes.map(node => <button className="list-row resource-row" key={node.id} onClick={() => setEditingId(node.id)}><div className="list-icon" style={{ background: KIND_META[kind].color, color: "#fff" }}>{KIND_META[kind].icon}</div><div><strong>{node.name}</strong><small>{node.subtitle || "尚未配置"}</small></div><span className="chevron">›</span></button>)}{nodes.length === 0 && <div className="empty">暂无配置，点击右上角创建。</div>}</div>{editing && <InspectorModal node={editing} draft={draft} onUpdate={update} onRemove={remove} onClose={closeEditor} onRename={kind === "middleware" ? rename : undefined} onConfirm={creatingId === editingId ? confirmCreate : undefined} onCancel={creatingId === editingId ? cancelCreate : undefined} />}</section>;
}
function Login({ onLogin }: { onLogin: () => void }) { const [username, setUsername] = useState(""); const [password, setPassword] = useState(""); const [error, setError] = useState(""); const submit = async (event: FormEvent) => { event.preventDefault(); try { await api("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ username, password }) }); onLogin(); } catch { setError("账号或密码错误"); } }; return <div className="login-shell"><form className="login-card" onSubmit={submit}><div className="brand-mark">J</div><span className="eyebrow">JANUS CONSOLE</span><h2>欢迎回来</h2><p>登录后管理你的网关配置。</p><input autoFocus placeholder="管理员账号" value={username} onChange={event => setUsername(event.target.value)} /><input type="password" placeholder="密码" value={password} onChange={event => setPassword(event.target.value)} /><button className="primary" type="submit">登录</button>{error && <small className="login-error">{error}</small>}</form></div>; }

createRoot(document.getElementById("root")!).render(<App />);
