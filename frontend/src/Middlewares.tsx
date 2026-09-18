import { useState } from "react";
import { middlewareKind, uniqueName } from "./model";
import { Drawer, Empty, Field } from "./ui";
import type { ConfigStore } from "./useConfig";
import type { Middleware } from "./types";

type MwType = "buffer" | "body_limit" | "in_flight";

function typeOf(def: Middleware): MwType {
  if (def.body_limit) return "body_limit";
  if (def.in_flight) return "in_flight";
  return "buffer";
}

export function MiddlewaresPage({ store }: { store: ConfigStore }) {
  const draft = store.draft;
  const [editing, setEditing] = useState<{ name: string; original: string; value: Middleware; isNew: boolean } | null>(null);
  if (!draft) return <Empty text="正在加载配置…" />;
  const entries = Object.entries(draft.middlewares || {});

  function startCreate() {
    const name = uniqueName("middleware", Object.keys(store.draft?.middlewares || {}));
    setEditing({ name, original: name, value: { buffer: { max_response_body_bytes: 1048576 } }, isNew: true });
  }

  function confirm() {
    if (!editing) return;
    const name = editing.name.trim();
    if (!name) {
      store.setMessage("Middleware 名称不能为空。");
      return;
    }
    store.update((prev) => {
      const nextMiddlewares = { ...(prev.middlewares || {}) };
      if (editing.isNew && nextMiddlewares[name]) {
        store.setMessage(`Middleware 已存在：${name}`);
        return prev;
      }
      if (!editing.isNew && name !== editing.original) {
        if (nextMiddlewares[name]) {
          store.setMessage(`Middleware 已存在：${name}`);
          return prev;
        }
        delete nextMiddlewares[editing.original];
        const rename = (list: string[] | undefined) => (list || []).map((item) => (item === editing.original ? name : item));
        return {
          ...prev,
          middlewares: { ...nextMiddlewares, [name]: editing.value },
          routes: (prev.routes || []).map((r) => ({ ...r, middlewares: rename(r.middlewares) })),
          services: Object.fromEntries(
            Object.entries(prev.services || {}).map(([serviceName, service]) => [
              serviceName,
              { ...service, middlewares: rename(service.middlewares) },
            ]),
          ),
        };
      }
      return { ...prev, middlewares: { ...nextMiddlewares, [name]: editing.value } };
    });
    setEditing(null);
  }

  function remove(name: string) {
    const config = store.draft;
    if (!config) return;
    const usedRoutes = (config.routes || []).filter((r) => (r.middlewares || []).includes(name)).map((r) => r.name);
    const usedServices = Object.entries(config.services || {})
      .filter(([, s]) => (s.middlewares || []).includes(name))
      .map(([serviceName]) => serviceName);
    if (!window.confirm(`删除 Middleware ${name}？会同步从 ${usedRoutes.length + usedServices.length} 处引用移除。`)) return;
    store.update((prev) => {
      const next = { ...(prev.middlewares || {}) };
      delete next[name];
      const strip = (list: string[] | undefined) => (list || []).filter((item) => item !== name);
      return {
        ...prev,
        middlewares: next,
        routes: (prev.routes || []).map((r) => ({ ...r, middlewares: strip(r.middlewares) })),
        services: Object.fromEntries(
          Object.entries(prev.services || {}).map(([serviceName, service]) => [
            serviceName,
            { ...service, middlewares: strip(service.middlewares) },
          ]),
        ),
      };
    });
    setEditing(null);
  }

  return (
    <section className="page">
      <div className="toolbar">
        <p className="muted">Route 可用 buffer / body_limit；Service 还可用 in_flight。</p>
        <button type="button" className="btn primary" onClick={startCreate}>
          ＋ 新建 Middleware
        </button>
      </div>
      {entries.length === 0 ? (
        <Empty text="暂无 Middleware，点击右上角创建。" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>类型</th>
                <th>作用域</th>
                <th className="col-action">操作</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(([name, def]) => (
                <tr key={name}>
                  <td>
                    <strong>{name}</strong>
                  </td>
                  <td>{middlewareKind(def)}</td>
                  <td>{def.scope === "route" ? "Route" : def.scope === "service" ? "Service" : "Route + Service"}</td>
                  <td className="col-action">
                    <button
                      type="button"
                      className="btn small"
                      onClick={() => setEditing({ name, original: name, value: JSON.parse(JSON.stringify(def)) as Middleware, isNew: false })}
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
      {editing && (
        <Drawer
          title={editing.isNew ? "新建 Middleware" : `编辑 ${editing.original}`}
          subtitle="改名会自动同步所有 Route 与 Service 的引用。"
          onClose={() => setEditing(null)}
          onConfirm={confirm}
          onCancel={() => setEditing(null)}
          onDelete={editing.isNew ? undefined : () => remove(editing.original)}
        >
          <Field label="名称">
            <input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} />
          </Field>
          <Field label="作用域 Scope">
            <select
              value={editing.value.scope || ""}
              onChange={(e) => {
                const scope = e.target.value;
                let value = { ...editing.value };
                if (scope === "route" && value.in_flight) value = { body_limit: { max_bytes: 10485760 } };
                setEditing({ ...editing, value: { ...value, scope: scope || undefined } });
              }}
            >
              <option value="">Route + Service（共享）</option>
              <option value="route">Route</option>
              <option value="service">Service</option>
            </select>
          </Field>
          <Field label="类型">
            <select
              value={typeOf(editing.value)}
              onChange={(e) => {
                const type = e.target.value as MwType;
                if (type === "buffer") setEditing({ ...editing, value: { ...editing.value, buffer: { max_response_body_bytes: 1048576 }, body_limit: undefined, in_flight: undefined } });
                if (type === "body_limit") setEditing({ ...editing, value: { ...editing.value, body_limit: { max_bytes: 10485760 }, buffer: undefined, in_flight: undefined } });
                if (type === "in_flight") setEditing({ ...editing, value: { ...editing.value, in_flight: { max_concurrent: 100 }, buffer: undefined, body_limit: undefined, scope: "service" } });
              }}
            >
              <option value="buffer">buffer（缓存响应）</option>
              <option value="body_limit">body_limit（限制请求体）</option>
              <option value="in_flight" disabled={editing.value.scope === "route"}>
                in_flight（限制并发，仅 Service）
              </option>
            </select>
          </Field>
          {typeOf(editing.value) === "buffer" && (
            <Field label="max_response_body_bytes">
              <input
                type="number"
                min={1}
                max={67108864}
                value={editing.value.buffer?.max_response_body_bytes ?? 1048576}
                onChange={(e) =>
                  setEditing({ ...editing, value: { ...editing.value, buffer: { max_response_body_bytes: Number(e.target.value) || 0 } } })
                }
              />
            </Field>
          )}
          {typeOf(editing.value) === "body_limit" && (
            <Field label="max_bytes">
              <input
                type="number"
                min={1}
                max={67108864}
                value={editing.value.body_limit?.max_bytes ?? 10485760}
                onChange={(e) =>
                  setEditing({ ...editing, value: { ...editing.value, body_limit: { max_bytes: Number(e.target.value) || 0 } } })
                }
              />
            </Field>
          )}
          {typeOf(editing.value) === "in_flight" && (
            <Field label="max_concurrent">
              <input
                type="number"
                min={1}
                max={10000}
                value={editing.value.in_flight?.max_concurrent ?? 100}
                onChange={(e) =>
                  setEditing({ ...editing, value: { ...editing.value, in_flight: { max_concurrent: Number(e.target.value) || 0 } } })
                }
              />
            </Field>
          )}
        </Drawer>
      )}
    </section>
  );
}
