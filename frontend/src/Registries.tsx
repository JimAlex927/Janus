import { useState } from "react";
import { ApiError, testRegistry } from "./api";
import { uniqueName } from "./model";
import { Drawer, Empty, Field } from "./ui";
import type { ConfigStore } from "./useConfig";
import type { NacosRegistry } from "./types";

type Health = { healthy: boolean; latency_ms?: number; error?: string };

export function RegistriesPage({ store }: { store: ConfigStore }) {
  const draft = store.draft;
  const [editing, setEditing] = useState<{ name: string; value: NacosRegistry; isNew: boolean } | null>(null);
  const [health, setHealth] = useState<Record<string, Health>>({});
  const [testing, setTesting] = useState<string | null>(null);
  if (!draft) return <Empty text="正在加载配置…" />;
  const entries = Object.entries(draft.discovery?.nacos || {});

  function startCreate() {
    const name = uniqueName("registry", Object.keys(draft?.discovery?.nacos || {}));
    setEditing({
      name,
      isNew: true,
      value: { servers: [{ address: "127.0.0.1", port: 8848 }], namespace_id: "public", timeout: "5s", stale_after: "2m" },
    });
  }

  function confirm() {
    if (!editing) return;
    const config = store.draft;
    if (!config) return;
    const name = editing.name.trim();
    if (!name) {
      store.setMessage("Registry 名称不能为空。");
      return;
    }
    if (editing.isNew && config.discovery?.nacos?.[name]) {
      store.setMessage(`Registry 已存在：${name}`);
      return;
    }
    if (editing.value.servers.length === 0) {
      store.setMessage("至少需要 1 个 Server。");
      return;
    }
    store.update((prev) => ({
      ...prev,
      discovery: { ...(prev.discovery || {}), nacos: { ...(prev.discovery?.nacos || {}), [name]: editing.value } },
    }));
    setEditing(null);
  }

  function remove(name: string) {
    const config = store.draft;
    if (!config) return;
    const usedBy = Object.entries(config.services || {})
      .filter(([, s]) => s.nacos?.registry === name)
      .map(([serviceName]) => serviceName);
    if (usedBy.length > 0) {
      store.setMessage(`Registry ${name} 仍被 Service 引用：${usedBy.join("、")}，请先修改这些 Service。`);
      return;
    }
    if (!window.confirm(`删除 Registry ${name}？`)) return;
    store.update((prev) => {
      const next = { ...(prev.discovery?.nacos || {}) };
      delete next[name];
      return { ...prev, discovery: { ...(prev.discovery || {}), nacos: next } };
    });
    setEditing(null);
  }

  async function runTest(name: string) {
    const registry = store.draft?.discovery?.nacos?.[name];
    if (!registry) return;
    setTesting(name);
    try {
      const result = await testRegistry(name, registry);
      setHealth((prev) => ({ ...prev, [name]: result }));
    } catch (error) {
      const message = error instanceof ApiError && error.status === 401 ? "未登录" : String(error);
      setHealth((prev) => ({ ...prev, [name]: { healthy: false, error: message } }));
      if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
    } finally {
      setTesting(null);
    }
  }

  return (
    <section className="page">
      <div className="toolbar">
        <p className="muted">账号密码不会被读取接口回显；编辑时留空表示沿用旧凭据。</p>
        <button type="button" className="btn primary" onClick={startCreate}>
          ＋ 新建 Registry
        </button>
      </div>
      {entries.length === 0 ? (
        <Empty text="暂无 Registry，点击右上角创建。" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>命名空间</th>
                <th>Servers</th>
                <th>健康</th>
                <th className="col-action">操作</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(([name, registry]) => {
                const result = health[name];
                return (
                  <tr key={name}>
                    <td>
                      <strong>{name}</strong>
                    </td>
                    <td>{registry.namespace_id || "public"}</td>
                    <td>{registry.servers?.length || 0}</td>
                    <td>
                      {!result ? (
                        <span className="muted">未测试</span>
                      ) : result.healthy ? (
                        <span className="ok-text">Healthy{result.latency_ms != null ? ` · ${result.latency_ms}ms` : ""}</span>
                      ) : (
                        <span className="error-text">Unhealthy{result.error ? ` · ${result.error}` : ""}</span>
                      )}
                    </td>
                    <td className="col-action">
                      <button
                        type="button"
                        className="btn small"
                        disabled={testing === name}
                        onClick={() => runTest(name)}
                      >
                        {testing === name ? "测试中…" : "测试"}
                      </button>{" "}
                      <button
                        type="button"
                        className="btn small"
                        onClick={() => setEditing({ name, isNew: false, value: JSON.parse(JSON.stringify(registry)) as NacosRegistry })}
                      >
                        编辑
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {editing && (
        <Drawer
          title={editing.isNew ? "新建 Registry" : `编辑 ${editing.name}`}
          subtitle="密码留空表示保持远端已保存的凭据。"
          onClose={() => setEditing(null)}
          onConfirm={confirm}
          onCancel={() => setEditing(null)}
          onDelete={editing.isNew ? undefined : () => remove(editing.name)}
        >
          {editing.isNew && (
            <Field label="名称">
              <input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} />
            </Field>
          )}
          <Field label="Namespace ID">
            <input
              value={editing.value.namespace_id || ""}
              placeholder="public"
              onChange={(e) => setEditing({ ...editing, value: { ...editing.value, namespace_id: e.target.value } })}
            />
          </Field>
          <div className="group-title">Servers</div>
          {editing.value.servers.map((server, index) => (
            <div className="server-row" key={index}>
              <input
                aria-label={`Server ${index + 1} address`}
                value={server.address}
                placeholder="127.0.0.1"
                onChange={(e) => {
                  const servers = editing.value.servers.map((s, i) => (i === index ? { ...s, address: e.target.value } : s));
                  setEditing({ ...editing, value: { ...editing.value, servers } });
                }}
              />
              <input
                aria-label={`Server ${index + 1} port`}
                type="number"
                min={1}
                max={65535}
                value={server.port}
                onChange={(e) => {
                  const servers = editing.value.servers.map((s, i) => (i === index ? { ...s, port: Number(e.target.value) || 0 } : s));
                  setEditing({ ...editing, value: { ...editing.value, servers } });
                }}
              />
              <button
                type="button"
                className="btn small"
                disabled={editing.value.servers.length <= 1}
                onClick={() => setEditing({ ...editing, value: { ...editing.value, servers: editing.value.servers.filter((_, i) => i !== index) } })}
              >
                移除
              </button>
            </div>
          ))}
          <button
            type="button"
            className="btn small"
            onClick={() => setEditing({ ...editing, value: { ...editing.value, servers: [...editing.value.servers, { address: "127.0.0.1", port: 8848 }] } })}
          >
            ＋ 添加 Server
          </button>
          <div className="grid-2">
            <Field label="Username">
              <input value={editing.value.username || ""} autoComplete="off" onChange={(e) => setEditing({ ...editing, value: { ...editing.value, username: e.target.value } })} />
            </Field>
            <Field label="Password" hint="留空保持原密码">
              <input
                type="password"
                value={editing.value.password || ""}
                autoComplete="new-password"
                onChange={(e) => setEditing({ ...editing, value: { ...editing.value, password: e.target.value, password_env: undefined } })}
              />
            </Field>
            <Field label="Password env">
              <input value={editing.value.password_env || ""} placeholder="JANUS_NACOS_PASSWORD" onChange={(e) => setEditing({ ...editing, value: { ...editing.value, password_env: e.target.value, password: undefined } })} />
            </Field>
            <Field label="Timeout">
              <input value={editing.value.timeout || ""} placeholder="5s" onChange={(e) => setEditing({ ...editing, value: { ...editing.value, timeout: e.target.value } })} />
            </Field>
            <Field label="Stale after">
              <input value={editing.value.stale_after || ""} placeholder="2m" onChange={(e) => setEditing({ ...editing, value: { ...editing.value, stale_after: e.target.value } })} />
            </Field>
          </div>
          <p className="muted">servers 由地址与端口两列组成；username 与 password / password_env 必须成组出现。</p>
        </Drawer>
      )}
    </section>
  );
}
