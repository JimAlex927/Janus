import { useEffect, useState } from "react";
import { Empty } from "./ui";
import type { ConfigStore } from "./useConfig";

/**
 * JSON 页是全量兜底：表单未覆盖的高级字段只能在这里改。
 * 与旧版不同：文本是局部状态，只有“应用到草稿”才会触碰全局 draft，
 * 从根上避免双数据源互相覆盖的 bug。
 */
export function JsonPage({ store }: { store: ConfigStore }) {
  const draft = store.draft;
  const [text, setText] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (draft) {
      setText(JSON.stringify(draft, null, 2));
      setError("");
    }
  }, [store.revision]);

  if (!draft) return <Empty text="正在加载配置…" />;

  function apply() {
    try {
      const parsed = JSON.parse(text);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        setError("顶层必须是 JSON 对象。");
        return;
      }
      setError("");
      store.replace(parsed);
      store.setMessage("已将 JSON 应用到本地草稿，请校验后发布。");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  function format() {
    try {
      setText(JSON.stringify(JSON.parse(text), null, 2));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <section className="page">
      <div className="toolbar">
        <p className="muted">JSON 与各表单页共享同一份草稿；适合批量调整高级字段。</p>
        <div className="toolbar-actions">
          <button type="button" className="btn ghost" onClick={() => { setText(JSON.stringify(draft, null, 2)); setError(""); }}>
            重置
          </button>
          <button type="button" className="btn ghost" onClick={format}>
            格式化
          </button>
          <button type="button" className="btn primary" onClick={apply}>
            应用到草稿
          </button>
        </div>
      </div>
      {error && <div className="error-text card">{error}</div>}
      <textarea className="json-editor" value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} />
    </section>
  );
}
