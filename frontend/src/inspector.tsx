import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { actionForType, defaultMiddlewareSettings, KIND_META, splitLines, type Config, type GraphNode, type JsonObject } from "./graph-model";

const LIMEN_PROTOCOLS = [
  { value: "http1", label: "HTTP/1.1", hint: "明文或 TLS" },
  { value: "http2", label: "HTTP/2", hint: "TLS + ALPN" },
  { value: "h2c", label: "h2c", hint: "明文 HTTP/2" },
  { value: "http3", label: "HTTP/3", hint: "TLS + QUIC" },
];
const ROUTE_PROTOCOLS = [
  { value: "http", label: "普通 HTTP", hint: "短请求" },
  { value: "sse", label: "SSE", hint: "事件流" },
  { value: "websocket", label: "WebSocket", hint: "升级连接" },
];
const ROUTE_MIDDLEWARE_TYPES = [
  { value: "buffer", label: "Buffer", hint: "缓存完整响应" },
  { value: "body_limit", label: "Body Limit", hint: "限制请求体" },
];
const SERVICE_MIDDLEWARE_TYPES = [
  ...ROUTE_MIDDLEWARE_TYPES,
  { value: "in_flight", label: "In Flight", hint: "限制并发请求" },
];

function middlewareOption(definition: JsonObject | undefined, allowedTypes: { value: string; label: string; hint: string }[]) {
  if (!definition) return undefined;
  const configured = SERVICE_MIDDLEWARE_TYPES.filter(option => definition[option.value] != null);
  if (configured.length !== 1) return undefined;
  return allowedTypes.find(option => option.value === configured[0].value);
}

export function InspectorModal({ node, draft, onUpdate, onRemove, onClose, onCreateMiddleware, onUpdateMiddleware }: { node: GraphNode; draft: Config; onUpdate: (patch: JsonObject) => void; onRemove: () => void; onClose: () => void; onCreateMiddleware?: () => void; onUpdateMiddleware?: (name: string, definition: JsonObject) => void }) {
  const modalRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const modal = modalRef.current;
    const focusable = modal?.querySelector<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex=\"-1\"])");
    focusable?.focus();
    return () => previous?.focus();
  }, []);
  function keepFocus(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key !== "Tab") return;
    const focusable = Array.from(modalRef.current?.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex=\"-1\"])") || []);
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
  return <div className="inspector-modal-backdrop" onMouseDown={onClose}>
    <div ref={modalRef} className="inspector-modal" role="dialog" aria-modal="true" aria-labelledby="node-inspector-title" onMouseDown={event => event.stopPropagation()} onKeyDown={keepFocus}>
      <div className="inspector-head"><div><span className="eyebrow">{KIND_META[node.kind].label.toUpperCase()} EDITOR</span><h3 id="node-inspector-title">{node.name}</h3></div><div className="inspector-head-actions">{node.kind !== "action" && <button className="icon-button danger" title="删除节点" aria-label={`删除 ${node.name}`} onClick={onRemove}>×</button>}<button className="icon-button close-button" title="关闭编辑器" aria-label="关闭编辑器" onClick={onClose}>×</button></div></div>
      {node.kind === "route" && <RouteInspector node={node} draft={draft} onUpdate={onUpdate} onCreateMiddleware={onCreateMiddleware} onUpdateMiddleware={onUpdateMiddleware} />}{node.kind === "middleware" && <MiddlewareInspector node={node} draft={draft} onUpdate={onUpdate} />}{node.kind === "service" && <ServiceInspector node={node} draft={draft} onUpdate={onUpdate} />}{node.kind === "limen" && <LimenInspector node={node} draft={draft} onUpdate={onUpdate} />}{node.kind === "action" && <ActionInspector node={node} draft={draft} />}
    </div>
  </div>;
}

