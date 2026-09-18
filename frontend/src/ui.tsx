import type { ReactNode } from "react";

/** 通用抽屉弹窗：所有编辑器共用，保证交互一致、一次修好所有页面。 */
export function Drawer({
  title,
  subtitle,
  onClose,
  onConfirm,
  onCancel,
  confirmLabel = "确认",
  hideDelete = false,
  onDelete,
  children,
}: {
  title: string;
  subtitle?: string;
  onClose: () => void;
  onConfirm: () => void;
  onCancel: () => void;
  confirmLabel?: string;
  hideDelete?: boolean;
  onDelete?: () => void;
  children: ReactNode;
}) {
  return (
    <div className="backdrop" onMouseDown={onClose}>
      <div
        className="drawer"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="drawer-head">
          <div>
            <div className="eyebrow">JANUS CONFIG</div>
            <h3>{title}</h3>
            {subtitle && <p>{subtitle}</p>}
          </div>
          <div className="drawer-head-actions">
            {onDelete && !hideDelete && (
              <button type="button" className="btn danger" onClick={onDelete}>
                删除
              </button>
            )}
            <button type="button" className="btn ghost" onClick={onClose} aria-label="关闭">
              ×
            </button>
          </div>
        </div>
        <div className="drawer-body">{children}</div>
        <div className="drawer-foot">
          <button type="button" className="btn ghost" onClick={onCancel}>
            取消
          </button>
          <button type="button" className="btn primary" onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="field-label">
        {label}
        {hint && <small>{hint}</small>}
      </span>
      {children}
      {error && <small className="field-error">{error}</small>}
    </label>
  );
}

export function Empty({ text, action }: { text: string; action?: ReactNode }) {
  return (
    <div className="empty">
      <p>{text}</p>
      {action}
    </div>
  );
}

export function Badge({ text }: { text: string }) {
  return <em className="badge">{text}</em>;
}
