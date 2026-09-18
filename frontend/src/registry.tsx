import { useState } from "react";
import type { Config, JsonObject } from "./graph-model";
import "./registry.css";

type Registry = JsonObject;

function uniqueName(prefix: string, names: string[]) {
  let index = 1;
  while (names.includes(`${prefix}-${index}`)) index++;
  return `${prefix}-${index}`;
}

function registryConfig(config: Config, nacos: Record<string, Registry>): Config {
  return { ...config, discovery: { ...(config.discovery || {}), nacos } };
}

export function RegistryPage({ draft, onChange }: { draft: Config | null; onChange: (next: Config) => void }) {
  const [editingName, setEditingName] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [beforeCreate, setBeforeCreate] = useState<Config | null>(null);
  const [health, setHealth] = useState<Record<string, { healthy: boolean; latency_ms?: number; error?: string }>>({});
  const [testing, setTesting] = useState<string | null>(null);
  if (!draft) return <section className="content"><div className="empty">正在加载配置…</div></section>;
  const config = draft;

  const nacos = (config.discovery?.nacos || {}) as Record<string, Registry>;
  const entries = Object.entries(nacos);
  const editing = editingName ? nacos[editingName] : undefined;

  function create() {
    const name = uniqueName("registry", Object.keys(nacos));
    const next = { servers: [{ address: "127.0.0.1", port: 8848 }], namespace_id: "public", timeout: "5s", stale_after: "2m" };
    setBeforeCreate(config);
    setCreating(true);
    setEditingName(name);
    onChange(registryConfig(config, { ...nacos, [name]: next }));
  }
  function update(patch: JsonObject) {
    if (!editingName) return;
    onChange(registryConfig(config, { ...nacos, [editingName]: { ...(nacos[editingName] || {}), ...patch } }));
  }
  function remove() {
    if (!editingName) return;
    const next = { ...nacos };
    delete next[editingName];
    onChange(registryConfig(config, next));
    setEditingName(null);
    setCreating(false);
    setBeforeCreate(null);
  }
  function confirmCreate() {
    setCreating(false);
    setBeforeCreate(null);
    setEditingName(null);
  }
  function cancelCreate() {
    if (beforeCreate) onChange(beforeCreate);
    setCreating(false);
    setBeforeCreate(null);
    setEditingName(null);
  }
  function closeEditor() {
    if (creating) cancelCreate();
    else setEditingName(null);
  }
  async function testRegistry(name: string) {
    setTesting(name);
    try {
      const response = await fetch("/api/v1/discovery/registries/health", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || `HTTP ${response.status}`);
      setHealth(current => ({ ...current, [name]: result }));
    } catch (error) {
      setHealth(current => ({ ...current, [name]: { healthy: false, error: String(error) } }));
    } finally {
      setTesting(null);
    }
  }

  return (
    <section className="content registry-page">
      <div className="section-head"><div><p>管理 Nacos Registry 连接。Service 只引用这里定义的 Registry。</p></div><button className="primary" onClick={create}>＋ 新建 Registry</button></div>
      <div className="registry-layout">
        <div className="list registry-list">
          {entries.map(([name, value]) => {
            const result = health[name];
            return <div className="registry-row" key={name}><button className="registry-row-main" onClick={() => setEditingName(name)}><div className="list-icon registry-icon">N</div><div><strong>{name}</strong><small>{value.namespace_id || "public"} · {(value.servers || []).length} server</small></div></button><div className="registry-row-actions"><span className={result ? (result.healthy ? "health-ok" : "health-failed") : "health-unknown"}>{result ? (result.healthy ? `Healthy${result.latency_ms != null ? ` · ${result.latency_ms}ms` : ""}` : "Unhealthy") : "未测试"}</span><button className="small-action" disabled={testing === name} onClick={() => testRegistry(name)}>{testing === name ? "测试中…" : "测试健康"}</button></div></div>;
          })}
          {entries.length === 0 && <div className="empty">暂无 Registry，点击右上角创建。</div>}
        </div>
        <div className="registry-side-note"><strong>使用方式</strong><p>在 Service 编辑器中选择 Nacos service discovery，然后引用这里的 Registry。</p><small>账号密码不会在配置读取接口中回显；编辑时留空表示保持原凭据。</small></div>
      </div>
      {editing && <RegistryEditor name={editingName!} registry={editing} onUpdate={update} onRemove={remove} onClose={closeEditor} onConfirm={creating ? confirmCreate : undefined} onCancel={creating ? cancelCreate : undefined} />}
    </section>
  );
}

