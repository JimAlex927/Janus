import { useEffect, useRef, useState } from "react";
import { apiPath, getConfig, listConfigs, type ConfigRecord } from "./api";
import type { ConfigStore } from "./useConfig";
import { Badge, Empty, StatCard, useDialogController } from "./ui";

const PAGE_SIZE = 12;

export function ConfigsPage({ store, onEdit }: { store: ConfigStore; onEdit: (id: number) => void }) {
  const [active, setActive] = useState<ConfigRecord | null>(null);
  const [drafts, setDrafts] = useState<ConfigRecord[]>([]);
  const [archived, setArchived] = useState<ConfigRecord[]>([]);
  const [draftTotal, setDraftTotal] = useState(0);
  const [archivedTotal, setArchivedTotal] = useState(0);
  const [draftPageNum, setDraftPageNum] = useState(1);
  const [archivedPageNum, setArchivedPageNum] = useState(1);
  const [createdAtFrom, setCreatedAtFrom] = useState("");
  const [createdAtTo, setCreatedAtTo] = useState("");
  const [updatedAtFrom, setUpdatedAtFrom] = useState("");
  const [updatedAtTo, setUpdatedAtTo] = useState("");
  const [sortBy, setSortBy] = useState<"createdAt" | "updatedAt">("updatedAt");
  const [sortOrder, setSortOrder] = useState<"asc" | "desc">("desc");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const dialogs = useDialogController();

  useEffect(() => {
    loadConfigs().catch(() => undefined);
    // Each status owns its own page number.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftPageNum, archivedPageNum, createdAtFrom, createdAtTo, updatedAtFrom, updatedAtTo, sortBy, sortOrder]);

  async function loadConfigs() {
    setLoading(true);
    setError("");
    try {
      const [activePage, draftPage, archivedPage] = await Promise.all([
        listConfigs({ status: "active", pageNum: 1, pageSize: 1 }),
        listConfigs({ status: "draft", pageNum: draftPageNum, pageSize: PAGE_SIZE, ...queryFilters() }),
        listConfigs({ status: "archived", pageNum: archivedPageNum, pageSize: PAGE_SIZE, ...queryFilters() }),
      ]);
      setActive(activePage.configs[0] || null);
      setDrafts(draftPage.configs || []);
      setArchived(archivedPage.configs || []);
      setDraftTotal(draftPage.total || 0);
      setArchivedTotal(archivedPage.total || 0);
      if (draftPage.configs.length === 0 && draftPage.total > 0 && draftPageNum > Math.ceil(draftPage.total / PAGE_SIZE)) {
        setDraftPageNum(Math.ceil(draftPage.total / PAGE_SIZE));
      }
      if (archivedPage.configs.length === 0 && archivedPage.total > 0 && archivedPageNum > Math.ceil(archivedPage.total / PAGE_SIZE)) {
        setArchivedPageNum(Math.ceil(archivedPage.total / PAGE_SIZE));
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  function queryFilters() {
    return {
      sortBy,
      sortOrder,
      createdAtFrom: toApiTime(createdAtFrom),
      createdAtTo: toApiTime(createdAtTo),
      updatedAtFrom: toApiTime(updatedAtFrom),
      updatedAtTo: toApiTime(updatedAtTo),
    };
  }

  function resetQueryPage() {
    setDraftPageNum(1);
    setArchivedPageNum(1);
  }

  async function createConfig() {
    const name = (await dialogs.prompt({
      title: "新建配置",
      message: "为这份配置设置一个便于识别的名称。",
      initialValue: `config-${draftTotal + archivedTotal + (active ? 1 : 0) + 1}`,
      placeholder: "例如 production",
      confirmLabel: "创建配置",
      icon: "plus",
      context: "CREATE",
      inputLabel: "配置名称",
      inputHint: "建议使用环境或用途命名，便于后续发布和回滚。",
    }))?.trim();
    if (!name) return;
    try {
      const snap = await getConfig();
      const res = await fetch(apiPath("/api/v1/configs"), {
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
      const res = await fetch(apiPath(`/api/v1/configs/${id}`));
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
      icon: "copy",
      context: "DUPLICATE",
      inputLabel: "副本名称",
      inputHint: "副本会保留完整配置和凭据，创建后将打开新的草稿。",
    }))?.trim();
    if (!next) return;
    try {
      const res = await fetch(apiPath("/api/v1/configs"), {
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
        icon: "upload",
        context: "IMPORT",
        inputLabel: "配置名称",
        inputHint: "脱敏凭据不会恢复，导入后请在编辑器中重新填写。",
      }))?.trim();
      if (!name) return;
      const res = await fetch(apiPath("/api/v1/configs"), {
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
      icon: "trash",
      context: "DELETE",
    }))) return;
    try {
      const res = await fetch(apiPath(`/api/v1/configs/${id}`), { method: "DELETE" });
      if (!res.ok) throw new Error(await res.text());
      if (drafts.length === 1 && draftPageNum > 1) setDraftPageNum((page) => page - 1);
      else await loadConfigs();
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
      icon: "publish",
      context: "PUBLISH",
    }))) return;
    try {
      const res = await fetch(apiPath(`/api/v1/configs/${id}/publish`), {
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

  function renderConfigCard(cfg: ConfigRecord) {
    return (
      <div key={cfg.id} className={`config-card ${cfg.status}`}>
        <div className="config-card-header">
          <div className="config-name">
            <strong>{cfg.name}</strong>
            <Badge text={cfg.status} variant={cfg.status === "active" ? "success" : cfg.status === "draft" ? "warn" : "default"} />
          </div>
          <div className="config-time">更新于 {new Date(cfg.updated_at).toLocaleString()}</div>
        </div>
        <div className="config-card-actions">
          <button type="button" className="btn small" onClick={() => onEdit(cfg.id)}>编辑</button>
          {cfg.status !== "active" && (
            <button type="button" className="btn small primary" onClick={() => publishConfig(cfg.id)}>
              {cfg.status === "archived" ? "回滚" : "发布"}
            </button>
          )}
          <button type="button" className="btn small" onClick={() => exportConfig(cfg.id, cfg.name)}>导出</button>
          <button type="button" className="btn small" onClick={() => duplicateConfig(cfg.id, cfg.name)}>复制</button>
          {cfg.status === "draft" && (
            <button type="button" className="btn small danger" onClick={() => deleteConfig(cfg.id, cfg.name)}>删除</button>
          )}
        </div>
      </div>
    );
  }

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

      <div className="config-query-controls">
        <label>排序字段<select value={sortBy} onChange={(event) => { setSortBy(event.target.value as "createdAt" | "updatedAt"); resetQueryPage(); }}><option value="updatedAt">修改时间</option><option value="createdAt">创建时间</option></select></label>
        <label>顺序<select value={sortOrder} onChange={(event) => { setSortOrder(event.target.value as "asc" | "desc"); resetQueryPage(); }}><option value="desc">倒序</option><option value="asc">正序</option></select></label>
        <label>创建时间起<input type="datetime-local" value={createdAtFrom} onChange={(event) => { setCreatedAtFrom(event.target.value); resetQueryPage(); }} /></label>
        <label>创建时间止<input type="datetime-local" value={createdAtTo} onChange={(event) => { setCreatedAtTo(event.target.value); resetQueryPage(); }} /></label>
        <label>修改时间起<input type="datetime-local" value={updatedAtFrom} onChange={(event) => { setUpdatedAtFrom(event.target.value); resetQueryPage(); }} /></label>
        <label>修改时间止<input type="datetime-local" value={updatedAtTo} onChange={(event) => { setUpdatedAtTo(event.target.value); resetQueryPage(); }} /></label>
      </div>

      <div className="stat-grid config-summary-grid">
        <StatCard label="生效中" value={active ? 1 : 0} sub={active?.name} />
        <StatCard label="草稿" value={draftTotal} />
        <StatCard label="历史版本" value={archivedTotal} />
      </div>

      {!active && draftTotal === 0 && archivedTotal === 0 ? (
        <Empty text="暂无配置，点击右上角创建" action={<button className="btn primary" onClick={createConfig}>创建第一个配置</button>} />
      ) : (
        <div className="config-groups">
          {active && <ConfigGroup label="当前生效" detail="正在运行的版本"><div className="card-list">{renderConfigCard(active)}</div></ConfigGroup>}
          {draftTotal > 0 && <ConfigGroup label="草稿" detail={`共 ${draftTotal} 条，独立分页`}><div className="card-list">{drafts.map(renderConfigCard)}</div><PageControls total={draftTotal} pageNum={draftPageNum} onPageChange={setDraftPageNum} /></ConfigGroup>}
          {archivedTotal > 0 && <ConfigGroup label="历史版本" detail={`共 ${archivedTotal} 条，独立分页，可直接回滚`}><div className="card-list">{archived.map(renderConfigCard)}</div><PageControls total={archivedTotal} pageNum={archivedPageNum} onPageChange={setArchivedPageNum} /></ConfigGroup>}
        </div>
      )}
      {dialogs.dialog}
    </section>
  );
}

function ConfigGroup({ label, detail, children }: { label: string; detail: string; children: React.ReactNode }) {
  return <section className="config-group"><div className="config-group-head"><div><span>{label}</span><small>{detail}</small></div></div>{children}</section>;
}

function PageControls({ total, pageNum, onPageChange }: { total: number; pageNum: number; onPageChange: (pageNum: number) => void }) {
  if (total <= PAGE_SIZE) return null;
  const first = (pageNum - 1) * PAGE_SIZE + 1;
  const last = Math.min(pageNum * PAGE_SIZE, total);
  const pageCount = Math.ceil(total / PAGE_SIZE);
  return <div className="config-page-controls"><small>{first}-{last} / {total} · 第 {pageNum}/{pageCount} 页</small><div><button type="button" className="btn small" disabled={pageNum === 1} onClick={() => onPageChange(Math.max(1, pageNum - 1))} title="上一页">←</button><button type="button" className="btn small" disabled={pageNum >= pageCount} onClick={() => onPageChange(Math.min(pageCount, pageNum + 1))} title="下一页">→</button></div></div>;
}

function toApiTime(value: string): string | undefined {
  return value ? new Date(value).toISOString() : undefined;
}
