import { useEffect, useRef, useState } from "react";
import { registryNames, splitLines } from "./model";
import { Drawer, Field } from "./ui";
import type { JanusConfig, Limen, Middleware, MiddlewareCapability, MiddlewareFieldCapability, NacosRegistry, Service } from "./types";

export function middlewareTypeOf(def: Middleware, catalog: MiddlewareCapability[]): string {
  return catalog.find((item) => def[item.type] != null)?.type || Object.keys(def).find((key) => key !== "scope") || catalog[0]?.type || "";
}

export function createMiddlewareDefinition(capability: MiddlewareCapability, requestedScope?: string): Middleware {
  const policy: Record<string, unknown> = {};
  for (const field of capability.fields) {
    if (field.default !== undefined) policy[field.name] = structuredClone(field.default);
  }
  let scope = requestedScope;
  if (!scope && capability.scopes.length === 1) scope = capability.scopes[0];
  return { ...(scope ? { scope } : {}), [capability.type]: policy };
}

export function ServiceEditor({
  draft,
  name,
  value,
  isNew,
  onName,
  onChange,
  onConfirm,
  onCancel,
  onClose,
  onDelete,
  onOpenMiddlewareManager,
  onOpenRegistryManager,
}: {
  draft: JanusConfig;
  name: string;
  value: Service;
  isNew: boolean;
  onName?: (name: string) => void;
  onChange: (value: Service) => void;
  onConfirm: () => void;
  onCancel: () => void;
  onClose: () => void;
  onDelete?: () => void;
  onOpenMiddlewareManager?: () => void;
  onOpenRegistryManager?: () => void;
}) {
  const registries = registryNames(draft);
  const source = value.nacos ? "nacos" : "static";
  const middlewares = draft?.middlewares || {};
  const set = (patch: Partial<Service>) => onChange({ ...value, ...patch });

  function setSource(next: string) {
    if (next === "nacos") {
      set({
        upstreams: undefined,
        health_check: undefined,
        nacos: value.nacos || { registry: registries[0] || "", service_name: "", group_name: "DEFAULT_GROUP", scheme: "http" },
      });
    } else {
      set({ nacos: undefined, upstreams: value.upstreams?.length ? value.upstreams : ["http://127.0.0.1:9000"] });
    }
  }

  return (
    <Drawer
      title={isNew ? "新建 Service" : `编辑 ${name}`}
      subtitle="上游二选一：静态地址池，或 Nacos 服务发现。"
      onClose={onClose}
      onConfirm={onConfirm}
      onCancel={onCancel}
      onDelete={onDelete}
    >
      {isNew && (
        <Field label="名称">
          <input value={name} onChange={(e) => onName?.(e.target.value)} placeholder="service-1" />
        </Field>
      )}
      <Field label="来源">
        <select value={source} onChange={(e) => setSource(e.target.value)}>
          <option value="static">Static upstreams</option>
          <option value="nacos">Nacos service discovery</option>
        </select>
      </Field>
      {source === "static" ? (
        <>
          <Field label="Upstreams" hint="每行一个 http(s) origin，例如 http://127.0.0.1:9000">
            <DraftTextarea rows={3} value={(value.upstreams || []).join("\n")} parse={splitLines} onChange={(next) => set({ upstreams: next as string[] })} placeholder="http://127.0.0.1:9000" />
          </Field>
          {value.health_check ? (
            <>
              <div className="group-title">健康检查</div>
              <Field label="Path">
                <input value={value.health_check.path} onChange={(e) => set({ health_check: { ...value.health_check!, path: e.target.value } })} />
              </Field>
              <div className="grid-2">
                <Field label="Interval">
                  <input value={value.health_check.interval || ""} onChange={(e) => set({ health_check: { ...value.health_check!, interval: e.target.value } })} placeholder="30s" />
                </Field>
                <Field label="Timeout">
                  <input value={value.health_check.timeout || ""} onChange={(e) => set({ health_check: { ...value.health_check!, timeout: e.target.value } })} placeholder="5s" />
                </Field>
                <Field label="Unhealthy threshold">
                  <input type="number" min={1} value={value.health_check.unhealthy_threshold ?? 1} onChange={(e) => set({ health_check: { ...value.health_check!, unhealthy_threshold: Number(e.target.value) || 1 } })} />
                </Field>
                <Field label="Healthy threshold">
                  <input type="number" min={1} value={value.health_check.healthy_threshold ?? 1} onChange={(e) => set({ health_check: { ...value.health_check!, healthy_threshold: Number(e.target.value) || 1 } })} />
                </Field>
              </div>
              <button type="button" className="btn small danger" onClick={() => set({ health_check: undefined })}>
                移除健康检查
              </button>
            </>
          ) : (
            <button
              type="button"
              className="btn small"
              onClick={() =>
                set({ health_check: { path: "/healthz", interval: "30s", timeout: "5s", unhealthy_threshold: 1, healthy_threshold: 1 } })
              }
            >
              ＋ 添加健康检查
            </button>
          )}
        </>
      ) : (
        <>
          <Field label="Nacos Registry" hint="在管理弹窗中新建、测试连通性后选用">
            <div className="registry-pick">
              <input value={value.nacos?.registry || ""} readOnly placeholder="尚未选择" />
              <button type="button" className="btn small primary" onClick={onOpenRegistryManager}>管理 / 选择</button>
            </div>
          </Field>
          {registries.length === 0 && <p className="error-text">还没有 Registry，点「管理 / 选择」新建一个。</p>}
          <Field label="Service name">
            <input value={value.nacos?.service_name || ""} onChange={(e) => set({ nacos: { ...value.nacos!, service_name: e.target.value } })} />
          </Field>
          <div className="grid-2">
            <Field label="Group">
              <input value={value.nacos?.group_name || "DEFAULT_GROUP"} onChange={(e) => set({ nacos: { ...value.nacos!, group_name: e.target.value } })} />
            </Field>
            <Field label="Scheme">
              <select value={value.nacos?.scheme || "http"} onChange={(e) => set({ nacos: { ...value.nacos!, scheme: e.target.value } })}>
                <option value="http">HTTP</option>
                <option value="https">HTTPS</option>
              </select>
            </Field>
          </div>
          <Field label="Clusters" hint="每行一个，可留空">
            <DraftTextarea rows={2} value={(value.nacos?.clusters || []).join("\n")} parse={splitLines} onChange={(next) => set({ nacos: { ...value.nacos!, clusters: next as string[] } })} placeholder="DEFAULT" />
          </Field>
        </>
      )}
      <div className="check-group">
        <div className="field-label-row">
          <div className="field-label">中间件（{(value.middlewares || []).length}）</div>
          {onOpenMiddlewareManager && <button type="button" className="btn small primary" onClick={onOpenMiddlewareManager}>管理中间件</button>}
        </div>
        {(value.middlewares || []).length === 0 ? (
          <small className="muted">尚未挂载。在管理弹窗中从 class 实例化、再到 flow 里选用编排。</small>
        ) : (
          <div className="mw-flow-preview">
            {(value.middlewares || []).map((mw, index) => (
              <span className="mw-flow-chip" key={`${mw}-${index}`}>
                <em>{index + 1}</em><strong>{mw}</strong>{!middlewares[mw] && <small className="error-text">未定义</small>}
              </span>
            ))}
          </div>
        )}
      </div>
    </Drawer>
  );
}

