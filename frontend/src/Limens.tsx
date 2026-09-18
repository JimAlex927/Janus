import { useState } from "react";
import { splitLines } from "./model";
import { Drawer, Empty, Field } from "./ui";
import type { ConfigStore } from "./useConfig";
import type { Limen } from "./types";

const PROTOCOLS = [
  { value: "http1", label: "HTTP/1.1" },
  { value: "http2", label: "HTTP/2（需 TLS）" },
  { value: "h2c", label: "h2c（明文，不可与 TLS 共存）" },
  { value: "http3", label: "HTTP/3（需 TLS，且需保留 TCP 回退）" },
];

export function LimensPage({ store }: { store: ConfigStore }) {
  const draft = store.draft;
  const [editing, setEditing] = useState<{ name: string; value: Limen; isNew: boolean } | null>(null);
  if (!draft) return <Empty text="正在加载配置…" />;
  const entries = Object.entries(draft.limens || {});

  function confirm() {
    if (!editing) return;
    const config = store.draft;
    if (!config) return;
    const name = editing.name.trim();
    if (!name) {
      store.setMessage("Limen 名称不能为空。");
      return;
    }
    const tlsOn = Boolean(editing.value.tls);
    const protocols = editing.value.protocols || [];
    if (protocols.length === 0) {
      store.setMessage("至少启用 1 个协议。");
      return;
    }
    if (!editing.value.address) {
      store.setMessage("请填写监听地址（host:port）。");
      return;
    }
    if ((protocols.includes("http2") || protocols.includes("http3")) && !tlsOn) {
      store.setMessage("HTTP/2 与 HTTP/3 需要先启用 TLS。");
      return;
    }
    if (protocols.includes("h2c") && tlsOn) {
      store.setMessage("h2c 不能与 TLS 共存。");
      return;
    }
    if (protocols.includes("http3") && !protocols.includes("http1") && !protocols.includes("http2")) {
      store.setMessage("HTTP/3 需要同时保留 HTTP/1.1 或 HTTP/2 作为 TCP 回退。");
      return;
    }
    if (editing.isNew && config.limens?.[name]) {
      store.setMessage(`Limen 已存在：${name}`);
      return;
    }
    store.update((prev) => ({ ...prev, limens: { ...(prev.limens || {}), [name]: editing.value } }));
    setEditing(null);
  }

  function remove(name: string) {
    const config = store.draft;
    if (!config) return;
    const usedBy = (config.routes || []).filter((r) => r.limen === name).map((r) => r.name);
    if (usedBy.length > 0) {
      store.setMessage(`Limen ${name} 仍被路由引用：${usedBy.join("、")}，请先修改这些路由。`);
      return;
    }
    if (!window.confirm(`删除 Limen ${name}？监听变更需要重启 Janus 才生效。`)) return;
    store.update((prev) => {
      const next = { ...(prev.limens || {}) };
      delete next[name];
      return { ...prev, limens: next };
    });
    setEditing(null);
  }

  return (
    <section className="page">
      <div className="toolbar">
        <p className="muted">监听地址、协议、TLS 的修改需要重启 Janus；路由引用关系可热发布。</p>
        <button
          type="button"
          className="btn primary"
          onClick={() => setEditing({ name: "", value: { address: "127.0.0.1:8080", protocols: ["http1"] }, isNew: true })}
        >
          ＋ 新建 Limen
        </button>
      </div>
      {entries.length === 0 ? (
        <Empty text="暂无 Limen，点击右上角创建。" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>地址</th>
                <th>协议</th>
                <th>TLS</th>
                <th className="col-action">操作</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(([name, limen]) => (
                <tr key={name}>
                  <td>
                    <strong>{name}</strong>
                  </td>
                  <td className="mono">{limen.address}</td>
                  <td>{(limen.protocols || []).join(" · ")}</td>
                  <td>{limen.tls ? "启用" : <span className="muted">关闭</span>}</td>
                  <td className="col-action">
                    <button
                      type="button"
                      className="btn small"
                      onClick={() => setEditing({ name, value: JSON.parse(JSON.stringify(limen)) as Limen, isNew: false })}
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
          title={editing.isNew ? "新建 Limen" : `编辑 ${editing.name}`}
          subtitle="地址 / 协议 / TLS 属于启动级配置，发布后需重启生效。"
          onClose={() => setEditing(null)}
          onConfirm={confirm}
          onCancel={() => setEditing(null)}
          onDelete={editing.isNew ? undefined : () => remove(editing.name)}
        >
          {editing.isNew && (
            <Field label="名称">
              <input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder="public-http" />
            </Field>
          )}
          <Field label="Address" hint="host:port，例如 127.0.0.1:8080">
            <input value={editing.value.address} onChange={(e) => setEditing({ ...editing, value: { ...editing.value, address: e.target.value } })} />
          </Field>
          <div className="check-group">
            <div className="field-label">Protocols</div>
            {PROTOCOLS.map((option) => {
              const checked = (editing.value.protocols || []).includes(option.value);
              return (
                <label key={option.value} className="check-row">
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => {
                      const list = editing.value.protocols || [];
                      const next = checked ? list.filter((p) => p !== option.value) : [...list, option.value];
                      setEditing({ ...editing, value: { ...editing.value, protocols: next } });
                    }}
                  />
                  <span>
                    <strong>{option.label}</strong>
                  </span>
                </label>
              );
            })}
          </div>
          <Field label="Trusted proxies" hint="每行一个 CIDR，可留空">
            <textarea
              rows={2}
              value={(editing.value.trusted_proxies || []).join("\n")}
              onChange={(e) => setEditing({ ...editing, value: { ...editing.value, trusted_proxies: splitLines(e.target.value) } })}
              placeholder="10.0.0.0/8"
            />
          </Field>
          <label className="check-row">
            <input
              type="checkbox"
              checked={Boolean(editing.value.tls)}
              onChange={(e) =>
                setEditing({
                  ...editing,
                  value: e.target.checked
                    ? { ...editing.value, tls: { cert_file: "certs/janus.crt", key_file: "certs/janus.key", min_version: "1.3" } }
                    : { ...editing.value, tls: undefined, http3: undefined, protocols: (editing.value.protocols || []).filter((p) => p !== "http3") },
                })
              }
            />
            <span>
              <strong>启用 TLS</strong>
            </span>
          </label>
          {editing.value.tls && (
            <>
              <Field label="cert_file">
                <input value={editing.value.tls.cert_file} onChange={(e) => setEditing({ ...editing, value: { ...editing.value, tls: { ...editing.value.tls!, cert_file: e.target.value } } })} />
              </Field>
              <Field label="key_file">
                <input value={editing.value.tls.key_file} onChange={(e) => setEditing({ ...editing, value: { ...editing.value, tls: { ...editing.value.tls!, key_file: e.target.value } } })} />
              </Field>
              <Field label="min_version">
                <select value={editing.value.tls.min_version || "1.3"} onChange={(e) => setEditing({ ...editing, value: { ...editing.value, tls: { ...editing.value.tls!, min_version: e.target.value } } })}>
                  <option value="1.2">TLS 1.2</option>
                  <option value="1.3">TLS 1.3</option>
                </select>
              </Field>
              <label className="check-row">
                <input
                  type="checkbox"
                  checked={Boolean(editing.value.http3)}
                  disabled={!editing.value.tls}
                  onChange={(e) =>
                    setEditing({
                      ...editing,
                      value: {
                        ...editing.value,
                        http3: e.target.checked ? { max_concurrent_streams: 100 } : undefined,
                        protocols: e.target.checked
                          ? [...new Set([...(editing.value.protocols || []), "http3"])]
                          : (editing.value.protocols || []).filter((p) => p !== "http3"),
                      },
                    })
                  }
                />
                <span>
                  <strong>启用 HTTP/3</strong>
                </span>
              </label>
            </>
          )}
        </Drawer>
      )}
    </section>
  );
}
