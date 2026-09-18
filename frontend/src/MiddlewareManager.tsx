import { useEffect, useState, type ReactNode } from "react";
import { MiddlewareDefForm, middlewareTypeOf, type MwType } from "./editors";
import { Field } from "./ui";
import type { Middleware } from "./types";

export type MiddlewareScopeFilter = "route" | "service";

export interface MiddlewareClass {
  type: MwType;
  label: string;
  desc: string;
  scopes: MiddlewareScopeFilter[];
  example: string;
}

export const MIDDLEWARE_CLASSES: MiddlewareClass[] = [
  { type: "buffer", label: "Buffer", desc: "缓存完整响应后再转发，适合小而完整的 API 响应。", scopes: ["route", "service"], example: "max_response_body_bytes: 1048576" },
  { type: "body_limit", label: "Body Limit", desc: "限制请求体大小，超限直接拒绝。", scopes: ["route", "service"], example: "max_bytes: 10485760" },
  { type: "in_flight", label: "In Flight", desc: "限制并发请求数，超限返回 503，不排队。", scopes: ["service"], example: "max_concurrent: 100" },
];

const CLASS_DEFAULTS: Record<MwType, Middleware> = {
  buffer: { buffer: { max_response_body_bytes: 1048576 } },
  body_limit: { body_limit: { max_bytes: 10485760 } },
  in_flight: { scope: "service", in_flight: { max_concurrent: 100 } },
};

export function classDefaults(type: MwType): Middleware {
  return JSON.parse(JSON.stringify(CLASS_DEFAULTS[type])) as Middleware;
}

/** 实例是否可被某作用域的 flow 引用（与后端 AllowsScope + in_flight 约束一致）。 */
export function isFlowCompatible(def: Middleware | undefined, scope: MiddlewareScopeFilter): boolean {
  if (!def) return false;
  if (scope === "route") return (!def.scope || def.scope === "route") && !def.in_flight;
  return !def.scope || def.scope === "service";
}

export function instanceLabel(def: Middleware | undefined): string {
  if (!def) return "未定义";
  const kind = def.buffer ? "buffer" : def.body_limit ? "body_limit" : def.in_flight ? "in_flight" : "未配置";
  const scope = !def.scope ? "共享" : def.scope === "route" ? "仅 Route" : "仅 Service";
  return `${kind} · ${scope}`;
}

function kindColor(def: Middleware | undefined): string {
  if (!def) return "#b91c1c";
  if (def.buffer) return "#536dfe";
  if (def.body_limit) return "#c2410c";
  if (def.in_flight) return "#7c3aed";
  return "#9aa1b3";
}

type Tab = "class" | "instance" | "flow";

/** 洋葱栈视图：flow[0] 在最外层，依次包裹，最内层是 Service / 上游。 */
function StackView({
  flow,
  instances,
  scope,
  coreLabel,
  chainSel,
  onSelect,
}: {
  flow: string[];
  instances: Record<string, Middleware>;
  scope: MiddlewareScopeFilter;
  coreLabel: string;
  chainSel: string | null;
  onSelect: (name: string) => void;
}) {
  let inner: ReactNode = <div className="mw-stack-core">{coreLabel}</div>;
  for (let index = flow.length - 1; index >= 0; index--) {
    const name = flow[index];
    const def = instances[name];
    const compatible = isFlowCompatible(def, scope);
    inner = (
      <div
        key={`${name}-${index}`}
        className={`mw-stack-layer ${chainSel === name ? "selected" : ""} ${compatible ? "" : "warn"}`}
        style={{ borderColor: kindColor(def) }}
      >
        <button
          type="button"
          className="mw-stack-head"
          title={`${name} · ${instanceLabel(def)}${compatible ? "" : "（已不兼容，请移除）"}`}
          onClick={() => onSelect(name)}
        >
          <em className="mw-stack-badge" style={{ background: kindColor(def) }}>{index + 1}</em>
          <strong>{name}</strong>
          <small className={compatible ? "" : "error-text"}>{instanceLabel(def)}</small>
        </button>
        <div className="mw-stack-inner">{inner}</div>
      </div>
    );
  }
  return <div className="mw-stack">{inner}</div>;
}

/**
 * 中间件管理弹窗：class（后端内置静态类型）→ instance（具名实例，可改参）
 * → flow（当前节点按序选用）。实例是全局共享的，修改即进入画布草稿
 * （顶栏保存），不受外层抽屉取消影响。
 */