function mapToLines(value: unknown): string {
  if (!value || typeof value !== "object" || Array.isArray(value)) return "";
  return Object.entries(value as Record<string, unknown>).map(([key, item]) => `${key}: ${String(item)}`).join("\n");
}

function linesToMap(value: string): Record<string, string> {
  const result: Record<string, string> = {};
  for (const line of value.split(/\r?\n/)) {
    const index = line.indexOf(":");
    if (index <= 0) continue;
    const key = line.slice(0, index).trim();
    if (key) result[key] = line.slice(index + 1).trim();
  }
  return result;
}

function DraftTextarea({ value, rows, placeholder, parse, onChange }: {
  value: string;
  rows: number;
  placeholder: string;
  parse: (value: string) => unknown;
  onChange: (value: unknown) => void;
}) {
  const [draft, setDraft] = useState(value);
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setDraft(value);
  }, [value]);
  return (
    <textarea
      rows={rows}
      value={draft}
      placeholder={placeholder}
      onFocus={() => { focused.current = true; }}
      onBlur={() => { focused.current = false; onChange(parse(draft)); }}
      onChange={(event) => {
        setDraft(event.target.value);
        onChange(parse(event.target.value));
      }}
    />
  );
}

function CapabilityField({ field, value, onChange }: { field: MiddlewareFieldCapability; value: unknown; onChange: (value: unknown) => void }) {
  if (field.kind === "integer") {
    return <input type="number" min={field.min} max={field.max} value={typeof value === "number" ? value : Number(field.default ?? 0)} onChange={(e) => onChange(Number(e.target.value) || 0)} />;
  }
  if (field.kind === "boolean") {
    return <select value={value === true ? "true" : "false"} onChange={(e) => onChange(e.target.value === "true")}><option value="true">true</option><option value="false">false</option></select>;
  }
  if (field.kind === "string_list") {
    return <DraftTextarea rows={3} value={Array.isArray(value) ? value.join("\n") : ""} parse={splitLines} onChange={onChange} placeholder="每行一项" />;
  }
  if (field.kind === "string_map") {
    return <DraftTextarea rows={4} value={mapToLines(value)} parse={linesToMap} onChange={onChange} placeholder="Header-Name: value" />;
  }
  return <input value={typeof value === "string" ? value : String(field.default ?? "")} onChange={(e) => onChange(e.target.value)} />;
}