function RouteInspector({ node, draft, onUpdate, onCreateMiddleware, onUpdateMiddleware }: { node: GraphNode; draft: Config; onUpdate: (patch: JsonObject) => void; onCreateMiddleware?: () => void; onUpdateMiddleware?: (name: string, definition: JsonObject) => void }) {
  const route = draft.routes?.find(item => `route:${item.name}` === node.id) || {};
  const action = route.action || {};
  const actionType = action.forward ? "forward" : action.redirect ? "redirect" : action.respond ? "respond" : "forward";
  const currentService = action.forward?.service || route.service || "";
  const serviceNames = [...new Set([...(Object.keys(draft.services || {})), currentService].filter(Boolean))];
  return <div className="inspector-form">
    <Field label="Route name"><input value={route.name || ""} readOnly /></Field>
    <Field label="Limen"><select value={route.limen || ""} onChange={event => onUpdate({ limen: event.target.value })}><option value="">所有入口</option>{Object.keys(draft.limens || {}).map(name => <option value={name} key={name}>{name}</option>)}</select></Field>
    <Field label="Match" hint="支持 Host、PathPrefix、Method 等规则组合"><textarea className="route-match-editor" rows={5} value={route.match || ""} onChange={event => onUpdate({ match: event.target.value })} placeholder="Host(`api.example.com`) && PathPrefix(`/api`) && Method(`GET`)" spellCheck={false} /></Field>
    <Field label="Priority"><input type="number" min={0} value={route.priority ?? 0} onChange={event => onUpdate({ priority: Number(event.target.value) || 0 })} /></Field>
    <Field label="Action"><select value={actionType} onChange={event => onUpdate({ action: actionForType(event.target.value, route, draft) })}><option value="forward">Forward to Service</option><option value="redirect">Redirect</option><option value="respond">Direct Response</option></select></Field>
    {actionType === "forward" && <Field label="Service"><select value={currentService} onChange={event => onUpdate({ action: { forward: { service: event.target.value } } })}><option value="">选择 Service</option>{serviceNames.map(name => <option value={name} key={name}>{name}{!draft.services?.[name] ? "（未定义）" : ""}</option>)}</select></Field>}
    {actionType === "redirect" && <><Field label="Status"><input type="number" min={300} max={399} value={action.redirect?.status ?? 308} onChange={event => onUpdate({ action: { redirect: { ...action.redirect, status: Number(event.target.value) || 0 } } })} /></Field><Field label="Location"><input value={action.redirect?.location || ""} onChange={event => onUpdate({ action: { redirect: { ...action.redirect, location: event.target.value } } })} /></Field></>}
    {actionType === "respond" && <><Field label="Status"><input type="number" min={100} max={599} value={action.respond?.status ?? 200} onChange={event => onUpdate({ action: { respond: { ...action.respond, status: Number(event.target.value) || 0 } } })} /></Field><Field label="Body"><textarea value={action.respond?.body || ""} onChange={event => onUpdate({ action: { respond: { ...action.respond, body: event.target.value } } })} /></Field></>}
    <MultiSelect label="Protocols" hint="可多选；Match 与结构化字段不能同时发布" options={ROUTE_PROTOCOLS} value={route.protocols || []} onChange={value => onUpdate({ protocols: value })} />
    <RouteMiddlewareCanvas route={route} draft={draft} onUpdate={onUpdate} onUpdateMiddleware={onUpdateMiddleware} />
    <MiddlewarePicker route={route} draft={draft} onUpdate={onUpdate} onCreateMiddleware={onCreateMiddleware} />
  </div>;
}

function RouteMiddlewareCanvas({ route, draft, onUpdate, onUpdateMiddleware }: { route: JsonObject; draft: Config; onUpdate: (patch: JsonObject) => void; onUpdateMiddleware?: (name: string, definition: JsonObject) => void }) {
  const selected: string[] = route.middlewares || [];
  const [editingName, setEditingName] = useState<string | null>(null);
  function move(index: number, offset: number) {
    const nextIndex = index + offset;
    if (nextIndex < 0 || nextIndex >= selected.length) return;
    const next = [...selected];
    [next[index], next[nextIndex]] = [next[nextIndex], next[index]];
    onUpdate({ middlewares: next });
  }
  return <section className="route-middleware-canvas"><div className="route-flow-head"><div><strong>Middleware flow</strong><small>这里的顺序就是该 Route 的执行顺序，可直接编辑参数。</small></div><span>{selected.length}</span></div><div className="middleware-flow">{selected.length === 0 ? <div className="flow-empty">尚未添加 Middleware</div> : selected.map((name, index) => <div className="middleware-step-wrap" key={`${name}-${index}`}><div className="middleware-step"><span className="step-index">{String(index + 1).padStart(2, "0")}</span><strong>{name}</strong><div className="step-actions"><button type="button" aria-label={`编辑 ${name} 参数`} onClick={() => setEditingName(current => current === name ? null : name)}>参数</button><button type="button" aria-label={`将 ${name} 上移`} disabled={index === 0} onClick={() => move(index, -1)}>↑</button><button type="button" aria-label={`将 ${name} 下移`} disabled={index === selected.length - 1} onClick={() => move(index, 1)}>↓</button></div></div>{index < selected.length - 1 && <span className="flow-arrow">→</span>}</div>)}</div>{editingName && selected.includes(editingName) && onUpdateMiddleware && <MiddlewareParameters name={editingName} draft={draft} onChange={onUpdateMiddleware} />}</section>;
}