export function MiddlewareManagerModal({
  title,
  scope,
  coreLabel,
  instances,
  flow,
  onFlowChange,
  onInstantiate,
  onUpdateInstance,
  onDeleteInstance,
  onRenameInstance,
  onClose,
  notify,
}: {
  title: string;
  scope: MiddlewareScopeFilter;
  coreLabel: string;
  instances: Record<string, Middleware>;
  flow: string[];
  onFlowChange: (flow: string[]) => void;
  onInstantiate: (type: MwType) => string | undefined;
  onUpdateInstance: (name: string, def: Middleware) => void;
  onDeleteInstance: (name: string) => void;
  onRenameInstance: (oldName: string, newName: string) => string | undefined;
  onClose: () => void;
  notify: (msg: string) => void;
}) {
  const [tab, setTab] = useState<Tab>("flow");
  const [selected, setSelected] = useState<string | null>(null);
  const [chainSel, setChainSel] = useState<string | null>(null);
  const [nameDraft, setNameDraft] = useState("");
  const [renameError, setRenameError] = useState("");

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

  function instantiate(type: MwType, klass: MiddlewareClass) {
    if (!klass.scopes.includes(scope)) {
      notify(`${klass.label} 不能用于 ${scope === "route" ? "Route" : "Service"}。`);
      return;
    }
    const name = onInstantiate(type);
    if (!name) return;
    if (!flow.includes(name)) onFlowChange([...flow, name]);
    setTab("flow");
    notify(`已实例化 ${name} 并加入执行顺序。`);
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

  const available = names.filter((name) => !flow.includes(name) && isFlowCompatible(instances[name], scope));
  const blocked = names.filter((name) => !flow.includes(name) && !isFlowCompatible(instances[name], scope));
  const selectedDef = selected ? instances[selected] : undefined;

  return (
    <div className="backdrop" onMouseDown={onClose}>
      <div className="mw-modal" role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.stopPropagation()}>
        <div className="drawer-head">
          <div>
            <div className="eyebrow">MIDDLEWARE</div>
            <h3>{title}</h3>
          </div>
          <button type="button" className="btn ghost" onClick={onClose} aria-label="关闭">×</button>
        </div>
        <div className="mw-modal-body">
          <nav className="mw-tabs">
            {([
              ["flow", `Flow（${flow.length}）`],
              ["instance", `Instance（${names.length}）`],
              ["class", "Class（3）"],
            ] as [Tab, string][]).map(([id, label]) => (
              <button key={id} type="button" className={tab === id ? "active" : ""} onClick={() => setTab(id)}>
                {label}
              </button>
            ))}
            <p className="muted">实例全局共享；改动即进入画布草稿，顶栏保存后生效。</p>
          </nav>
          <div className="mw-tab-panel">
            {tab === "flow" && (
              <div className="mw-flow">
                <p className="muted">洋葱模型：外层先执行请求逻辑再调内层，最内层是动作核心；响应按相反方向逐层返回。点击一层进行排序或移除。</p>
                {flow.length === 0 ? (
                  <div className="mw-stack-core">直达{coreLabel}（未挂载中间件）</div>
                ) : (
                  <>
                    <StackView
                      flow={flow}
                      instances={instances}
                      scope={scope}
                      coreLabel={coreLabel}
                      chainSel={chainSel}
                      onSelect={(name) => setChainSel((cur) => (cur === name ? null : name))}
                    />
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
                    <span><strong>{name}</strong><small>{instanceLabel(instances[name])}</small></span>
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
                      <small>{instanceLabel(instances[name])}</small>
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
                      <MiddlewareDefForm value={selectedDef} onChange={(def) => onUpdateInstance(selected, def)} />
                      <button type="button" className="btn small danger" onClick={() => { onDeleteInstance(selected); }}>删除实例</button>
                    </>
                  )}
                </div>
              </div>
            )}
            {tab === "class" && (
              <div className="mw-classes">
                {MIDDLEWARE_CLASSES.map((klass) => {
                  const usable = klass.scopes.includes(scope);
                  return (
                    <div className={`mw-class-card ${usable ? "" : "disabled"}`} key={klass.type}>
                      <div>
                        <strong>{klass.label}</strong>
                        <p>{klass.desc}</p>
                        <small className="mono">{klass.example}</small>
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
                {!MIDDLEWARE_CLASSES.some((k) => k.scopes.includes(scope)) && <p className="muted">当前作用域无可用类型。</p>}
              </div>
            )}
          </div>
        </div>
        <div className="drawer-foot">
          <button type="button" className="btn primary" onClick={onClose}>完成</button>
        </div>
      </div>
    </div>
  );
}