function RegistryEditor({ name, registry, onUpdate, onRemove, onClose, onConfirm, onCancel }: { name: string; registry: Registry; onUpdate: (patch: JsonObject) => void; onRemove: () => void; onClose: () => void; onConfirm?: () => void; onCancel?: () => void }) {
  const servers = (registry.servers || []) as JsonObject[];
  function updateServer(index: number, patch: JsonObject) {
    onUpdate({ servers: servers.map((server, current) => current === index ? { ...server, ...patch } : server) });
  }
  function addServer() { onUpdate({ servers: [...servers, { address: "127.0.0.1", port: 8848 }] }); }
  function removeServer(index: number) { onUpdate({ servers: servers.filter((_, current) => current !== index) }); }
  return <div className="registry-modal-backdrop" onMouseDown={onClose}>
    <div className="registry-modal" role="dialog" aria-modal="true" onMouseDown={event => event.stopPropagation()}>
      <div className="inspector-head">
        <div><span className="eyebrow">NACOS REGISTRY</span><h3>{name}</h3></div>
        <div className="inspector-head-actions"><button className="icon-button danger" onClick={onRemove}>删除</button><button className="icon-button close-button" onClick={onClose}>×</button></div>
      </div>
      <div className="inspector-form">
        <label className="builder-field"><span>Registry name</span><input value={name} readOnly /></label>
        <label className="builder-field"><span>Namespace ID</span><input value={registry.namespace_id || "public"} onChange={event => onUpdate({ namespace_id: event.target.value })} placeholder="public" /></label>
        <div className="registry-servers"><div className="registry-subhead"><strong>Servers</strong><button className="small-action" onClick={addServer}>＋ 添加 Server</button></div>
          {servers.map((server, index) => <div className="registry-server" key={index}>
            <input aria-label={`Server ${index + 1} address`} value={server.address || ""} onChange={event => updateServer(index, { address: event.target.value })} placeholder="127.0.0.1" />
            <input aria-label={`Server ${index + 1} port`} type="number" min={1} max={65535} value={server.port ?? 8848} onChange={event => updateServer(index, { port: Number(event.target.value) || 0 })} />
            <input aria-label={`Server ${index + 1} gRPC port`} type="number" min={0} max={65535} value={server.grpc_port ?? ""} onChange={event => updateServer(index, { grpc_port: Number(event.target.value) || 0 })} placeholder="gRPC port" />
            <button className="small-action danger-action" disabled={servers.length <= 1} onClick={() => removeServer(index)}>移除</button>
          </div>)}
        </div>
        <div className="field-grid">
          <label className="builder-field"><span>Username</span><input value={registry.username || ""} onChange={event => onUpdate({ username: event.target.value })} autoComplete="off" /></label>
          <label className="builder-field"><span>Password</span><input type="password" value={registry.password || ""} onChange={event => onUpdate({ password: event.target.value, password_env: undefined })} placeholder="留空保持当前密码" autoComplete="new-password" /></label>
          <label className="builder-field"><span>Password env</span><input value={registry.password_env || ""} onChange={event => onUpdate({ password_env: event.target.value, password: undefined })} placeholder="JANUS_NACOS_PASSWORD" /></label>
          <label className="builder-field"><span>Timeout</span><input value={registry.timeout || "5s"} onChange={event => onUpdate({ timeout: event.target.value })} /></label>
          <label className="builder-field"><span>Stale after</span><input value={registry.stale_after || "2m"} onChange={event => onUpdate({ stale_after: event.target.value })} /></label>
        </div>
        <div className="inspector-note">密码字段为空时，发布会保留当前已保存的密码。新增 Registry 则必须填写密码或 password_env（也可以使用匿名 Nacos）。</div>
      </div>
      {onConfirm && <div className="inspector-modal-footer"><button className="ghost" onClick={onCancel || onClose}>取消</button><button className="primary" onClick={onConfirm}>确认</button></div>}
    </div>
  </div>;
}