/** 中间件定义表单完全由后端能力目录驱动。 */
export function MiddlewareDefForm({ value, catalog, onChange }: { value: Middleware; catalog: MiddlewareCapability[]; onChange: (value: Middleware) => void }) {
  const type = middlewareTypeOf(value, catalog);
  const capability = catalog.find((item) => item.type === type);
  const selectableCapabilities = value.scope
    ? catalog.filter((item) => item.scopes.includes(value.scope as "route" | "service"))
    : catalog;
  const policy = (type && value[type] && typeof value[type] === "object" ? value[type] : {}) as Record<string, unknown>;

  function changeScope(scope: string) {
    const compatible = (candidate: MiddlewareCapability) => scope === "" ? candidate.scopes.includes("route") && candidate.scopes.includes("service") : candidate.scopes.includes(scope as "route" | "service");
    if (capability && compatible(capability)) {
      onChange({ ...value, scope: scope || undefined });
      return;
    }
    const replacement = catalog.find(compatible);
    if (replacement) onChange(createMiddlewareDefinition(replacement, scope || undefined));
  }

  function changeType(nextType: string) {
    const next = catalog.find((item) => item.type === nextType);
    if (!next) return;
    const requested = value.scope;
    const scope = requested && next.scopes.includes(requested as "route" | "service")
      ? requested
      : next.scopes.length === 1 ? next.scopes[0] : undefined;
    onChange(createMiddlewareDefinition(next, scope));
  }

  return (
    <>
      <Field label="作用域 Scope">
        <select value={value.scope || ""} onChange={(e) => changeScope(e.target.value)}>
          <option value="">未限定（按类型可用范围）</option>
          <option value="route">Route</option>
          <option value="service">Service</option>
        </select>
      </Field>
      <Field label="类型">
        <select value={type} onChange={(e) => changeType(e.target.value)}>
          {selectableCapabilities.map((item) => <option key={item.type} value={item.type}>{item.type}（{item.label}）</option>)}
        </select>
      </Field>
      {capability?.description && <p className="form-note">{capability.description}</p>}
      {capability?.fields.map((field) => (
        <Field key={field.name} label={`${field.label}${field.required ? " *" : ""}`} hint={field.description}>
          <CapabilityField
            field={field}
            value={policy[field.name]}
            onChange={(nextValue) => onChange({ ...value, [type]: { ...policy, [field.name]: nextValue } })}
          />
        </Field>
      ))}
    </>
  );
}

export function MiddlewareEditor({
  name,
  value,
  catalog,
  isNew,
  onName,
  onChange,
  onConfirm,
  onCancel,
  onClose,
  onDelete,
}: {
  name: string;
  value: Middleware;
  catalog: MiddlewareCapability[];
  isNew: boolean;
  onName: (name: string) => void;
  onChange: (value: Middleware) => void;
  onConfirm: () => void;
  onCancel: () => void;
  onClose: () => void;
  onDelete?: () => void;
}) {
  return (
    <Drawer
      title={isNew ? "新建 Middleware" : `编辑 ${name}`}
      subtitle="改名会自动同步所有 Route 与 Service 的引用。"
      onClose={onClose}
      onConfirm={onConfirm}
      onCancel={onCancel}
      onDelete={onDelete}
    >
      <Field label="名称">
        <input value={name} onChange={(e) => onName(e.target.value)} />
      </Field>
      <MiddlewareDefForm value={value} catalog={catalog} onChange={onChange} />
    </Drawer>
  );
}

