import { useEffect, useRef, useState, type ReactNode } from "react";
import { MiddlewareDefForm, middlewareTypeOf } from "./editors";
import { Field } from "./ui";
import type { Middleware, MiddlewareCapability } from "./types";

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

/** 洋葱栈视图：flow[0] 在最外层，依次包裹，最内层是 Service / 上游。 */
function StackView({
  flow,
  instances,
  catalog,
  scope,
  coreLabel,
  chainSel,
  onSelect,
}: {
  flow: string[];
  instances: Record<string, Middleware>;
  catalog: MiddlewareCapability[];
  scope: MiddlewareScopeFilter;
  coreLabel: string;
  chainSel: string | null;
  onSelect: (name: string) => void;
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
  return <div className="mw-stack">{inner}</div>;
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
  onFlowChange,
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
  onFlowChange: (flow: string[]) => void;
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
  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      const dialogs = document.querySelectorAll<HTMLElement>('[role="dialog"]');
      if (dialogs[dialogs.length - 1] !== dialogRef.current) return;
      event.preventDefault();
      onCancel();
    };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [onCancel]);

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

  return (
    <div className="backdrop" onMouseDown={onCancel}>
      <div ref={dialogRef} className="mw-modal" role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.stopPropagation()}>
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
              ["flow", `Flow（${flow.length}）`],
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
                <p className="muted">洋葱模型：外层先执行请求逻辑再调内层，最内层是动作核心；响应按相反方向逐层返回。点击一层进行排序或移除。</p>
                {flow.length === 0 ? (
                  <div className="mw-stack-core">直达{coreLabel}（未挂载中间件）</div>
                ) : (
                  <>
                    <StackView
                      flow={flow}
                      instances={instances}
                      catalog={catalog}
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
