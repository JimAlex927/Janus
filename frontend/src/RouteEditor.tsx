import { useEffect, useMemo, useState } from "react";
import { builtinMiddlewareViews } from "./builtinMiddleware";
import { limenNames, serviceNames } from "./model";
import { Drawer, Field } from "./ui";
import type { JanusConfig, Route } from "./types";

export type MatchRule = { type: "host" | "path" | "pathPrefix" | "pathPattern" | "method" | "protocol" | "header" | "query"; value: string; extra?: string };

function parseMatchExpression(expr: string): { rules: MatchRule[]; safe: boolean } {
  if (!expr.trim()) return { rules: [], safe: true };
  const rules: MatchRule[] = [];
  const names: Record<string, MatchRule["type"]> = {
    Host: "host", Path: "path", PathPrefix: "pathPrefix", PathPattern: "pathPattern", Method: "method",
    Protocol: "protocol", Header: "header", Query: "query",
  };
  const source = expr.trim();
  const token = /\s*(Host|Path|PathPrefix|PathPattern|Method|Protocol|Header|Query)\(\s*`([^`]*)`(?:\s*,\s*`([^`]*)`)?\s*\)\s*/y;
  let index = 0;
  while (index < source.length) {
    token.lastIndex = index;
    const match = token.exec(source);
    if (!match || match.index !== index) return { rules: [], safe: false };
    const type = names[match[1]];
    const needsExtra = type === "header" || type === "query";
    if (needsExtra !== (match[3] !== undefined)) return { rules: [], safe: false };
    rules.push({ type, value: match[2], ...(needsExtra ? { extra: match[3] } : {}) });
    index = token.lastIndex;
    if (index === source.length) break;
    const separator = /^\s*&&\s*/.exec(source.slice(index));
    if (!separator) return { rules: [], safe: false };
    index += separator[0].length;
    if (index === source.length) return { rules: [], safe: false };
  }
  return { rules, safe: true };
}

function buildMatchExpression(rules: MatchRule[]): string {
  const parts: string[] = [];
  for (const rule of rules) {
    switch (rule.type) {
      case "host": parts.push(`Host(\`${rule.value}\`)`); break;
      case "path": parts.push(`Path(\`${rule.value}\`)`); break;
      case "pathPrefix": parts.push(`PathPrefix(\`${rule.value}\`)`); break;
      case "pathPattern": parts.push(`PathPattern(\`${rule.value}\`)`); break;
      case "method": parts.push(`Method(\`${rule.value}\`)`); break;
      case "protocol": parts.push(`Protocol(\`${rule.value}\`)`); break;
      case "header": if (rule.extra) parts.push(`Header(\`${rule.value}\`, \`${rule.extra}\`)`); break;
      case "query": if (rule.extra) parts.push(`Query(\`${rule.value}\`, \`${rule.extra}\`)`); break;
    }
  }
  return parts.join(" && ");
}

type RouteEditorSection = "basic" | "match" | "middleware" | "action";

