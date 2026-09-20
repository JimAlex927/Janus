import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  clearBuiltinMiddlewareOverride,
  setBuiltinMiddlewareOverride,
  type BuiltinMiddlewareView,
  type BuiltinRequestKind,
} from "./builtinMiddleware";
import { MiddlewareDefForm, middlewareTypeOf } from "./editors";
import { Field, useDialogLifecycle } from "./ui";
import type { BuiltinMiddlewareOverrides, Middleware, MiddlewareCapability } from "./types";

export type MiddlewareScopeFilter = "route" | "service";

/** 实例是否可被某作用域的 flow 引用（与后端 AllowsScope + in_flight 约束一致）。 */
export function isFlowCompatible(def: Middleware | undefined, scope: MiddlewareScopeFilter, catalog: MiddlewareCapability[]): boolean {
  if (!def) return false;
  if (def.scope && def.scope !== scope) return false;
  const capability = catalog.find((item) => item.type === middlewareTypeOf(def, catalog));
  return Boolean(capability?.scopes.includes(scope));
}

export function instanceLabel(def: Middleware | undefined, catalog: MiddlewareCapability[]): string {
  if (!def) return "未定义";
  const kind = middlewareTypeOf(def, catalog) || "未配置";
  const capability = catalog.find((item) => item.type === kind);
  const scope = def.scope === "route" || (!def.scope && capability?.scopes.length === 1 && capability.scopes[0] === "route")
    ? "仅 Route"
    : def.scope === "service" || (!def.scope && capability?.scopes.length === 1 && capability.scopes[0] === "service")
      ? "仅 Service"
      : "共享";
  return `${kind} · ${scope}`;
}

function kindColor(def: Middleware | undefined, catalog: MiddlewareCapability[]): string {
  if (!def) return "#b91c1c";
  const index = Math.max(0, catalog.findIndex((item) => item.type === middlewareTypeOf(def, catalog)));
  return ["#536dfe", "#c2410c", "#7c3aed", "#0f766e", "#b45309", "#0369a1"][index % 6];
}

type Tab = "class" | "instance" | "flow";

function builtinKey(builtin: BuiltinMiddlewareView, index: number): string {
  return `${builtin.scope}:${builtin.name}:${index}`;
}

