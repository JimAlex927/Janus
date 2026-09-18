import { Empty, Field } from "./ui";
import type { ConfigStore } from "./useConfig";

function get(settings: Record<string, unknown>, section: string, key: string): string {
  const group = (settings?.[section] as Record<string, unknown>) || {};
  const value = group[key];
  if (value === undefined || value === null) return "";
  if (typeof value === "boolean") return value ? "true" : "false";
  return String(value);
}

export function SettingsPage({ store }: { store: ConfigStore }) {
  const draft = store.draft;
  if (!draft) return <Empty text="正在加载配置…" />;
  const settings = (draft.settings || {}) as Record<string, unknown>;

  function set(section: string, key: string, raw: string, kind: "string" | "number" | "bool") {
    store.update((prev) => {
      const prevSettings = ((prev.settings || {}) as Record<string, unknown>) || {};
      const group = { ...((prevSettings[section] as Record<string, unknown>) || {}) };
      if (raw === "") {
        delete group[key];
      } else if (kind === "number") {
        const value = Number(raw);
        if (!Number.isFinite(value)) {
          store.setMessage(`${section}.${key} 需要填写数字。`);
          return prev;
        }
        group[key] = value;
      } else if (kind === "bool") {
        group[key] = raw === "true";
      } else {
        group[key] = raw;
      }
      return { ...prev, settings: { ...prevSettings, [section]: group } };
    });
  }

  const text = (section: string, key: string, kind: "string" | "number" | "bool" = "string") => ({
    value: get(settings, section, key),
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => set(section, key, e.target.value, kind),
  });

  return (
    <section className="page">
      <div className="warn-card">
        <strong>注意：部分设置需要重启生效</strong>
        <p>监听与进程级参数（含 admin 地址、server 超时、shutdown）修改后需重启；发布接口会直接返回错误，请按提示处理。</p>
      </div>
      <div className="settings-grid">
        <div className="card">
          <h3>请求 request</h3>
          <Field label="read_timeout"><input {...text("request", "read_timeout")} placeholder="30s" /></Field>
          <Field label="maximum_duration"><input {...text("request", "maximum_duration")} placeholder="30s" /></Field>
          <Field label="max_in_flight"><input type="number" min={1} {...text("request", "max_in_flight", "number")} placeholder="1024" /></Field>
        </div>
        <div className="card">
          <h3>流 stream（SSE / WebSocket）</h3>
          <Field label="max_duration"><input {...text("stream", "max_duration")} placeholder="1h" /></Field>
          <Field label="idle_timeout"><input {...text("stream", "idle_timeout")} placeholder="5m" /></Field>
        </div>
        <div className="card">
          <h3>服务 server</h3>
          <Field label="read_header_timeout"><input {...text("server", "read_header_timeout")} placeholder="5s" /></Field>
          <Field label="write_timeout"><input {...text("server", "write_timeout")} placeholder="35s" /></Field>
          <Field label="idle_timeout"><input {...text("server", "idle_timeout")} placeholder="60s" /></Field>
          <Field label="max_header_bytes"><input type="number" min={1} {...text("server", "max_header_bytes", "number")} placeholder="32768" /></Field>
        </div>
        <div className="card">
          <h3>上游 backend</h3>
          <Field label="connect_timeout"><input {...text("backend", "connect_timeout")} placeholder="3s" /></Field>
          <Field label="response_header_timeout"><input {...text("backend", "response_header_timeout")} placeholder="10s" /></Field>
          <Field label="max_conns_per_host"><input type="number" min={1} {...text("backend", "max_conns_per_host", "number")} /></Field>
          <Field label="max_idle_conns_per_host"><input type="number" min={1} {...text("backend", "max_idle_conns_per_host", "number")} /></Field>
          <Field label="disable_compression">
            <select value={get(settings, "backend", "disable_compression") || "true"} onChange={(e) => set("backend", "disable_compression", e.target.value, "bool")}>
              <option value="true">true</option>
              <option value="false">false</option>
            </select>
          </Field>
        </div>
        <div className="card">
          <h3>管理 admin</h3>
          <Field label="address" hint="回环或私网地址，修改后需重启"><input {...text("admin", "address")} placeholder="127.0.0.1:9090" /></Field>
          <Field label="username"><input {...text("admin", "username")} placeholder="admin" /></Field>
          <p className="muted">password_hash 不会在此显示；改密码请用 janus-hash 生成后写入配置文件并重启。</p>
        </div>
        <div className="card">
          <h3>关闭 shutdown</h3>
          <Field label="drain_timeout"><input {...text("shutdown", "drain_timeout")} placeholder="35s" /></Field>
          <Field label="load_balancer_removal_delay"><input {...text("shutdown", "load_balancer_removal_delay")} placeholder="0s" /></Field>
        </div>
      </div>
      <div className="card">
        <h3>高级字段</h3>
        <p className="muted">未列出的 settings 字段请到 JSON 页编辑；留空表示使用后端默认值。</p>
      </div>
    </section>
  );
}