export function RouteEditor({ draft, value, isNew, onChange, onConfirm, onCancel, onClose, onDelete, onOpenMiddlewareManager }: {
  draft: JanusConfig; value: Route; isNew: boolean;
  onChange: (value: Route) => void; onConfirm: () => void; onCancel: () => void; onClose: () => void; onDelete?: () => void;
  onOpenMiddlewareManager?: () => void;
}) {
  const [section, setSection] = useState<RouteEditorSection>("basic");
  const [showAdvanced, setShowAdvanced] = useState(() => !parseMatchExpression(value.match || "").safe);
  const actionType = value.action?.redirect ? "redirect" : value.action?.respond ? "respond" : value.action?.static ? "static" : "forward";
  const services = serviceNames(draft);
  const limens = limenNames(draft);
  const parsedMatch = useMemo(() => parseMatchExpression(value.match || ""), [value.match]);
  const matchRules = parsedMatch.rules;
  useEffect(() => {
    if (!parsedMatch.safe) setShowAdvanced(true);
  }, [parsedMatch.safe]);
  const set = (patch: Partial<Route>) => onChange({ ...value, ...patch });

  function setActionType(type: string) {
    if (type === "redirect") set({ action: { redirect: { status: 308, location: "https://example.com" } } });
    else if (type === "respond") set({ action: { respond: { status: 200, body: "ok\n" } } });
    else if (type === "static") set({ action: { static: { root: "", index: "index.html", spa_fallback: true } } });
    else set({ action: { forward: { service: value.action?.forward?.service || services[0] || "" } } });
  }

  function updateMatchRules(rules: MatchRule[]) {
    const expr = buildMatchExpression(rules);
    set({ match: expr || undefined });
  }

  function addRule(type: MatchRule["type"]) {
    const defaults: Record<MatchRule["type"], string> = {
      host: "api.example.com", path: "/api/users", pathPrefix: "/api", pathPattern: "/abcd/abc*",
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
        <button type="button" className="btn small ghost" disabled={showAdvanced && !parsedMatch.safe} onClick={() => setShowAdvanced(!showAdvanced)}>
          {showAdvanced ? (parsedMatch.safe ? "可视化编辑" : "当前表达式仅支持高级编辑") : "高级编辑"}
        </button>
      </div>
      {showAdvanced ? (
        <Field label="Match 表达式" hint="支持完整 DSL；含 OR、NOT、括号或其他复杂组合时保留在高级模式，避免无损转换失败">
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
                <option value="pathPrefix">PathPrefix</option><option value="pathPattern">PathPattern (*)</option><option value="method">Method</option>
                <option value="protocol">Protocol</option><option value="header">Header</option>
                <option value="query">Query</option>
              </select>
              <input value={rule.value} onChange={(e) => updateRule(index, { value: e.target.value })} placeholder="值" />
              {(rule.type === "header" || rule.type === "query") && (
                <input value={rule.extra || ""} onChange={(e) => updateRule(index, { extra: e.target.value })} placeholder={rule.type === "header" ? "Header 值" : "Query 值"} />
              )}
              {rule.type !== "header" && rule.type !== "query" && <span className="match-rule-spacer" />}
              <button type="button" className="btn small danger" onClick={() => removeRule(index)}>×</button>
            </div>
          ))}
          <div className="match-add">
            <span>＋ 添加匹配条件</span>
            <select value="" onChange={(event) => event.target.value && addRule(event.target.value as MatchRule["type"])}>
              <option value="">选择条件类型…</option>
              <option value="pathPrefix">PathPrefix</option><option value="pathPattern">PathPattern (*)</option>
              <option value="path">Path</option><option value="host">Host</option><option value="method">Method</option>
              <option value="protocol">Protocol</option><option value="header">Header</option><option value="query">Query</option>
            </select>
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
          <option value="static">Static 静态文件</option>
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
      {actionType === "static" && (
        <>
          <Field label="静态根目录 Root" hint="必须是绝对路径；网关进程必须拥有读取权限">
            <input value={value.action?.static?.root || ""} placeholder="/srv/janus/web/dist" onChange={(e) => set({ action: { static: { ...value.action?.static, root: e.target.value } } })} />
          </Field>
          <div className="grid-2">
            <Field label="默认文件 Index">
              <input value={value.action?.static?.index || "index.html"} placeholder="index.html" onChange={(e) => set({ action: { static: { ...value.action?.static, root: value.action?.static?.root || "", index: e.target.value } } })} />
            </Field>
            <Field label="Cache-Control" hint="可选响应缓存策略">
              <input value={value.action?.static?.cache_control || ""} placeholder="public, max-age=3600" onChange={(e) => set({ action: { static: { ...value.action?.static, root: value.action?.static?.root || "", cache_control: e.target.value } } })} />
            </Field>
          </div>
          <div className="check-group">
            <label className="check-row">
              <span className="check-row-main"><input type="checkbox" checked={value.action?.static?.spa_fallback ?? true} onChange={(e) => set({ action: { static: { ...value.action?.static, root: value.action?.static?.root || "", spa_fallback: e.target.checked } } })} /><span><strong>SPA fallback</strong><small>找不到普通文件时回退到 Index，适用于 React、Vue 等前端路由。</small></span></span>
            </label>
            <label className="check-row">
              <span className="check-row-main"><input type="checkbox" checked={value.action?.static?.directory_listing ?? false} onChange={(e) => set({ action: { static: { ...value.action?.static, root: value.action?.static?.root || "", directory_listing: e.target.checked } } })} /><span><strong>目录浏览</strong><small>默认关闭；只建议用于受控的内部文件分发。</small></span></span>
            </label>
          </div>
        </>
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

  const builtinOverrides = value.builtin_middleware_overrides;
  const overrideCount = builtinOverrides
    ? Object.values(builtinOverrides).reduce((count, group) => count + (group ? Object.values(group).filter((item) => item !== undefined).length : 0), 0)
    : 0;
  const configuredMiddlewares = value.middlewares || [];
  const builtinCount = builtinMiddlewareViews(draft, value).length;
  const sectionItems: Array<{ id: RouteEditorSection; label: string; meta: string }> = [
    { id: "basic", label: "基础", meta: value.limen || "默认入口" },
    { id: "match", label: "匹配", meta: matchRules.length > 0 ? `${matchRules.length} 条规则` : value.match ? "高级表达式" : "全部请求" },
    { id: "middleware", label: "中间件", meta: `${configuredMiddlewares.length} 个配置层${overrideCount > 0 ? ` · ${overrideCount} 处覆盖` : ""}` },
    { id: "action", label: "动作", meta: actionType === "forward" ? "Forward" : actionType === "static" ? "Static" : actionType === "redirect" ? "Redirect" : "Response" },
  ];

  return (
    <Drawer
      title={isNew ? "新建 Route" : `编辑 ${value.name}`}
      subtitle="分区编辑 Route；确认前所有修改都只保存在当前草稿。"
      className="route-editor-drawer"
      onClose={onClose} onConfirm={onConfirm} onCancel={onCancel} onDelete={onDelete}
    >
      <div className="route-editor-layout">
        <nav className="route-editor-nav" aria-label="Route 编辑分区">
          {sectionItems.map((item, index) => (
            <button key={item.id} type="button" className={section === item.id ? "active" : ""} onClick={() => setSection(item.id)}>
              <em>{index + 1}</em><span><strong>{item.label}</strong><small>{item.meta}</small></span>
            </button>
          ))}
        </nav>
        <section className="route-editor-panel">
          <header>
            <div><span>STEP {sectionItems.findIndex((item) => item.id === section) + 1}</span><h4>{sectionItems.find((item) => item.id === section)?.label}</h4></div>
            <small>匹配 → 中间件 → 动作</small>
          </header>
          {section === "basic" && (
            <div className="route-editor-fields">
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
              <Field label="优先级" hint="数字越大越先匹配，默认 0">
                <input type="number" min={0} value={value.priority ?? 0} onChange={(e) => set({ priority: Number(e.target.value) || 0 })} />
              </Field>
            </div>
          )}
          {section === "match" && matchBuilder}
          {section === "middleware" && (
            <div className="route-middleware-overview">
              <div className="route-middleware-stats">
                <span><strong>固定内置层</strong><b>{builtinCount}</b><small>不可移除，可覆盖参数</small></span>
                <span><strong>配置中间件</strong><b>{configuredMiddlewares.length}</b><small>可排序、编辑和移除</small></span>
                <span><strong>Route 覆盖</strong><b>{overrideCount}</b><small>未覆盖参数继承全局</small></span>
              </div>
              {configuredMiddlewares.length > 0 ? (
                <div className="mw-flow-preview">
                  {configuredMiddlewares.map((name, index) => (
                    <span className="mw-flow-chip" key={`${name}-${index}`}>
                      <em>{index + 1}</em><strong>{name}</strong>{!draft.middlewares?.[name] && <small className="error-text">未定义</small>}
                    </span>
                  ))}
                </div>
              ) : <p className="route-editor-empty">当前没有配置型中间件，请在执行链管理器中添加。</p>}
              {onOpenMiddlewareManager && <button type="button" className="btn primary route-open-middleware" onClick={onOpenMiddlewareManager}>打开中间件执行链 →</button>}
            </div>
          )}
          {section === "action" && <div className="route-editor-fields">{actionFields}</div>}
        </section>
      </div>
    </Drawer>
  );
}