/** 洋葱栈视图：flow[0] 在最外层，依次包裹，最内层是 Service / 上游。 */
function StackView({
  flow,
  builtinFlow,
  instances,
  catalog,
  scope,
  coreLabel,
  chainSel,
  builtinSel,
  requestKind,
  onSelect,
  onSelectBuiltin,
}: {
  flow: string[];
  builtinFlow: BuiltinMiddlewareView[];
  instances: Record<string, Middleware>;
  catalog: MiddlewareCapability[];
  scope: MiddlewareScopeFilter;
  coreLabel: string;
  chainSel: string | null;
  builtinSel: string | null;
  requestKind: BuiltinRequestKind;
  onSelect: (name: string) => void;
  onSelectBuiltin: (key: string) => void;
}) {
  let inner: ReactNode = <div className="mw-stack-core">{coreLabel}</div>;
  for (let index = flow.length - 1; index >= 0; index--) {
    const name = flow[index];
    const def = instances[name];
    const compatible = isFlowCompatible(def, scope, catalog);
    inner = (
      <div
        key={`${name}-${index}`}
        className={`mw-stack-layer ${chainSel === name ? "selected" : ""} ${compatible ? "" : "warn"}`}
        style={{ borderColor: kindColor(def, catalog) }}
      >
        <button
          type="button"
          className="mw-stack-head"
          title={`${name} · ${instanceLabel(def, catalog)}${compatible ? "" : "（已不兼容，请移除）"}`}
          onClick={() => onSelect(name)}
        >
          <em className="mw-stack-badge" style={{ background: kindColor(def, catalog) }}>{index + 1}</em>
          <strong>{name}</strong>
          <small className={compatible ? "" : "error-text"}>{instanceLabel(def, catalog)}</small>
        </button>
        <div className="mw-stack-inner">{inner}</div>
      </div>
    );
  }
  for (let index = builtinFlow.length - 1; index >= 0; index--) {
    const builtin = builtinFlow[index];
    const key = builtinKey(builtin, index);
    const effective = builtin.active !== false && builtin.appliesTo.includes(requestKind);
    const stateLabel = effective ? "当前生效" : "当前旁路";
    inner = (
      <div
        key={key}
        className={`mw-stack-layer builtin ${builtin.overridden ? "overridden" : ""} ${effective ? "effective" : "bypass"} ${builtin.editable ? "editable" : ""} ${builtinSel === key ? "selected" : ""}`}
      >
        <button
          type="button"
          className={`mw-stack-head ${builtin.editable ? "" : "readonly"}`}
          title={`${builtin.name} · ${builtin.detail} · ${builtin.editable ? "参数可修改，内置层不可移除" : "内置层不可移除"}`}
          onClick={() => builtin.editable && onSelectBuiltin(key)}
          aria-disabled={!builtin.editable}
        >
          <em className="mw-stack-badge">内</em>
          <strong>{builtin.name}</strong>
          <small>内置 · {builtin.scope}{builtin.editable ? " · 参数可修改" : ""}{builtin.overridden ? " · Route 覆盖" : ""}</small>
          <em className={`mw-stack-state ${effective ? "active" : "bypass"}`}>{stateLabel}</em>
        </button>
        <small className="mw-stack-builtin-detail">{builtin.detail}</small>
        <div className="mw-stack-inner">{inner}</div>
      </div>
    );
  }
  return <div className="mw-stack">{inner}</div>;
}

function BuiltinParameterEditor({
  builtin,
  overrides,
  onChange,
}: {
  builtin: BuiltinMiddlewareView;
  overrides: BuiltinMiddlewareOverrides | undefined;
  onChange: (value: BuiltinMiddlewareOverrides | undefined) => void;
}) {
  const update = (group: keyof BuiltinMiddlewareOverrides, field: string, raw: string, numeric = false) => {
    onChange(setBuiltinMiddlewareOverride(overrides, group, field, raw, numeric));
  };
  const group: keyof BuiltinMiddlewareOverrides = builtin.name === "Timeout"
    ? "timeout"
    : builtin.name === "Admission"
      ? "admission"
      : builtin.name === "StreamTimeout"
        ? "stream_timeout"
        : "write_timeout";

  return (
    <div className="mw-builtin-editor">
      <div className="field-label-row">
        <div>
          <div className="field-label">{builtin.name} · Route 参数</div>
          <small className="muted">{builtin.detail}。参数可修改，但这个内置层不能移除或排序。</small>
        </div>
        <button type="button" className="btn small ghost" onClick={() => onChange(clearBuiltinMiddlewareOverride(overrides, group))}>
          {group === "admission" ? "禁用 Route 限制" : "恢复继承"}
        </button>
      </div>
      {group === "timeout" && (
        <Field label="maximum_duration" hint="仅普通 HTTP；留空继承 request.maximum_duration">
          <input value={overrides?.timeout?.maximum_duration || ""} placeholder="继承全局" onChange={(event) => update("timeout", "maximum_duration", event.target.value)} />
        </Field>
      )}
      {group === "admission" && (
        <Field label="max_in_flight" hint="所有请求类型；留空表示不增加 Route 独立并发闸门">
          <input type="number" min={1} value={overrides?.admission?.max_in_flight ?? ""} placeholder="未启用" onChange={(event) => update("admission", "max_in_flight", event.target.value, true)} />
        </Field>
      )}
      {group === "stream_timeout" && (
        <div className="grid-2">
          <Field label="max_duration" hint="仅 SSE / WebSocket；留空继承 stream.max_duration">
            <input value={overrides?.stream_timeout?.max_duration || ""} placeholder="继承全局" onChange={(event) => update("stream_timeout", "max_duration", event.target.value)} />
          </Field>
          <Field label="idle_timeout" hint="仅 SSE / WebSocket；留空继承 stream.idle_timeout">
            <input value={overrides?.stream_timeout?.idle_timeout || ""} placeholder="继承全局" onChange={(event) => update("stream_timeout", "idle_timeout", event.target.value)} />
          </Field>
        </div>
      )}
      {group === "write_timeout" && (
        <Field label="timeout" hint="普通 HTTP 响应写入 deadline；SSE / WebSocket 会清除该 deadline">
          <input value={overrides?.write_timeout?.timeout || ""} placeholder="继承 server.write_timeout" onChange={(event) => update("write_timeout", "timeout", event.target.value)} />
        </Field>
      )}
    </div>
  );
}

