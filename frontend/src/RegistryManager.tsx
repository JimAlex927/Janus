import { useEffect, useState } from "react";
import { ApiError, testRegistry } from "./api";
import { RegistryForm } from "./editors";
import { Field } from "./ui";
import type { NacosRegistry } from "./types";

type Health = { healthy: boolean; latency_ms?: number; error?: string };

/**
 * 注册中心管理弹窗：左侧列表（测试/选用/删除），右侧选中后内联编辑参数。
 * 与中间件弹窗一致，修改即进入画布草稿（顶栏保存），密码留空表示沿用
 * 已保存凭据。
 */
export function RegistryManagerModal({
  title,
  registries,
  allowSelect,
  onSelect,
  onCreate,
  onRenameNew,
  onUpdate,
  onDelete,
  onClose,
  notify,
}: {
  title: string;
  registries: Record<string, NacosRegistry>;
  allowSelect: boolean;
  onSelect: (name: string) => void;
  onCreate: () => string | undefined;
  onRenameNew: (oldName: string, newName: string) => string | undefined;
  onUpdate: (name: string, reg: NacosRegistry) => void;
  onDelete: (name: string) => void;
  onClose: () => void;
  notify: (msg: string) => void;
}) {
  const names = Object.keys(registries);
  const [selected, setSelected] = useState<string | null>(names[0] || null);
  const [justCreated, setJustCreated] = useState<string | null>(null);
  const [nameDraft, setNameDraft] = useState("");
  const [testing, setTesting] = useState<string | null>(null);
  const [health, setHealth] = useState<Record<string, Health>>({});

  useEffect(() => {
    if (selected && !registries[selected]) {
      setSelected(names[0] || null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [registries]);

  function create() {
    const name = onCreate();
    if (!name) return;
    setJustCreated(name);
    setSelected(name);
    setNameDraft(name);
  }

  function commitName() {
    // 仅新建的记录可改名一次（尚无任何引用）；存量名称不可修改。
    if (!selected || selected !== justCreated) return;
    const error = onRenameNew(selected, nameDraft);
    if (error) {
      notify(error);
      setNameDraft(selected);
      return;
    }
    setSelected(nameDraft.trim());
    setJustCreated(null);
  }

  async function runTest(name: string) {
    const registry = registries[name];
    if (!registry) return;
    setTesting(name);
    try {
      const result = await testRegistry(name, registry);
      setHealth((prev) => ({ ...prev, [name]: result }));
      if (!result.healthy) notify(`Registry ${name} 不可用${result.error ? `：${result.error}` : ""}。`);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) notify("登录已过期，请重新登录。");
      else setHealth((prev) => ({ ...prev, [name]: { healthy: false, error: String(error) } }));
    } finally {
      setTesting(null);
    }
  }

  const selectedDef = selected ? registries[selected] : undefined;

  return (
    <div className="backdrop" onMouseDown={onClose}>
      <div className="mw-modal" role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.stopPropagation()}>
        <div className="drawer-head">
          <div>
            <div className="eyebrow">NACOS REGISTRY</div>
            <h3>{title}</h3>
          </div>
          <div className="drawer-head-actions">
            <button type="button" className="btn small primary" onClick={create}>＋ 新建</button>
            <button type="button" className="btn ghost" onClick={onClose} aria-label="关闭">×</button>
          </div>
        </div>
        <div className="mw-modal-body">
          <div className="mw-tab-panel">
            <div className="mw-instance">
              <div className="mw-instance-list">
                {names.length === 0 && <p className="muted">暂无 Registry，点右上新建。</p>}
                {names.map((name) => {
                  const result = health[name];
                  const reg = registries[name];
                  return (
                    <div key={name} className={`reg-instance-item ${selected === name ? "active" : ""}`}>
                      <button type="button" className="reg-instance-select" onClick={() => { setSelected(name); setJustCreated(null); }}>
                        <strong>{name}</strong>
                        <small>{reg.namespace_id || "public"} · {(reg.servers || []).length} server</small>
                        <small className={result ? (result.healthy ? "ok-text" : "error-text") : "muted"}>
                          {result ? (result.healthy ? `Healthy${result.latency_ms != null ? ` · ${result.latency_ms}ms` : ""}` : "Unhealthy") : "未测试"}
                        </small>
                      </button>
                      <span className="mw-order-actions">
                        <button type="button" disabled={testing === name} onClick={() => runTest(name)}>{testing === name ? "…" : "测试"}</button>
                        {allowSelect && <button type="button" className="btn small primary" onClick={() => onSelect(name)}>选用</button>}
                        <button type="button" className="danger" onClick={() => onDelete(name)}>×</button>
                      </span>
                    </div>
                  );
                })}
              </div>
              <div className="mw-instance-editor">
                {!selected || !selectedDef ? (
                  <p className="muted">左侧选择一个 Registry 编辑连接参数；密码留空表示沿用已保存凭据。</p>
                ) : (
                  <>
                    <Field label="名称" hint={justCreated === selected ? "新建时可改名，确认后不可修改" : "名称创建后不可修改"}>
                      <input value={justCreated === selected ? nameDraft : selected} readOnly={justCreated !== selected} onChange={(e) => setNameDraft(e.target.value)} onBlur={commitName} />
                    </Field>
                    <RegistryForm value={selectedDef} onChange={(def) => onUpdate(selected, def)} />
                  </>
                )}
              </div>
            </div>
          </div>
        </div>
        <div className="drawer-foot">
          <button type="button" className="btn primary" onClick={onClose}>完成</button>
        </div>
      </div>
    </div>
  );
}