/** Registry 表单（连接/认证参数），弹窗内联复用。名称由外层管理。 */
export function RegistryForm({ value, onChange }: { value: NacosRegistry; onChange: (value: NacosRegistry) => void }) {
  return (
    <>
      <Field label="Namespace ID">
        <input
          value={value.namespace_id || ""}
          placeholder="public"
          onChange={(e) => onChange({ ...value, namespace_id: e.target.value })}
        />
      </Field>
      <div className="group-title">Servers</div>
      {value.servers.map((server, index) => (
        <div className="server-row" key={index}>
          <input
            aria-label={`Server ${index + 1} address`}
            value={server.address}
            placeholder="127.0.0.1"
            onChange={(e) => {
              const servers = value.servers.map((s, i) => (i === index ? { ...s, address: e.target.value } : s));
              onChange({ ...value, servers });
            }}
          />
          <input
            aria-label={`Server ${index + 1} port`}
            type="number"
            min={1}
            max={65535}
            value={server.port}
            onChange={(e) => {
              const servers = value.servers.map((s, i) => (i === index ? { ...s, port: Number(e.target.value) || 0 } : s));
              onChange({ ...value, servers });
            }}
          />
          <button
            type="button"
            className="btn small"
            disabled={value.servers.length <= 1}
            onClick={() => onChange({ ...value, servers: value.servers.filter((_, i) => i !== index) })}
          >
            移除
          </button>
        </div>
      ))}
      <button
        type="button"
        className="btn small"
        onClick={() => onChange({ ...value, servers: [...value.servers, { address: "127.0.0.1", port: 8848 }] })}
      >
        ＋ 添加 Server
      </button>
      <div className="grid-2">
        <Field label="Username">
          <input value={value.username || ""} autoComplete="off" onChange={(e) => onChange({ ...value, username: e.target.value })} />
        </Field>
        <Field label="Password" hint="留空保持原密码">
          <input
            type="password"
            value={value.password || ""}
            autoComplete="new-password"
            onChange={(e) => onChange({ ...value, password: e.target.value, password_env: undefined })}
          />
        </Field>
        <Field label="Password env">
          <input value={value.password_env || ""} placeholder="JANUS_NACOS_PASSWORD" onChange={(e) => onChange({ ...value, password_env: e.target.value, password: undefined })} />
        </Field>
        <Field label="Timeout">
          <input value={value.timeout || ""} placeholder="5s" onChange={(e) => onChange({ ...value, timeout: e.target.value })} />
        </Field>
        <Field label="Stale after">
          <input value={value.stale_after || ""} placeholder="2m" onChange={(e) => onChange({ ...value, stale_after: e.target.value })} />
        </Field>
      </div>
      <p className="muted">username 与 password / password_env 必须成组出现。</p>
    </>
  );
}

export function LimenViewer({ name, limen, onClose, onStageLimens }: { name: string; limen: Limen; onClose: () => void; onStageLimens?: () => void }) {
  return (
    <Drawer
      title={`入口 ${name}`}
      subtitle="入口的监听配置是启动级的，此处只读；改监听地址/协议/TLS 请改配置文件并重启。"
      onClose={onClose}
      onConfirm={onClose}
      onCancel={onClose}
      confirmLabel="关闭"
    >
      <Field label="Address">
        <input value={limen.address} readOnly />
      </Field>
      <Field label="Protocols">
        <input value={(limen.protocols || []).join(", ")} readOnly />
      </Field>
      <Field label="Trusted proxies">
        <textarea rows={2} value={(limen.trusted_proxies || []).join("\n")} readOnly />
      </Field>
      <Field label="TLS">
        <input value={limen.tls ? `启用（${limen.tls.min_version || "默认"}）` : "关闭"} readOnly />
      </Field>
      {limen.http3 && (
        <Field label="HTTP/3 max_concurrent_streams">
          <input value={String(limen.http3.max_concurrent_streams ?? "")} readOnly />
        </Field>
      )}
      {onStageLimens && (
        <div className="stage-box">
          <div className="field-label">入口写文件</div>
          <small className="muted">入口变更不通过热发布；保存草稿后可将已保存入口写入生效文件，重启后生效。</small>
          <button type="button" className="btn small" onClick={onStageLimens}>写入文件（需重启生效）</button>
        </div>
      )}
    </Drawer>
  );
}