function MiddlewarePicker({ route, draft, onUpdate, onCreateMiddleware, allowedTypes = ROUTE_MIDDLEWARE_TYPES, title = "可用 Middleware" }: { route: JsonObject; draft: Config; onUpdate: (patch: JsonObject) => void; onCreateMiddleware?: () => void; allowedTypes?: { value: string; label: string; hint: string }[]; title?: string }) {
  const selected: string[] = route.middlewares || [];
  const names = [...new Set([...Object.keys(draft.middlewares || {}), ...selected])];
  function isAllowed(name: string) {
    if (selected.includes(name)) return true;
    const definition = draft.middlewares?.[name];
    return Boolean(middlewareOption(definition, allowedTypes));
  }
  function toggle(name: string) {
    if (!selected.includes(name) && !isAllowed(name)) return;
    onUpdate({ middlewares: selected.includes(name) ? selected.filter(item => item !== name) : [...selected, name] });
  }
  return <section className="middleware-picker"><div className="picker-head"><div><div className="field-label">{title}</div><small className="picker-hint">仅显示当前作用域支持的策略；共享定义可在多个对象中复用。</small></div>{onCreateMiddleware && <button type="button" className="small-action" onClick={onCreateMiddleware}>＋ 新建 Middleware</button>}</div>{names.length === 0 && <small className="muted">还没有可用 Middleware，可直接新建一个。</small>}{names.map(name => { const definition = draft.middlewares?.[name]; const option = middlewareOption(definition, allowedTypes); const allowed = isAllowed(name); return <label className={`check-row ${allowed ? "" : "unsupported-row"}`} key={name}><input type="checkbox" checked={selected.includes(name)} disabled={!allowed} onChange={() => toggle(name)} /><span><strong>{name}</strong>{option && <small className="middleware-kind">{option.label}</small>}{!definition && <small className="missing-label">未定义</small>}{definition && !option && <small className="missing-label">不适用于此处</small>}</span></label>; })}</section>;
}

function MiddlewareParameters({ name, draft, onChange }: { name: string; draft: Config; onChange: (name: string, definition: JsonObject) => void }) {
  const definition = draft.middlewares?.[name] || {};
  const currentType = ROUTE_MIDDLEWARE_TYPES.find(option => definition[option.value] != null)?.value || "buffer";
  const type = ROUTE_MIDDLEWARE_TYPES.some(option => option.value === currentType) ? currentType : "buffer";
  const value = definition[type] || defaultMiddlewareSettings(type);
  function changeType(nextType: string) { onChange(name, { [nextType]: defaultMiddlewareSettings(nextType) }); }
  function changeValue(patch: JsonObject) { onChange(name, { [type]: { ...value, ...patch } }); }
  return <div className="middleware-parameters"><div className="parameters-head"><strong>{name} 参数</strong><small>仅显示 Janus 当前支持的 Route Middleware</small></div><Field label="Type"><select value={type} onChange={event => changeType(event.target.value)}>{ROUTE_MIDDLEWARE_TYPES.map(option => <option value={option.value} key={option.value}>{option.label} · {option.hint}</option>)}</select></Field><div className="parameter-fields">{type === "buffer" && <Field label="Max response body (bytes)"><input type="number" min={1} max={67108864} value={value.max_response_body_bytes ?? 1048576} onChange={event => changeValue({ max_response_body_bytes: Number(event.target.value) || 0 })} /></Field>}{type === "body_limit" && <Field label="Max body (bytes)"><input type="number" min={1} max={1073741824} value={value.max_bytes ?? 10485760} onChange={event => changeValue({ max_bytes: Number(event.target.value) || 0 })} /></Field>}</div></div>;
}

function MiddlewareInspector({ node, draft, onUpdate }: { node: GraphNode; draft: Config; onUpdate: (patch: JsonObject) => void }) {
  const definition = draft.middlewares?.[node.name] || {};
  const type = SERVICE_MIDDLEWARE_TYPES.find(option => definition[option.value] != null)?.value || "buffer";
  const value = definition[type] || {};
  function changeType(nextType: string) { onUpdate({ definition: { [nextType]: defaultMiddlewareSettings(nextType) } }); }
  function changeValue(patch: JsonObject) { onUpdate({ definition: { [type]: { ...value, ...patch } } }); }
  return <div className="inspector-form"><Field label="Middleware name"><input value={node.name} readOnly /></Field><Field label="Type"><select value={type} onChange={event => changeType(event.target.value)}>{SERVICE_MIDDLEWARE_TYPES.map(option => <option value={option.value} key={option.value}>{option.label}</option>)}</select></Field>{type === "buffer" && <Field label="Max response body (bytes)"><input type="number" min={1} max={67108864} value={value.max_response_body_bytes ?? 1048576} onChange={event => changeValue({ max_response_body_bytes: Number(event.target.value) || 0 })} /></Field>}{type === "body_limit" && <Field label="Max body (bytes)"><input type="number" min={1} max={1073741824} value={value.max_bytes ?? 10485760} onChange={event => changeValue({ max_bytes: Number(event.target.value) || 0 })} /></Field>}{type === "in_flight" && <Field label="Max concurrent"><input type="number" min={1} max={10000} value={value.max_concurrent ?? 100} onChange={event => changeValue({ max_concurrent: Number(event.target.value) || 0 })} /></Field>}<div className="inspector-note">Middleware 名称是共享引用。修改类型或参数会影响所有引用它的 Route 和 Service。</div></div>;
}

