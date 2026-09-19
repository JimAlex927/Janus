import { useEffect, useRef, useState } from "react";
import { getConfig } from "./api";
import type { ConfigStore } from "./useConfig";
import { Badge, Empty, StatCard, useDialogController } from "./ui";

export interface ConfigRecord {
  id: number;
  name: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export function ConfigsPage({ store, onEdit }: { store: ConfigStore; onEdit: (id: number) => void }) {
  const [configs, setConfigs] = useState<ConfigRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const dialogs = useDialogController();

  useEffect(() => {
    loadConfigs();
  }, []);

  async function loadConfigs() {
    setLoading(true);
    setError("");
    try {
      const res = await fetch("/api/v1/configs");
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      setConfigs(data.configs || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  async function createConfig() {
    const name = (await dialogs.prompt({
      title: "新建配置",
      message: "为这份配置设置一个便于识别的名称。",
      initialValue: `config-${configs.length + 1}`,
      placeholder: "例如 production",
      confirmLabel: "创建配置",
    }))?.trim();
    if (!name) return;
    try {
      const snap = await getConfig();
      const res = await fetch("/api/v1/configs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, content: snap.config }),
      });
      if (!res.ok) throw new Error(await res.text());
      const created = await res.json();
      store.setMessage("配置已创建，可以开始编辑");
      onEdit(created.id);
    } catch (e) {
      store.setMessage(e instanceof Error ? e.message : String(e));
    }
  }

  async function exportConfig(id: number, name: string) {
    try {
      const res = await fetch(`/api/v1/configs/${id}`);
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      const blob = new Blob([JSON.stringify(data.content, null, 2)], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `${safeFileName(name)}.json`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      store.setMessage(`已导出 ${name}.json（完整配置；凭据为脱敏后的占位，导入后需重填密码）`);
    } catch (e) {
      store.setMessage(e instanceof Error ? e.message : String(e));
    }
  }

  async function duplicateConfig(id: number, name: string) {
    const next = (await dialogs.prompt({
      title: "复制配置",
      message: `将复制「${name}」的完整内容与凭据，生成一份新的草稿。`,
      initialValue: `${name}-copy`,
      placeholder: "请输入新配置名称",
      confirmLabel: "创建副本",
    }))?.trim();
    if (!next) return;
    try {
      const res = await fetch("/api/v1/configs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: next, from: String(id) }),
      });
      if (!res.ok) throw new Error(await res.text());
      const created = await res.json();
      await loadConfigs();
      store.setMessage("已复制（含凭据，服务端直接拷贝不经过脱敏）");
      onEdit(created.id);
    } catch (e) {
      store.setMessage(e instanceof Error ? e.message : String(e));
    }
  }

  const fileInput = useRef<HTMLInputElement>(null);

  async function importConfig(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    try {
      const text = await file.text();
      let content: unknown;
      try {
        content = JSON.parse(text);
      } catch {
        throw new Error("文件不是合法 JSON。");
      }
      if (!content || typeof content !== "object" || Array.isArray(content) || !Array.isArray((content as { routes?: unknown }).routes)) {
        throw new Error("文件不是 Janus 完整配置（缺少 routes 数组）。");
      }
      const fallback = file.name.replace(/\.json$/i, "") || "imported";
      const name = (await dialogs.prompt({
        title: "导入配置",
        message: "为导入的 JSON 设置名称。脱敏凭据需要在导入后重新填写。",
        initialValue: fallback,
        placeholder: "请输入配置名称",
        confirmLabel: "导入配置",
      }))?.trim();
      if (!name) return;
      const res = await fetch("/api/v1/configs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, content }),
      });
      if (!res.ok) throw new Error(await res.text());
      const created = await res.json();
      await loadConfigs();
      store.setMessage("已导入。注意：文件中的凭据若被脱敏过，需要重新填写密码。");
      onEdit(created.id);
    } catch (e) {
      store.setMessage(e instanceof Error ? e.message : String(e));
    }
  }

  function safeFileName(name: string) {
    const value = name.trim().replace(/[<>:"/\\|?*\x00-\x1F]+/g, "-").replace(/^[. ]+|[. ]+$/g, "");
    return value || "config";
  }

  async function deleteConfig(id: number, name: string) {
    if (!(await dialogs.confirm({
      title: "删除配置",
      message: `确定删除「${name}」吗？该操作不可撤销。`,
      confirmLabel: "删除配置",
      tone: "danger",
    }))) return;
    try {
      const res = await fetch(`/api/v1/configs/${id}`, { method: "DELETE" });
      if (!res.ok) throw new Error(await res.text());
      await loadConfigs();
      store.setMessage("配置已删除");
    } catch (e) {
      store.setMessage(e instanceof Error ? e.message : String(e));
    }
  }

  async function publishConfig(id: number) {
    if (!(await dialogs.confirm({
      title: "发布配置",
      message: "当前生效配置将被替换，发布后路由配置会立即进入新的 generation。",
      confirmLabel: "确认发布",
      tone: "warning",
    }))) return;
    try {
      const res = await fetch(`/api/v1/configs/${id}/publish`, {
        method: "POST",
        headers: { "X-Janus-Revision": String(store.revision) },
      });
      if (res.status === 409) {
        await store.load(true);
        store.setMessage("版本冲突：远端已被他人更新。已刷新当前配置，请确认后再发布。");
        return;
      }
      if (!res.ok) {
        const text = await res.text();
        let msg = text;
        try { const body = JSON.parse(text); if (body.error) msg = body.error; } catch {}
        throw new Error(msg);
      }
      await loadConfigs();
      await store.load(true);
      store.setMessage("配置已发布");
    } catch (e) {
      store.setMessage(e instanceof Error ? e.message : String(e));
    }
  }

  const active = configs.find((c) => c.status === "active");
  const drafts = configs.filter((c) => c.status === "draft");
  const archived = configs.filter((c) => c.status === "archived");

  if (loading) return <Empty text="正在加载配置列表…" />;
  if (error) return <Empty text={`加载失败：${error}`} action={<button className="btn primary" onClick={loadConfigs}>重试</button>} />;

  return (
    <section className="page config-library-page">
      <div className="hero config-library-hero">
        <div className="hero-content">
          <div className="eyebrow">CONFIGURATIONS</div>
          <h2>配置管理</h2>
          <p>创建、编辑、发布网关配置。同一时间只有一个配置生效。</p>
        </div>
        <div className="toolbar-actions">
          <button type="button" className="btn ghost" onClick={() => fileInput.current?.click()}>导入 JSON</button>
          <button type="button" className="btn primary" onClick={createConfig}>＋ 新建配置</button>
        </div>
        <input ref={fileInput} type="file" accept=".json,application/json" hidden onChange={importConfig} />
      </div>

      <div className="stat-grid config-summary-grid">
        <StatCard label="生效中" value={active ? 1 : 0} sub={active?.name} />
        <StatCard label="草稿" value={drafts.length} />
        <StatCard label="历史版本" value={archived.length} />
      </div>

      {configs.length === 0 ? (
        <Empty text="暂无配置，点击右上角创建" action={<button className="btn primary" onClick={createConfig}>创建第一个配置</button>} />
      ) : (
        <div className="card-list">
          {configs.map((cfg) => (
            <div key={cfg.id} className={`config-card ${cfg.status}`}>
              <div className="config-card-header">
                <div className="config-name">
                  <strong>{cfg.name}</strong>
                  <Badge text={cfg.status} variant={cfg.status === "active" ? "success" : cfg.status === "draft" ? "warn" : "default"} />
                </div>
                <div className="config-time">
                  更新于 {new Date(cfg.updated_at).toLocaleString()}
                </div>
              </div>
              <div className="config-card-actions">
                <button type="button" className="btn small" onClick={() => onEdit(cfg.id)}>编辑</button>
                {cfg.status !== "active" && (
                  <button type="button" className="btn small primary" onClick={() => publishConfig(cfg.id)}>发布</button>
                )}
                <button type="button" className="btn small" onClick={() => exportConfig(cfg.id, cfg.name)}>导出</button>
                <button type="button" className="btn small" onClick={() => duplicateConfig(cfg.id, cfg.name)}>复制</button>
                {cfg.status === "draft" && (
                  <button type="button" className="btn small danger" onClick={() => deleteConfig(cfg.id, cfg.name)}>删除</button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
      {dialogs.dialog}
    </section>
  );
}
