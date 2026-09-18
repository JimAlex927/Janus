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
import { BuilderPage } from "./builder";

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

  const services = draft?.services || {}; const middlewares = draft?.middlewares || {};
  const updateDraft = (next: Config) => { dirtyRef.current = true; setDraft(next); setJsonText(JSON.stringify(next, null, 2)); setJsonError(""); setDirty(true); };
  const validate = async () => { if (!draft || jsonError) { setMessage(jsonError || "没有可校验的配置"); return; } try { await api("/api/v1/config/validate", { method: "POST", body: JSON.stringify(draft) }); setMessage("配置校验通过"); } catch (error) { setMessage(String(error)); } };
  const publish = async () => { if (!draft || !snapshot || jsonError) { setMessage(jsonError || "请先修复配置"); return; } if (!dirty) { setMessage("当前没有未发布变更"); return; } try { const value = await api<{ revision: number }>("/api/v1/config/publish", { method: "POST", headers: { "X-Janus-Revision": String(snapshot.revision) }, body: JSON.stringify(draft) }); setMessage(`已发布配置版本 ${value.revision}`); await load(true); } catch (error) { setMessage(String(error)); } };
  const logout = async () => { await api("/api/v1/auth/logout", { method: "POST" }).catch(() => undefined); setLoggedIn(false); };
  const onJSONChange = (text: string) => { dirtyRef.current = true; setJsonText(text); setDirty(true); try { setDraft(JSON.parse(text) as Config); setJsonError(""); } catch { setJsonError("JSON 尚未完成，当前文本已保留，修复后才能校验或发布"); } };
  const refresh = () => { if (dirty && !window.confirm("当前有未发布变更，刷新会丢弃草稿。继续吗？")) return; load(true).catch((error: APIError) => setMessage(error.message)); };
  const title = ({ overview: "系统概览", builder: "配置画布", services: "Services", middlewares: "Middleware" } as Record<string, string>)[page];
  if (!loggedIn) return <Login onLogin={() => setLoggedIn(true)} />;

  return <div className="app-shell"><aside><div className="brand"><div className="brand-mark">J</div><div><strong>Janus</strong><small>Gateway Console</small></div></div><nav>{[["overview", "概览"], ["builder", "配置画布"], ["services", "Services"], ["middlewares", "Middleware"]].map(([id, label]) => <button className={page === id ? "active" : ""} onClick={() => setPage(id)} key={id}><span className="nav-dot" />{label}</button>)}</nav><div className="side-foot"><span className="status-dot" />运行中<br /><small>Revision {snapshot?.revision ?? "—"}</small></div></aside><main><header><div><span className="eyebrow">CONTROL PLANE</span><div className="title-line"><h1>{title}</h1><span className={`draft-state ${dirty ? "dirty" : ""}`}>{dirty ? "未发布变更" : "已同步"}</span></div></div><div className="header-actions"><button className="ghost" onClick={refresh}>刷新</button><button className="ghost" onClick={logout}>退出</button><button className="primary" disabled={!dirty || Boolean(jsonError)} onClick={publish}>发布变更</button></div></header>{message && <div className="toast">{message}<button onClick={() => setMessage("")}>×</button></div>}{page === "overview" && <Overview points={points} snapshot={snapshot} summary={summary} />}{page === "builder" && <BuilderPage draft={draft} revision={snapshot?.revision} dirty={dirty} jsonText={jsonText} jsonError={jsonError} onChange={updateDraft} onJSONChange={onJSONChange} onValidate={validate} />}{page === "services" && <ListPage title="Services" description="上游池、健康检查和 Service Middleware。" items={Object.entries(services).map(([name, value]) => ({ name, detail: `${value.upstreams?.length || 0} upstream · ${(value.middlewares || []).length} middleware` }))} />}{page === "middlewares" && <ListPage title="Middleware" description="当前支持的可复用策略组件。" items={Object.entries(middlewares).map(([name, value]) => ({ name, detail: Object.keys(value).join(" · ") }))} />}</main></div>;
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
function ListPage({ title, description, items }: { title: string; description: string; items: { name: string; detail: string }[] }) { return <section className="content"><div className="section-head"><div><h2>{title}</h2><p>{description}</p></div></div><div className="list">{items.map(item => <div className="list-row" key={item.name}><div className="list-icon">{item.name.slice(0, 1).toUpperCase()}</div><div><strong>{item.name}</strong><small>{item.detail}</small></div><span className="chevron">›</span></div>)}{items.length === 0 && <div className="empty">暂无配置</div>}</div></section>; }
function Login({ onLogin }: { onLogin: () => void }) { const [username, setUsername] = useState(""); const [password, setPassword] = useState(""); const [error, setError] = useState(""); const submit = async (event: FormEvent) => { event.preventDefault(); try { await api("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ username, password }) }); onLogin(); } catch { setError("账号或密码错误"); } }; return <div className="login-shell"><form className="login-card" onSubmit={submit}><div className="brand-mark">J</div><span className="eyebrow">JANUS CONSOLE</span><h2>欢迎回来</h2><p>登录后管理你的网关配置。</p><input autoFocus placeholder="管理员账号" value={username} onChange={event => setUsername(event.target.value)} /><input type="password" placeholder="密码" value={password} onChange={event => setPassword(event.target.value)} /><button className="primary" type="submit">登录</button>{error && <small className="login-error">{error}</small>}</form></div>; }

createRoot(document.getElementById("root")!).render(<App />);