function ServiceInspector({ node, draft, onUpdate }: { node: GraphNode; draft: Config; onUpdate: (patch: JsonObject) => void }) {
  const service = draft.services?.[node.name] || {};
  const health = service.health_check;
  function updateHealth(patch: JsonObject) { onUpdate({ health_check: { ...health, ...patch } }); }
  return <div className="inspector-form">
    <InspectorGroup title="基本信息" description="Service 是 Route 最终转发到的上游池。">
      <Field label="Service name"><input value={node.name} readOnly /></Field>
      <Field label="Upstreams" hint="每行一个 URL"><textarea value={(service.upstreams || []).join("\n")} onChange={event => onUpdate({ upstreams: splitLines(event.target.value) })} placeholder="http://127.0.0.1:9000" /></Field>
      <MiddlewarePicker route={{ middlewares: service.middlewares || [] }} draft={draft} onUpdate={patch => onUpdate({ middlewares: patch.middlewares })} allowedTypes={SERVICE_MIDDLEWARE_TYPES} title="Service Middleware" />
    </InspectorGroup>
    <InspectorGroup title="Health check" description="主动探测每个 upstream，失败后暂时摘除。">
      {health ? <>
        <Field label="Path"><input value={health.path || ""} onChange={event => updateHealth({ path: event.target.value })} placeholder="/healthz" /></Field>
        <div className="field-grid">
          <Field label="Interval"><input value={health.interval || ""} onChange={event => updateHealth({ interval: event.target.value })} placeholder="30s" /></Field>
          <Field label="Timeout"><input value={health.timeout || ""} onChange={event => updateHealth({ timeout: event.target.value })} placeholder="5s" /></Field>
          <Field label="Jitter"><input value={health.jitter || ""} onChange={event => updateHealth({ jitter: event.target.value })} placeholder="1s" /></Field>
          <Field label="Expected status"><input type="number" min={200} max={599} value={health.expected_status ?? 200} onChange={event => updateHealth({ expected_status: Number(event.target.value) || 0 })} /></Field>
          <Field label="Unhealthy threshold"><input type="number" min={1} max={100} value={health.unhealthy_threshold ?? 1} onChange={event => updateHealth({ unhealthy_threshold: Number(event.target.value) || 0 })} /></Field>
          <Field label="Healthy threshold"><input type="number" min={1} max={100} value={health.healthy_threshold ?? 1} onChange={event => updateHealth({ healthy_threshold: Number(event.target.value) || 0 })} /></Field>
        </div>
        <button className="small-action danger-action" onClick={() => onUpdate({ health_check: undefined })}>移除健康检查</button>
      </> : <button className="small-action" onClick={() => onUpdate({ health_check: { path: "/healthz", interval: "30s", timeout: "5s", jitter: "0s", unhealthy_threshold: 1, healthy_threshold: 1, expected_status: 200 } })}>＋ 添加健康检查</button>}
    </InspectorGroup>
  </div>;
}

