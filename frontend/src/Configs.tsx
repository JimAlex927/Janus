import { useEffect, useRef, useState } from "react";
import { apiPath, getConfig, listConfigs, type ConfigRecord } from "./api";
import type { ConfigStore } from "./useConfig";
import { Badge, Empty, useDialogController } from "./ui";

const PAGE_SIZE = 20;

type StatusTab = "active" | "draft" | "archived";

const TAB_LABELS: Record<StatusTab, { label: string; hint: string }> = {
  active: { label: "当前生效", hint: "正在运行的版本" },
  draft: { label: "草稿", hint: "可编辑、可发布" },
  archived: { label: "历史版本", hint: "可直接回滚" },
};

export function ConfigsPage({ store, onEdit }: { store: ConfigStore; onEdit: (id: number) => void }) {
  const [tab, setTab] = useState<StatusTab>("draft");
  const [records, setRecords] = useState<ConfigRecord[]>([]);
  const [totals, setTotals] = useState<Record<StatusTab, number>>({ active: 0, draft: 0, archived: 0 });
  const [pageNum, setPageNum] = useState(1);
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
    // Reload when tab, page, or filters change. Tab switching always starts at page 1.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, pageNum, createdAtFrom, createdAtTo, updatedAtFrom, updatedAtTo, sortBy, sortOrder]);

  async function loadConfigs() {
    setLoading(true);
    setError("");
    try {
      const [page, activePage, draftPage, archivedPage] = await Promise.all([
        listConfigs({ status: tab, pageNum, pageSize: PAGE_SIZE, ...queryFilters() }),
        tab !== "active" ? listConfigs({ status: "active", pageNum: 1, pageSize: 1 }) : Promise.resolve(null),
        tab !== "draft" ? listConfigs({ status: "draft", pageNum: 1, pageSize: 1 }) : Promise.resolve(null),
        tab !== "archived" ? listConfigs({ status: "archived", pageNum: 1, pageSize: 1 }) : Promise.resolve(null),
      ]);
      setRecords(page.configs || []);
      setTotals({
        active: tab === "active" ? page.total : activePage?.total ?? totals.active,
        draft: tab === "draft" ? page.total : draftPage?.total ?? totals.draft,
        archived: tab === "archived" ? page.total : archivedPage?.total ?? totals.archived,
      });
      if ((page.configs || []).length === 0 && page.total > 0 && pageNum > Math.ceil(page.total / PAGE_SIZE)) {
        setPageNum(Math.ceil(page.total / PAGE_SIZE));
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

  function switchTab(next: StatusTab) {
    if (next === tab) return;
    setTab(next);
    setPageNum(1);
  }

  function resetQueryPage() {
    setPageNum(1);
  }

  async function createConfig() {
    const name = (await dialogs.prompt({
      title: "新建配置",
      message: "为这份配置设置一个便于识别的名称。",
      initialValue: `config-${totals.draft + totals.archived + totals.active + 1}`,
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
      if (records.length === 1 && pageNum > 1) setPageNum((page) => page - 1);
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

  function renderConfigRow(cfg: ConfigRecord) {
    return (
      <div key={cfg.id} className={`config-row ${cfg.status}`}>
        <div className="config-row-main">
          <strong className="config-row-name" title={cfg.name}>{cfg.name}</strong>
          <Badge text={cfg.status} variant={cfg.status === "active" ? "success" : cfg.status === "draft" ? "warn" : "default"} />
        </div>
        <div className="config-row-time">
          <span title={new Date(cfg.updated_at).toLocaleString()}>更新于 {new Date(cfg.updated_at).toLocaleString()}</span>
          <small>创建于 {new Date(cfg.created_at).toLocaleString()}</small>
        </div>
        <div className="config-row-actions">
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

  const currentTotal = totals[tab];
  const pageCount = Math.max(1, Math.ceil(currentTotal / PAGE_SIZE));

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

      <div className="config-tabs" role="tablist" aria-label="配置状态">
        {(Object.keys(TAB_LABELS) as StatusTab[]).map((key) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            className={`config-tab ${tab === key ? "active" : ""}`}
            onClick={() => switchTab(key)}
          >
            <span className="config-tab-label">{TAB_LABELS[key].label}</span>
            <span className="config-tab-count">{totals[key]}</span>
            <span className="config-tab-hint">{TAB_LABELS[key].hint}</span>
          </button>
        ))}
      </div>

      {tab !== "active" && (
        <div className="config-query-controls">
          <label>排序字段<select value={sortBy} onChange={(event) => { setSortBy(event.target.value as "createdAt" | "updatedAt"); resetQueryPage(); }}><option value="updatedAt">修改时间</option><option value="createdAt">创建时间</option></select></label>
          <label>顺序<select value={sortOrder} onChange={(event) => { setSortOrder(event.target.value as "asc" | "desc"); resetQueryPage(); }}><option value="desc">倒序</option><option value="asc">正序</option></select></label>
          <label>创建时间起<input type="datetime-local" value={createdAtFrom} onChange={(event) => { setCreatedAtFrom(event.target.value); resetQueryPage(); }} /></label>
          <label>创建时间止<input type="datetime-local" value={createdAtTo} onChange={(event) => { setCreatedAtTo(event.target.value); resetQueryPage(); }} /></label>
          <label>修改时间起<input type="datetime-local" value={updatedAtFrom} onChange={(event) => { setUpdatedAtFrom(event.target.value); resetQueryPage(); }} /></label>
          <label>修改时间止<input type="datetime-local" value={updatedAtTo} onChange={(event) => { setUpdatedAtTo(event.target.value); resetQueryPage(); }} /></label>
        </div>
      )}

      {loading ? (
        <Empty text="正在加载配置列表…" />
      ) : error ? (
        <Empty text={`加载失败：${error}`} action={<button className="btn primary" onClick={loadConfigs}>重试</button>} />
      ) : currentTotal === 0 ? (
        <Empty
          text={tab === "active" ? "当前没有生效中的配置" : tab === "draft" ? "暂无草稿，点击右上角创建" : "暂无历史版本"}
          action={tab === "draft" ? <button className="btn primary" onClick={createConfig}>创建第一个配置</button> : undefined}
        />
      ) : (
        <div className="config-table">
          <div className="config-table-head">
            <span>名称</span>
            <span>时间</span>
            <span>操作</span>
          </div>
          {records.map(renderConfigRow)}
        </div>
      )}

      {currentTotal > 0 && (
        <div className="config-pager">
          <span className="config-pager-info">共 {currentTotal} 条 · 第 {pageNum}/{pageCount} 页 · 每页 {PAGE_SIZE} 条</span>
          <div className="config-pager-controls">
            <button type="button" className="btn small" disabled={pageNum === 1} onClick={() => setPageNum(1)} title="第一页">«</button>
            <button type="button" className="btn small" disabled={pageNum === 1} onClick={() => setPageNum((p) => Math.max(1, p - 1))}>上一页</button>
            <button type="button" className="btn small" disabled={pageNum >= pageCount} onClick={() => setPageNum((p) => Math.min(pageCount, p + 1))}>下一页</button>
            <button type="button" className="btn small" disabled={pageNum >= pageCount} onClick={() => setPageNum(pageCount)} title="最后一页">»</button>
          </div>
        </div>
      )}
      {dialogs.dialog}
    </section>
  );
}

function toApiTime(value: string): string | undefined {
  return value ? new Date(value).toISOString() : undefined;
}
