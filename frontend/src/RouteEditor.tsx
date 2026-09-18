import { useMemo, useState } from "react";
import { limenNames, serviceNames } from "./model";
import { Drawer, Field } from "./ui";
import type { JanusConfig, Route } from "./types";

export type MatchRule = { type: "host" | "path" | "pathPrefix" | "method" | "protocol" | "header" | "query"; value: string; extra?: string };

function parseMatchExpression(expr: string): MatchRule[] {
  if (!expr) return [];
  const rules: MatchRule[] = [];
  const hostMatch = expr.match(/Host\(`([^`]+)`\)/);
  if (hostMatch) rules.push({ type: "host", value: hostMatch[1] });
  const pathMatch = expr.match(/Path\(`([^`]+)`\)/);
  if (pathMatch) rules.push({ type: "path", value: pathMatch[1] });
  const prefixMatch = expr.match(/PathPrefix\(`([^`]+)`\)/);
  if (prefixMatch) rules.push({ type: "pathPrefix", value: prefixMatch[1] });
  const methodMatch = expr.match(/Method\(`([^`]+)`\)/);
  if (methodMatch) rules.push({ type: "method", value: methodMatch[1] });
  const protocolMatch = expr.match(/Protocol\(`([^`]+)`\)/);
  if (protocolMatch) rules.push({ type: "protocol", value: protocolMatch[1] });
  return rules;
}

function buildMatchExpression(rules: MatchRule[]): string {
  const parts: string[] = [];
  for (const rule of rules) {
    switch (rule.type) {
      case "host": parts.push(`Host(\`${rule.value}\`)`); break;
      case "path": parts.push(`Path(\`${rule.value}\`)`); break;
      case "pathPrefix": parts.push(`PathPrefix(\`${rule.value}\`)`); break;
      case "method": parts.push(`Method(\`${rule.value}\`)`); break;
      case "protocol": parts.push(`Protocol(\`${rule.value}\`)`); break;
      case "header": if (rule.extra) parts.push(`Header(\`${rule.value}\`, \`${rule.extra}\`)`); break;
      case "query": if (rule.extra) parts.push(`Query(\`${rule.value}\`, \`${rule.extra}\`)`); break;
    }
  }
  return parts.join(" && ");
}
export function RouteEditor({ draft, value, isNew, onChange, onConfirm, onCancel, onClose, onDelete, onOpenMiddlewareManager }: {
  draft: JanusConfig; value: Route; isNew: boolean;
  onChange: (value: Route) => void; onConfirm: () => void; onCancel: () => void; onClose: () => void; onDelete?: () => void;
  onOpenMiddlewareManager?: () => void;
}) {
  const [showAdvanced, setShowAdvanced] = useState(false);
  const actionType = value.action?.redirect ? "redirect" : value.action?.respond ? "respond" : "forward";
  const services = serviceNames(draft);
  const limens = limenNames(draft);
  const matchRules = useMemo(() => parseMatchExpression(value.match || ""), [value.match]);
  const set = (patch: Partial<Route>) => onChange({ ...value, ...patch });

  function setActionType(type: string) {
    if (type === "redirect") set({ action: { redirect: { status: 308, location: "https://example.com" } } });
    else if (type === "respond") set({ action: { respond: { status: 200, body: "ok\n" } } });
    else set({ action: { forward: { service: value.action?.forward?.service || services[0] || "" } } });
  }

  function updateMatchRules(rules: MatchRule[]) {
    const expr = buildMatchExpression(rules);
    set({ match: expr || undefined });
  }

  function addRule(type: MatchRule["type"]) {
    const defaults: Record<MatchRule["type"], string> = {
      host: "api.example.com", path: "/api/users", pathPrefix: "/api",
      method: "GET", protocol: "http", header: "", query: "",
    };
    updateMatchRules([...matchRules, { type, value: defaults[type] }]);
  }

  function updateRule(index: number, patch: Partial<MatchRule>) {
    const next = [...matchRules];
    next[index] = { ...next[index], ...patch };
    updateMatchRules(next);
  }

  function removeRule(index: number) {
    updateMatchRules(matchRules.filter((_, i) => i !== index));
  }

  const matchBuilder = (
    <div className="match-builder">
      <div className="match-builder-header">
        <div className="field-label">匹配规则</div>
        <button type="button" className="btn small ghost" onClick={() => setShowAdvanced(!showAdvanced)}>
          {showAdvanced ? "可视化编辑" : "高级编辑"}
        </button>
      </div>
      {showAdvanced ? (
        <Field label="Match 表达式" hint="支持 Host, Path, PathPrefix, Method, Protocol, Header, Query">
          <textarea rows={3} value={value.match || ""}
            onChange={(e) => set({ match: e.target.value || undefined })}
            placeholder='PathPrefix(`/api`) && Protocol(`http`)' className="mono" />
        </Field>
      ) : (
        <div className="match-rules">
          {matchRules.length === 0 && <div className="match-empty"><p>暂无匹配规则，默认匹配所有请求</p></div>}
          {matchRules.map((rule, index) => (
            <div key={index} className="match-rule">
              <select value={rule.type} onChange={(e) => updateRule(index, { type: e.target.value as MatchRule["type"] })}>
                <option value="host">Host</option><option value="path">Path</option>
                <option value="pathPrefix">PathPrefix</option><option value="method">Method</option>
                <option value="protocol">Protocol</option><option value="header">Header</option>
                <option value="query">Query</option>
              </select>
              <input value={rule.value} onChange={(e) => updateRule(index, { value: e.target.value })} placeholder="值" />
              {(rule.type === "header" || rule.type === "query") && (
                <input value={rule.extra || ""} onChange={(e) => updateRule(index, { extra: e.target.value })} placeholder={rule.type === "header" ? "Header 值" : "Query 值"} />
              )}
              <button type="button" className="btn small danger" onClick={() => removeRule(index)}>×</button>
            </div>
          ))}
          <div className="match-add">
            <button type="button" className="btn small" onClick={() => addRule("pathPrefix")}>＋ PathPrefix</button>
            <button type="button" className="btn small" onClick={() => addRule("host")}>＋ Host</button>
            <button type="button" className="btn small" onClick={() => addRule("method")}>＋ Method</button>
            <button type="button" className="btn small" onClick={() => addRule("protocol")}>＋ Protocol</button>
          </div>
          {value.match && <div className="match-preview mono">{value.match}</div>}
        </div>
      )}
    </div>
  );

  const actionFields = (
    <>
      <Field label="动作 Action">
        <select value={actionType} onChange={(e) => setActionType(e.target.value)}>
          <option value="forward">Forward 到 Service</option>
          <option value="redirect">Redirect</option>
          <option value="respond">Direct Response</option>
        </select>
      </Field>
      {actionType === "forward" && (
        <Field label="目标 Service">
          <select value={value.action?.forward?.service || ""} onChange={(e) => set({ action: { forward: { service: e.target.value } } })}>
            <option value="">请选择 Service</option>
            {services.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </Field>
      )}
      {actionType === "redirect" && (
        <div className="grid-2">
          <Field label="状态码">
            <input type="number" min={300} max={399} value={value.action?.redirect?.status ?? 308}
              onChange={(e) => set({ action: { redirect: { location: value.action?.redirect?.location || "", status: Number(e.target.value) || 308 } } })} />
          </Field>
          <Field label="Location">
            <input value={value.action?.redirect?.location || ""}
              onChange={(e) => set({ action: { redirect: { location: e.target.value, status: value.action?.redirect?.status || 308 } } })} />
          </Field>
        </div>
      )}
      {actionType === "respond" && (
        <>
          <Field label="状态码">
            <input type="number" min={100} max={599} value={value.action?.respond?.status ?? 200}
              onChange={(e) => set({ action: { respond: { ...value.action?.respond, status: Number(e.target.value) || 200 } } })} />
          </Field>
          <Field label="Body">
            <textarea rows={3} value={value.action?.respond?.body || ""}
              onChange={(e) => set({ action: { respond: { ...value.action?.respond, body: e.target.value } } })} />
          </Field>
        </>
      )}
    </>
  );

  const middlewareSection = (
    <div className="check-group">
      <div className="field-label-row">
        <div className="field-label">中间件（{(value.middlewares || []).length}）</div>
        {onOpenMiddlewareManager && <button type="button" className="btn small primary" onClick={onOpenMiddlewareManager}>管理中间件</button>}
      </div>
      {(value.middlewares || []).length === 0 ? (
        <small className="muted">尚未挂载。在管理弹窗中从 class 实例化、再到 flow 里选用编排。</small>
      ) : (
        <div className="mw-flow-preview">
          {(value.middlewares || []).map((name, index, list) => (
            <span className="mw-flow-chip" key={`${name}-${index}`}>
              <em>{index + 1}</em><strong>{name}</strong>{!draft.middlewares?.[name] && <small className="error-text">未定义</small>}
            </span>
          ))}
        </div>
      )}
    </div>
  );

  return (
    <Drawer
      title={isNew ? "新建 Route" : `编辑 ${value.name}`}
      subtitle="执行顺序：匹配 → 中间件 → 动作"
      onClose={onClose} onConfirm={onConfirm} onCancel={onCancel} onDelete={onDelete}
    >
      <div className="grid-2">
        <Field label="名称" hint={isNew ? "创建后不可改名" : "名称不可修改"}>
          <input value={value.name} onChange={(e) => set({ name: e.target.value })} disabled={!isNew} />
        </Field>
        <Field label="入口 Limen">
          <select value={value.limen || ""} onChange={(e) => set({ limen: e.target.value || undefined })}>
            <option value="">默认入口</option>
            {limens.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </Field>
      </div>
      <Field label="优先级" hint="数字越大优先级越高，默认 0">
        <input type="number" min={0} value={value.priority ?? 0} onChange={(e) => set({ priority: Number(e.target.value) || 0 })} />
      </Field>
      {matchBuilder}
      {middlewareSection}
      {actionFields}
    </Drawer>
  );
}