function LimenInspector({ node, draft, onUpdate }: { node: GraphNode; draft: Config; onUpdate: (patch: JsonObject) => void }) {
  const limen = draft.limens?.[node.name] || {};
  const tls = limen.tls;
  const http3 = limen.http3;
  return <div className="inspector-form">
    <InspectorGroup title="监听入口" description="定义地址、协议和可信代理范围。">
      <Field label="Limen name"><input value={node.name} readOnly /></Field>
      <Field label="Address"><input value={limen.address || ""} onChange={event => onUpdate({ address: event.target.value })} placeholder="127.0.0.1:8080" /></Field>
      <MultiSelect label="Protocols" options={LIMEN_PROTOCOLS} value={limen.protocols || []} onChange={value => onUpdate({ protocols: value })} />
      <div className="protocol-note">HTTP/2、HTTP/3 需要 TLS；h2c 不能与 TLS 或 HTTP/2 同时启用。最终约束由后端校验。</div>
      <Field label="Trusted proxies" hint="每行一个 CIDR"><textarea value={(limen.trusted_proxies || []).join("\n")} onChange={event => onUpdate({ trusted_proxies: splitLines(event.target.value) })} placeholder="10.0.0.0/8" /></Field>
    </InspectorGroup>
    <InspectorGroup title="TLS" description="启用后由该 Limen 负责 TLS 握手。">
      <Toggle label="启用 TLS" checked={Boolean(tls)} onChange={checked => onUpdate({ tls: checked ? { cert_file: "certs/janus.crt", key_file: "certs/janus.key", min_version: "1.3" } : undefined })} />
      {tls && <div className="nested-fields">
        <Field label="Certificate file"><input value={tls.cert_file || ""} onChange={event => onUpdate({ tls: { ...tls, cert_file: event.target.value } })} placeholder="certs/janus.crt" /></Field>
        <Field label="Key file"><input value={tls.key_file || ""} onChange={event => onUpdate({ tls: { ...tls, key_file: event.target.value } })} placeholder="certs/janus.key" /></Field>
        <Field label="Minimum TLS version"><select value={tls.min_version || "1.2"} onChange={event => onUpdate({ tls: { ...tls, min_version: event.target.value } })}><option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option></select></Field>
      </div>}
    </InspectorGroup>
    <InspectorGroup title="HTTP/3" description="HTTP/3 使用独立的 QUIC/UDP 传输。">
      <Toggle label="启用 HTTP/3" checked={Boolean(http3)} onChange={checked => onUpdate({ http3: checked ? { max_concurrent_streams: 100 } : undefined })} />
      {http3 && <div className="nested-fields"><Field label="Max concurrent streams"><input type="number" min={1} max={1000000} value={http3.max_concurrent_streams ?? 100} onChange={event => onUpdate({ http3: { ...http3, max_concurrent_streams: Number(event.target.value) || 0 } })} /></Field></div>}
    </InspectorGroup>
  </div>;
}

function ActionInspector({ node, draft }: { node: GraphNode; draft: Config }) {
  const route = draft.routes?.find(item => `action:${item.name}` === node.id) || {};
  const action = route.action || {};
  const type = action.redirect ? "Redirect" : "Respond";
  const value = action[type.toLowerCase()] || {};
  return <div className="inspector-form"><div className="action-summary"><span className="graph-node-icon" style={{ background: KIND_META.action.color }}>{KIND_META.action.icon}</span><div><strong>{type}</strong><small>由 {route.name} 路由触发</small></div></div><Field label="Status"><input value={value.status || 200} readOnly /></Field>{type === "Redirect" ? <Field label="Location"><input value={value.location || ""} readOnly /></Field> : <Field label="Body"><textarea value={value.body || ""} readOnly /></Field>}<div className="inspector-note">Action 节点的内容请在 Route 节点中修改。</div></div>;
}

function MultiSelect({ label, hint, options, value, onChange, disabled = false, single = false }: { label: string; hint?: string; options: { value: string; label: string; hint: string }[]; value: string[]; onChange: (value: string[]) => void; disabled?: boolean; single?: boolean }) {
  function toggle(option: string, checked: boolean) {
    if (single) { onChange(checked ? [option] : []); return; }
    onChange(checked ? [...new Set([...value, option])] : value.filter(item => item !== option));
  }
  return <div className={`multi-select ${disabled ? "disabled" : ""}`}><div className="multi-select-label"><span>{label}</span>{hint && <small>{hint}</small>}</div><div className="multi-select-options">{options.map(option => <label className="multi-select-option" key={option.value}><input type="checkbox" checked={value.includes(option.value)} disabled={disabled} onChange={event => toggle(option.value, event.target.checked)} /><span><strong>{option.label}</strong><small>{option.hint}</small></span></label>)}</div>{disabled && <small className="multi-select-note">当前使用 Match 规则，请在 Match 中通过 Protocol(...) 控制协议。</small>}</div>;
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) { return <label className="builder-field"><span>{label}{hint && <small>{hint}</small>}</span>{children}</label>; }
function InspectorGroup({ title, description, children }: { title: string; description: string; children: ReactNode }) { return <section className="inspector-group"><div className="inspector-group-head"><strong>{title}</strong><small>{description}</small></div>{children}</section>; }
function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) { return <label className="toggle-row"><span>{label}</span><input type="checkbox" checked={checked} onChange={event => onChange(event.target.checked)} /><i /></label>; }
