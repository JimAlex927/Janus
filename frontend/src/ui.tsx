import { useEffect, useId, useRef, type ReactNode } from "react";

/** 通用编辑对话框：所有资源编辑器共用一致的确认、取消和关闭语义。 */
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
  const titleId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      const dialogs = document.querySelectorAll<HTMLElement>('[role="dialog"]');
      if (dialogs[dialogs.length - 1] !== dialogRef.current) return;
      event.preventDefault();
      onCancel();
    };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [onCancel]);
  return (
    <div className="backdrop" onMouseDown={onCancel}>
      <div
        ref={dialogRef}
        className="drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="drawer-head">
          <div>
            <div className="eyebrow">JANUS CONFIG</div>
            <h3 id={titleId}>{title}</h3>
            {subtitle && <p>{subtitle}</p>}
          </div>
          <div className="drawer-head-actions">
            {onDelete && !hideDelete && (
              <button type="button" className="btn danger" onClick={onDelete}>
                删除
              </button>
            )}
            <button type="button" className="icon-button" onClick={onClose} aria-label="关闭">
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

export function Badge({ text, variant = "default" }: { text: string; variant?: "default" | "success" | "warn" | "danger" }) {
  return <em className={`badge badge-${variant}`}>{text}</em>;
}

export function StatCard({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div className="stat-card">
      <span>{label}</span>
      <strong>{value}</strong>
      {sub != null && sub !== "" && <small className="muted">{sub}</small>}
    </div>
  );
}
