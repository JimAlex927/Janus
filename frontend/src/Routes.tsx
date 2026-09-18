import { useMemo, useState } from "react";
import { limenNames, routeActionLabel, routeMatchLabel, serviceNames, uniqueName } from "./model";
import { simulateRequest } from "./simulate";
import { Drawer, Empty, Field } from "./ui";
import type { ConfigStore } from "./useConfig";
import type { JanusConfig, Route } from "./types";

function blankRoute(taken: string[], limen: string): Route {
  return {
    name: uniqueName("route", taken),
    limen,
    match: "PathPrefix(`/api`)",
    action: { forward: { service: "" } },
    middlewares: [],
  };
}

export function RoutesPage({ store }: { store: ConfigStore }) {
  const draft = store.draft;
  const [limenFilter, setLimenFilter] = useState("all");
  const [keyword, setKeyword] = useState("");
  const [editing, setEditing] = useState<{ value: Route; isNew: boolean } | null>(null);

  const routes = useMemo(() => {
    const list = draft?.routes || [];
    return list.filter((r) => {
      if (limenFilter !== "all" && (r.limen || "") !== limenFilter) return false;
      if (keyword && !`${r.name} ${r.match || ""}`.toLowerCase().includes(keyword.toLowerCase())) return false;
      return true;
    });
  }, [draft, limenFilter, keyword]);

  if (!draft) return <Empty text="正在加载配置…" />;

  function startCreate() {
    const taken = (draft?.routes || []).map((r) => r.name);
    const limen = limenFilter !== "all" ? limenFilter : limenNames(draft)[0] || "";
    setEditing({ value: blankRoute(taken, limen), isNew: true });
  }

  function confirmEdit() {
    if (!editing) return;
    const value = { ...editing.value, name: editing.value.name.trim() };
    if (!value.name) {
      store.setMessage("Route 名称不能为空。");
      return;
    }
    store.update((prev) => {
      const list = [...(prev.routes || [])];
      if (editing.isNew) {
        if (list.some((r) => r.name === value.name)) {
          store.setMessage(`Route 名称已存在：${value.name}`);
          return prev;
        }
        list.push(value);
      } else {
        // 编辑时名称只读，直接按原名替换，避免改名破坏引用
        const index = list.findIndex((r) => r.name === value.name);
        if (index < 0) {
          store.setMessage(`找不到 Route：${value.name}`);
          return prev;
        }
        list[index] = value;
      }
      return { ...prev, routes: list };
    });
    setEditing(null);
  }

  function remove(name: string) {
    if (!window.confirm(`删除 Route ${name}？该操作只改本地草稿，发布后才生效。`)) return;
    store.update((prev) => ({ ...prev, routes: (prev.routes || []).filter((r) => r.name !== name) }));
    setEditing(null);
  }

  return (
    <section className="page">
      <div className="toolbar">
        <div className="toolbar-filters">
          <select value={limenFilter} onChange={(e) => setLimenFilter(e.target.value)} aria-label="按入口过滤">
            <option value="all">全部入口</option>
            {limenNames(draft).map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
          <input value={keyword} onChange={(e) => setKeyword(e.target.value)} placeholder="搜索名称或匹配规则" />
        </div>
        <button type="button" className="btn primary" onClick={startCreate}>
          ＋ 新建 Route
        </button>
      </div>
      {routes.length === 0 ? (
        <Empty text="当前筛选下没有 Route，点击右上角创建。" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>入口</th>
                <th>匹配</th>
                <th>动作</th>
                <th>中间件</th>
                <th className="col-action">操作</th>
              </tr>
            </thead>
            <tbody>
              {routes.map((route) => (
                <tr key={route.name}>
                  <td>
                    <strong>{route.name}</strong>
                    {route.priority ? <small className="muted"> · prio {route.priority}</small> : null}
                  </td>
                  <td>{route.limen || <span className="muted">—</span>}</td>
                  <td className="mono">{routeMatchLabel(route)}</td>
                  <td>{routeActionLabel(route)}</td>
                  <td>{(route.middlewares || []).join("、") || <span className="muted">—</span>}</td>
                  <td className="col-action">
                    <button
                      type="button"
                      className="btn small"
                      onClick={() => setEditing({ value: JSON.parse(JSON.stringify(route)) as Route, isNew: false })}
                    >
                      编辑
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <SimPanel draft={draft} />
      {editing && (
        <RouteEditor
          draft={draft}
          value={editing.value}
          isNew={editing.isNew}
          onChange={(value) => setEditing({ value, isNew: editing.isNew })}
          onConfirm={confirmEdit}
          onCancel={() => setEditing(null)}
          onClose={() => setEditing(null)}
          onDelete={editing.isNew ? undefined : () => remove(editing.value.name)}
        />
      )}
    </section>
  );
}

function RouteEditor({
  draft,
  value,
  isNew,
  onChange,
  onConfirm,
  onCancel,
  onClose,
  onDelete,
}: {
  draft: JanusConfig;
  value: Route;
  isNew: boolean;
  onChange: (value: Route) => void;
  onConfirm: () => void;
  onCancel: () => void;
  onClose: () => void;
  onDelete?: () => void;
}) {
  const actionType = value.action?.redirect ? "redirect" : value.action?.respond ? "respond" : "forward";
  const services = serviceNames(draft);
  const middlewares = draft.middlewares || {};
  const set = (patch: Partial<Route>) => onChange({ ...value, ...patch });

  function setActionType(type: string) {
    if (type === "redirect") set({ action: { redirect: { status: 308, location: "https://example.com" } } });
    else if (type === "respond")
      set({ action: { respond: { status: 200, body: "ok\n" } } });
    else set({ action: { forward: { service: value.action?.forward?.service || services[0] || "" } } });
  }

  function toggleMiddleware(name: string) {
    const list = value.middlewares || [];
    set({ middlewares: list.includes(name) ? list.filter((item) => item !== name) : [...list, name] });
  }

  return (
    <Drawer
      title={isNew ? "新建 Route" : `编辑 ${value.name}`}
      subtitle="Route 只负责引用已存在的 Service 与 Middleware。"
      onClose={onClose}
      onConfirm={onConfirm}
      onCancel={onCancel}
      onDelete={onDelete}
    >
      <Field label="名称" hint={isNew ? "创建后不可改名，如需改名请删后重建" : "名称不可修改"}>
        <input value={value.name} readOnly={!isNew} onChange={(e) => set({ name: e.target.value })} />
      </Field>
      <Field label="入口 Limen">
        <select value={value.limen || ""} onChange={(e) => set({ limen: e.target.value })}>
          <option value="">请选择</option>
          {limenNames(draft).map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Match" hint="例如 Host(`api.example.com`) && PathPrefix(`/api`)">
        <textarea rows={3} value={value.match || ""} onChange={(e) => set({ match: e.target.value })} spellCheck={false} />
      </Field>
      <Field label="优先级 Priority">
        <input
          type="number"
          min={0}
          value={value.priority ?? 0}
          onChange={(e) => set({ priority: Number(e.target.value) || 0 })}
        />
      </Field>
      <Field label="动作 Action">
        <select value={actionType} onChange={(e) => setActionType(e.target.value)}>
          <option value="forward">Forward 到 Service</option>
          <option value="redirect">Redirect</option>
          <option value="respond">Direct Response</option>
        </select>
      </Field>
      {actionType === "forward" && (
        <Field label="目标 Service">
          <select
            value={value.action?.forward?.service || ""}
            onChange={(e) => set({ action: { forward: { service: e.target.value } } })}
          >
            <option value="">请选择 Service</option>
            {services.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </Field>
      )}
      {actionType === "redirect" && (
        <>
          <Field label="状态码">
            <input
              type="number"
              min={300}
              max={399}
              value={value.action?.redirect?.status ?? 308}
              onChange={(e) =>
                set({ action: { redirect: { location: value.action?.redirect?.location || "", status: Number(e.target.value) || 308 } } })
              }
            />
          </Field>
          <Field label="Location">
            <input
              value={value.action?.redirect?.location || ""}
              onChange={(e) =>
                set({ action: { redirect: { location: e.target.value, status: value.action?.redirect?.status || 308 } } })
              }
            />
          </Field>
        </>
      )}
      {actionType === "respond" && (
        <>
          <Field label="状态码">
            <input
              type="number"
              min={100}
              max={599}
              value={value.action?.respond?.status ?? 200}
              onChange={(e) =>
                set({ action: { respond: { ...value.action?.respond, status: Number(e.target.value) || 200 } } })
              }
            />
          </Field>
          <Field label="Body">
            <textarea
              rows={3}
              value={value.action?.respond?.body || ""}
              onChange={(e) => set({ action: { respond: { ...value.action?.respond, body: e.target.value } } })}
            />
          </Field>
        </>
      )}
      <div className="check-group">
        <div className="field-label">中间件（按勾选顺序执行）</div>
        {Object.keys(middlewares).length === 0 && <small className="muted">还没有 Middleware，请先到中间件页创建。</small>}
        {Object.entries(middlewares).map(([name, def]) => {
          const usable = !def.scope || def.scope === "route";
          const checked = (value.middlewares || []).includes(name);
          return (
            <label key={name} className={`check-row ${usable ? "" : "disabled"}`}>
              <input type="checkbox" checked={checked} disabled={!usable} onChange={() => toggleMiddleware(name)} />
              <span>
                <strong>{name}</strong>
                <small>{def.buffer ? "buffer" : def.body_limit ? "body_limit" : def.in_flight ? "in_flight（仅 Service）" : "未配置"}</small>
              </span>
            </label>
          );
        })}
      </div>
    </Drawer>
  );
}

function SimPanel({ draft }: { draft: JanusConfig }) {
  const [open, setOpen] = useState(false);
  const [target, setTarget] = useState("/api/hello");
  const [host, setHost] = useState("");
  const [method, setMethod] = useState("GET");
  const [result, setResult] = useState<ReturnType<typeof simulateRequest> | null>(null);

  function run() {
    setResult(simulateRequest(draft, { target, host, method, protocol: "http", limen: "" }));
  }

  return (
    <div className={`sim ${open ? "open" : ""}`}>
      <button type="button" className="btn ghost" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        ⌁ 模拟请求（只看草稿，不发真实流量）
      </button>
      {open && (
        <div className="card sim-card">
          <div className="sim-grid">
            <label>
              Method
              <select value={method} onChange={(e) => setMethod(e.target.value)}>
                {["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"].map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </select>
            </label>
            <label>
              Path 或 URL
              <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="/api/hello" />
            </label>
            <label>
              Host
              <input value={host} onChange={(e) => setHost(e.target.value)} placeholder="api.example.com" />
            </label>
          </div>
          <button type="button" className="btn primary" disabled={!target.trim()} onClick={run}>
            运行模拟
          </button>
          {result && (
            <div className="sim-result">
              {result.error ? (
                <div className="error-text">{result.error}</div>
              ) : result.matchedRoute ? (
                <div>
                  命中 <strong>{result.matchedRoute.name}</strong> · {result.matchedRoute.action}
                  {result.matchedRoute.service ? ` → ${result.matchedRoute.service}` : ""}
                </div>
              ) : (
                <div>没有匹配的 Route。</div>
              )}
              {result.candidates.length > 0 && (
                <details>
                  <summary>查看 {result.candidates.length} 条候选</summary>
                  {result.candidates.map((c) => (
                    <div key={c.routeName} className="sim-candidate">
                      <span>{c.matched ? "✓" : "—"}</span> <strong>{c.routeName}</strong> <small>{c.reason}</small>
                    </div>
                  ))}
                </details>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
