import { useState } from "react";
import { registryNames, serviceSubtitle, splitLines, uniqueName } from "./model";
import { Drawer, Empty, Field } from "./ui";
import type { ConfigStore } from "./useConfig";
import type { Service } from "./types";

export function ServicesPage({ store }: { store: ConfigStore }) {
  const draft = store.draft;
  const [editing, setEditing] = useState<{ name: string; value: Service; isNew: boolean } | null>(null);
  if (!draft) return <Empty text="正在加载配置…" />;
  const entries = Object.entries(draft.services || {});

  function startCreate() {
    const name = uniqueName("service", Object.keys(store.draft?.services || {}));
    setEditing({ name, value: { upstreams: ["http://127.0.0.1:9000"], middlewares: [] }, isNew: true });
  }

  function confirm() {
    if (!editing) return;
    const config = store.draft;
    if (!config) return;
    const name = editing.name.trim();
    if (!name) {
      store.setMessage("Service 名称不能为空。");
      return;
    }
    if (editing.isNew && config.services?.[name]) {
      store.setMessage(`Service 已存在：${name}`);
      return;
    }
    store.update((prev) => ({ ...prev, services: { ...(prev.services || {}), [name]: editing.value } }));
    setEditing(null);
  }

  function remove(name: string) {
    const config = store.draft;
    if (!config) return;
    const usedBy = (config.routes || [])
      .filter((r) => (r.action?.forward?.service || r.service) === name)
      .map((r) => r.name);
    if (usedBy.length > 0) {
      store.setMessage(`Service ${name} 仍被路由引用：${usedBy.join("、")}，请先修改这些路由。`);
      return;
    }
    if (!window.confirm(`删除 Service ${name}？`)) return;
    store.update((prev) => {
      const next = { ...(prev.services || {}) };
      delete next[name];
      return { ...prev, services: next };
    });
    setEditing(null);
  }

  return (
    <section className="page">
      <div className="toolbar">
        <p className="muted">先在这里维护上游池，Route 只负责引用。</p>
        <button type="button" className="btn primary" onClick={startCreate}>
          ＋ 新建 Service
        </button>
      </div>
      {entries.length === 0 ? (
        <Empty text="暂无 Service，点击右上角创建。" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>来源</th>
                <th>中间件</th>
                <th className="col-action">操作</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(([name, service]) => (
                <tr key={name}>
                  <td>
                    <strong>{name}</strong>
                    <br />
                    <small className="muted">{serviceSubtitle(service)}</small>
                  </td>
                  <td>{service.nacos ? "Nacos" : "Static"}</td>
                  <td>{(service.middlewares || []).join("、") || <span className="muted">—</span>}</td>
                  <td className="col-action">
                    <button
                      type="button"
                      className="btn small"
                      onClick={() => setEditing({ name, value: JSON.parse(JSON.stringify(service)) as Service, isNew: false })}
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
        <ServiceEditor
          store={store}
          name={editing.name}
          value={editing.value}
          isNew={editing.isNew}
          onName={editing.isNew ? (name) => setEditing({ ...editing, name }) : undefined}
          onChange={(value) => setEditing({ ...editing, value })}
          onConfirm={confirm}
          onCancel={() => setEditing(null)}
          onClose={() => setEditing(null)}
          onDelete={editing.isNew ? undefined : () => remove(editing.name)}
        />
      )}
    </section>
  );
}

function ServiceEditor({
  store,
  name,
  value,
  isNew,
  onName,
  onChange,
  onConfirm,
  onCancel,
  onClose,
  onDelete,
}: {
  store: ConfigStore;
  name: string;
  value: Service;
  isNew: boolean;
  onName?: (name: string) => void;
  onChange: (value: Service) => void;
  onConfirm: () => void;
  onCancel: () => void;
  onClose: () => void;
  onDelete?: () => void;
}) {
  const draft = store.draft;
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

  function toggleMiddleware(mw: string) {
    const list = value.middlewares || [];
    set({ middlewares: list.includes(mw) ? list.filter((item) => item !== mw) : [...list, mw] });
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
            <textarea
              rows={3}
              value={(value.upstreams || []).join("\n")}
              onChange={(e) => set({ upstreams: splitLines(e.target.value) })}
            />
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
          <Field label="Registry">
            <select value={value.nacos?.registry || ""} onChange={(e) => set({ nacos: { ...value.nacos!, registry: e.target.value } })}>
              <option value="">请选择</option>
              {registries.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </Field>
          {registries.length === 0 && <p className="error-text">还没有 Registry，请先到注册中心页创建。</p>}
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
            <textarea
              rows={2}
              value={(value.nacos?.clusters || []).join("\n")}
              onChange={(e) => set({ nacos: { ...value.nacos!, clusters: splitLines(e.target.value) } })}
            />
          </Field>
        </>
      )}
      <div className="check-group">
        <div className="field-label">Service 中间件</div>
        {Object.keys(middlewares).length === 0 && <small className="muted">还没有 Middleware。</small>}
        {Object.entries(middlewares).map(([mw, def]) => {
          const usable = !def.scope || def.scope === "service";
          return (
            <label key={mw} className={`check-row ${usable ? "" : "disabled"}`}>
              <input
                type="checkbox"
                checked={(value.middlewares || []).includes(mw)}
                disabled={!usable}
                onChange={() => toggleMiddleware(mw)}
              />
              <span>
                <strong>{mw}</strong>
                <small>{def.in_flight ? "in_flight" : def.buffer ? "buffer" : def.body_limit ? "body_limit" : "未配置"}</small>
              </span>
            </label>
          );
        })}
      </div>
    </Drawer>
  );
}