/**
 * 中间件管理弹窗：class（后端内置静态类型）→ instance（具名实例，可改参）
 * → flow（当前节点按序选用）。弹窗打开时由父组件建立草稿快照；确认保留
 * 本次修改，取消、关闭、点击遮罩或按 Escape 都恢复快照。
 */
export function MiddlewareManagerModal({
  title,
  scope,
  coreLabel,
  instances,
  catalog,
  flow,
  builtinFlow = [],
  builtinOverrides,
  onFlowChange,
  onBuiltinOverridesChange,
  onInstantiate,
  onUpdateInstance,
  onDeleteInstance,
  onRenameInstance,
  onConfirm,
  onCancel,
  notify,
}: {
  title: string;
  scope: MiddlewareScopeFilter;
  coreLabel: string;
  instances: Record<string, Middleware>;
  catalog: MiddlewareCapability[];
  flow: string[];
  builtinFlow?: BuiltinMiddlewareView[];
  builtinOverrides?: BuiltinMiddlewareOverrides;
  onFlowChange: (flow: string[]) => void;
  onBuiltinOverridesChange?: (value: BuiltinMiddlewareOverrides | undefined) => void;
  onInstantiate: (type: string) => string | undefined;
  onUpdateInstance: (name: string, def: Middleware) => void;
  onDeleteInstance: (name: string) => void;
  onRenameInstance: (oldName: string, newName: string) => string | undefined;
  onConfirm: () => void;
  onCancel: () => void;
  notify: (msg: string) => void;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const [tab, setTab] = useState<Tab>("flow");
  const [selected, setSelected] = useState<string | null>(null);
  const [chainSel, setChainSel] = useState<string | null>(null);
  const [builtinSel, setBuiltinSel] = useState<string | null>(null);
  const [requestKind, setRequestKind] = useState<BuiltinRequestKind>("http");
  const [nameDraft, setNameDraft] = useState("");
  const [renameError, setRenameError] = useState("");
  useDialogLifecycle(dialogRef, onCancel);

  const names = Object.keys(instances);
  useEffect(() => {
    if (selected && !instances[selected]) {
      setSelected(null);
    }
  }, [instances, selected]);
  useEffect(() => {
    if (chainSel && !flow.includes(chainSel)) {
      setChainSel(null);
    }
  }, [flow, chainSel]);
  function instantiate(type: string, klass: MiddlewareCapability) {
    if (!klass.scopes.includes(scope)) {
      notify(`${klass.label} 不能用于 ${scope === "route" ? "Route" : "Service"}。`);
      return;
    }
    const name = onInstantiate(type);
    if (!name) return;
    if (!flow.includes(name)) onFlowChange([...flow, name]);
    setTab("flow");
  }

  function move(index: number, offset: number) {
    const next = index + offset;
    if (next < 0 || next >= flow.length) return;
    const list = [...flow];
    [list[index], list[next]] = [list[next], list[index]];
    onFlowChange(list);
  }

  function commitRename() {
    if (!selected) return;
    const error = onRenameInstance(selected, nameDraft);
    if (error) {
      setRenameError(error);
      return;
    }
    setRenameError("");
    setSelected(nameDraft.trim());
    onFlowChange(flow.map((m) => (m === selected ? nameDraft.trim() : m)));
  }

  const available = names.filter((name) => !flow.includes(name) && isFlowCompatible(instances[name], scope, catalog));
  const selectedDef = selected ? instances[selected] : undefined;
  const selectedBuiltin = builtinFlow.find((builtin, index) => builtinKey(builtin, index) === builtinSel);

  return (
    <div className="backdrop" onMouseDown={onCancel}>
      <div ref={dialogRef} className="mw-modal" role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} onMouseDown={(e) => e.stopPropagation()}>
        <div className="drawer-head">
          <div>
            <div className="eyebrow">MIDDLEWARE</div>
            <h3>{title}</h3>
          </div>
          <button type="button" className="icon-button" onClick={onCancel} aria-label="关闭并撤销">×</button>
        </div>
        <div className="mw-modal-body">
          <nav className="mw-tabs">
            {([
              ["flow", `Flow（内置 ${builtinFlow.length} · 配置 ${flow.length}）`],
              ["instance", `Instance（${names.length}）`],
              ["class", `Class（${catalog.length}）`],
            ] as [Tab, string][]).map(([id, label]) => (
              <button key={id} type="button" className={tab === id ? "active" : ""} onClick={() => setTab(id)}>
                {label}
              </button>
            ))}
            <p className="muted">实例全局共享；确认后写入当前配置草稿，取消会撤销本次全部修改。</p>
          </nav>
          <div className="mw-tab-panel">
            {tab === "flow" && (
              <div className="mw-flow">
                <p className="muted">洋葱模型：外层先执行请求逻辑再调内层，响应按相反方向返回。内置层不可移除或排序；带“参数可修改”的内置层可以点击编辑。灰色虚线层表示当前请求类型会旁路。</p>
                {builtinFlow.length > 0 && (
                  <div className="mw-request-kind" role="group" aria-label="请求类型">
                    <span>查看实际链路</span>
                    {([['http', '普通 HTTP'], ['sse', 'SSE'], ['websocket', 'WebSocket']] as [BuiltinRequestKind, string][]).map(([kind, label]) => (
                      <button key={kind} type="button" className={requestKind === kind ? "active" : ""} onClick={() => setRequestKind(kind)}>{label}</button>
                    ))}
                  </div>
                )}
                {flow.length === 0 && builtinFlow.length === 0 ? (
                  <div className="mw-stack-core">直达{coreLabel}（未挂载中间件）</div>
                ) : (
                  <>
                    <StackView
                      flow={flow}
                      builtinFlow={builtinFlow}
                      instances={instances}
                      catalog={catalog}
                      scope={scope}
                      coreLabel={coreLabel}
                      chainSel={chainSel}
                      builtinSel={builtinSel}
                      requestKind={requestKind}
                      onSelect={(name) => {
                        setBuiltinSel(null);
                        setChainSel((cur) => (cur === name ? null : name));
                      }}
                      onSelectBuiltin={(key) => {
                        setChainSel(null);
                        setBuiltinSel((current) => current === key ? null : key);
                      }}
                    />
                    {selectedBuiltin?.editable && onBuiltinOverridesChange && (
                      <BuiltinParameterEditor builtin={selectedBuiltin} overrides={builtinOverrides} onChange={onBuiltinOverridesChange} />
                    )}
                    {chainSel && flow.includes(chainSel) && (
                      <div className="mw-chain-actions">
                        <strong>{chainSel}</strong>
                        <button type="button" disabled={flow.indexOf(chainSel) === 0} onClick={() => move(flow.indexOf(chainSel), -1)}>← 外移</button>
                        <button type="button" disabled={flow.indexOf(chainSel) === flow.length - 1} onClick={() => move(flow.indexOf(chainSel), 1)}>内移 →</button>
                        <button type="button" onClick={() => { setSelected(chainSel); setNameDraft(chainSel); setRenameError(""); setTab("instance"); }}>⚙ 参数</button>
                        <button type="button" className="danger" onClick={() => onFlowChange(flow.filter((m) => m !== chainSel))}>× 移除</button>
                      </div>
                    )}
                  </>
                )}
                <div className="field-label">可选用（{scope === "route" ? "Route 兼容" : "Service 兼容"}）</div>
                {available.length === 0 && <small className="muted">没有可用的实例，去 Instance 新建或调整作用域。</small>}
                {available.map((name) => (
                  <div className="mw-order-row" key={name}>
                    <span><strong>{name}</strong><small>{instanceLabel(instances[name], catalog)}</small></span>
                    <span className="mw-order-actions">
                      <button type="button" className="btn small" onClick={() => onFlowChange([...flow, name])}>＋ 选用</button>
                    </span>
                  </div>
                ))}
              </div>
            )}
            {tab === "instance" && (
              <div className="mw-instance">
                <div className="mw-instance-list">
                  {names.length === 0 && <p className="muted">还没有实例，去 Class 页实例化。</p>}
                  {names.map((name) => (
                    <button
                      key={name}
                      type="button"
                      className={`mw-instance-item ${selected === name ? "active" : ""}`}
                      onClick={() => { setSelected(name); setNameDraft(name); setRenameError(""); }}
                    >
                      <strong>{name}</strong>
                      <small>{instanceLabel(instances[name], catalog)}</small>
                      {flow.includes(name) && <em className="badge">flow 中</em>}
                    </button>
                  ))}
                </div>
                <div className="mw-instance-editor">
                  {!selected || !selectedDef ? (
                    <p className="muted">左侧选择一个实例编辑参数；改名后所有引用自动同步。</p>
                  ) : (
                    <>
                      <Field label="实例名称" hint="改名会自动同步所有 Route 与 Service 的引用" error={renameError}>
                        <div className="name-row">
                          <input value={nameDraft} onChange={(e) => setNameDraft(e.target.value)} />
                          <button type="button" className="btn small" disabled={nameDraft.trim() === selected} onClick={commitRename}>改名</button>
                        </div>
                      </Field>
                      <MiddlewareDefForm value={selectedDef} catalog={catalog} onChange={(def) => onUpdateInstance(selected, def)} />
                      <button type="button" className="btn small danger" onClick={() => { onDeleteInstance(selected); }}>删除实例</button>
                    </>
                  )}
                </div>
              </div>
            )}
            {tab === "class" && (
              <div className="mw-classes">
                {catalog.map((klass) => {
                  const usable = klass.scopes.includes(scope);
                  return (
                    <div className={`mw-class-card ${usable ? "" : "disabled"}`} key={klass.type}>
                      <div>
                        <strong>{klass.label}</strong>
                        <p>{klass.description}</p>
                        <small className="mono">{klass.fields.map((field) => field.name).join(" · ") || "无参数"}</small>
                        <div className="mw-class-scopes">
                          {klass.scopes.map((s) => <em className="badge" key={s}>{s}</em>)}
                        </div>
                      </div>
                      <button type="button" className="btn small primary" disabled={!usable} onClick={() => instantiate(klass.type, klass)}>
                        实例化并选用
                      </button>
                    </div>
                  );
                })}
                {!catalog.some((k) => k.scopes.includes(scope)) && <p className="muted">当前作用域无可用类型。</p>}
              </div>
            )}
          </div>
        </div>
        <div className="drawer-foot">
          <button type="button" className="btn ghost" onClick={onCancel}>取消</button>
          <button type="button" className="btn primary" onClick={onConfirm}>确认修改</button>
        </div>
      </div>
    </div>
  );
}
