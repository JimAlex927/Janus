import { useCallback, useEffect, useId, useRef, useState, type ReactNode, type RefObject } from "react";

/**
 * Shared dialog lifecycle. Nested editors use the same behavior: the page
 * behind the active dialog cannot scroll, Escape only closes the top dialog,
 * and focus returns to the control that opened it.
 */
export function useDialogLifecycle(dialogRef: RefObject<HTMLElement>, onCancel: () => void) {
  const cancelRef = useRef(onCancel);
  cancelRef.current = onCancel;

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    dialogRef.current?.focus();

    const handleKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      const dialogs = document.querySelectorAll<HTMLElement>('[role="dialog"]');
      if (dialogs[dialogs.length - 1] !== dialogRef.current) return;
      event.preventDefault();
      cancelRef.current();
    };
    window.addEventListener("keydown", handleKey);
    return () => {
      window.removeEventListener("keydown", handleKey);
      document.body.style.overflow = previousOverflow;
      previous?.focus();
    };
  }, [dialogRef]);
}

type DialogTone = "neutral" | "warning" | "danger";
type DialogIcon = "copy" | "upload" | "plus" | "trash" | "publish" | "warning" | "edit";

type ConfirmDialogOptions = {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: DialogTone;
  icon?: DialogIcon;
  context?: string;
};

type PromptDialogOptions = ConfirmDialogOptions & {
  initialValue?: string;
  placeholder?: string;
  inputLabel?: string;
  inputHint?: string;
};

type DialogRequest =
  | ({ kind: "confirm" } & ConfirmDialogOptions)
  | ({ kind: "prompt" } & PromptDialogOptions);

/** Native confirm/prompt replacements with one consistent visual and keyboard contract. */
export function useDialogController() {
  const [request, setRequest] = useState<DialogRequest | null>(null);
  const resolver = useRef<((value: boolean | string | null) => void) | null>(null);

  const finish = useCallback((value: boolean | string | null) => {
    const resolve = resolver.current;
    resolver.current = null;
    setRequest(null);
    resolve?.(value);
  }, []);

  const confirm = useCallback((options: ConfirmDialogOptions) => new Promise<boolean>((resolve) => {
    resolver.current = (value) => resolve(Boolean(value));
    setRequest({ kind: "confirm", ...options });
  }), []);

  const prompt = useCallback((options: PromptDialogOptions) => new Promise<string | null>((resolve) => {
    resolver.current = (value) => resolve(typeof value === "string" ? value : null);
    setRequest({ kind: "prompt", ...options });
  }), []);

  const dialog = request ? (
    <Dialog
      request={request}
      onCancel={() => finish(request.kind === "confirm" ? false : null)}
      onConfirm={(value) => finish(request.kind === "confirm" ? true : value ?? "")}
    />
  ) : null;

  return { confirm, prompt, dialog };
}

function Dialog({
  request,
  onCancel,
  onConfirm,
}: {
  request: DialogRequest;
  onCancel: () => void;
  onConfirm: (value?: string) => void;
}) {
  const titleId = useId();
  const messageId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const [value, setValue] = useState(request.kind === "prompt" ? request.initialValue || "" : "");
  useDialogLifecycle(dialogRef, onCancel);

  useEffect(() => {
    if (request.kind === "prompt") inputRef.current?.select();
  }, [request.kind]);

  const tone = request.tone || "neutral";
  const isPrompt = request.kind === "prompt";
  const icon = request.icon || inferDialogIcon(request.title, tone);
  const context = request.context || inferDialogContext(icon, request.kind);
  return (
    <div className="dialog-backdrop" onMouseDown={onCancel}>
      <div
        ref={dialogRef}
        className={`decision-dialog decision-dialog-${tone}`}
        role={tone === "danger" ? "alertdialog" : "dialog"}
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={messageId}
        tabIndex={-1}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <form onSubmit={(event) => { event.preventDefault(); onConfirm(value); }}>
          <div className="decision-dialog-main">
            <span className="decision-dialog-mark" aria-hidden="true"><DialogGlyph icon={icon} /></span>
            <div>
              <div className="decision-dialog-kicker"><span>JANUS CONTROL</span><i>{context}</i></div>
              <h3 id={titleId}>{request.title}</h3>
              <p id={messageId}>{request.message}</p>
            </div>
            <button type="button" className="icon-button decision-dialog-close" onClick={onCancel} aria-label="关闭">×</button>
          </div>
          {isPrompt && (
            <label className="decision-dialog-field">
              <span>{request.inputLabel || "名称"}</span>
              <input
                ref={inputRef}
                className="decision-dialog-input"
                value={value}
                placeholder={request.placeholder}
                onChange={(event) => setValue(event.target.value)}
                aria-label={request.inputLabel || "名称"}
                autoComplete="off"
              />
              {request.inputHint && <small>{request.inputHint}</small>}
            </label>
          )}
          <div className="decision-dialog-foot">
            <button type="button" className="btn ghost" onClick={onCancel}>{request.cancelLabel || "取消"}</button>
            <button type="submit" className={`btn ${tone === "danger" ? "danger" : "primary"}`}>
              {request.confirmLabel || (isPrompt ? "继续" : "确认")}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

export function Toast({ message, onClose }: { message: string; onClose: () => void }) {
  const tone = /失败|错误|未通过|不可用|冲突|拒绝|不存在/.test(message)
    ? "error"
    : /注意|警告|未保存|重启|脱敏|丢失/.test(message)
      ? "warning"
      : "success";
  const mark = tone === "error" ? "!" : tone === "warning" ? "!" : "✓";
  return (
    <div className={`toast toast-${tone}`} role="status" aria-live="polite">
      <span className="toast-mark" aria-hidden="true">{mark}</span>
      <span className="toast-message">{message}</span>
      <button type="button" className="toast-close" onClick={onClose} aria-label="关闭提示">×</button>
    </div>
  );
}

function inferDialogIcon(title: string, tone: DialogTone): DialogIcon {
  if (/复制/.test(title)) return "copy";
  if (/导入/.test(title)) return "upload";
  if (/新建|创建/.test(title)) return "plus";
  if (/删除/.test(title)) return "trash";
  if (/发布|写入/.test(title)) return "publish";
  if (/离开|放弃|丢失/.test(title)) return "warning";
  return tone === "danger" || tone === "warning" ? "warning" : "edit";
}

function inferDialogContext(icon: DialogIcon, kind: DialogRequest["kind"]): string {
  if (icon === "copy") return "DUPLICATE";
  if (icon === "upload") return "IMPORT";
  if (icon === "plus") return "CREATE";
  if (icon === "trash") return "DELETE";
  if (icon === "publish") return "PUBLISH";
  if (icon === "warning") return "REVIEW";
  return kind === "prompt" ? "EDIT" : "CONFIRM";
}

function DialogGlyph({ icon }: { icon: DialogIcon }) {
  const paths: Record<DialogIcon, ReactNode> = {
    copy: <><rect x="7" y="7" width="10" height="10" rx="2" /><path d="M10 7V5.5A1.5 1.5 0 0 1 11.5 4h7A1.5 1.5 0 0 1 20 5.5v7a1.5 1.5 0 0 1-1.5 1.5H17" /></>,
    upload: <><path d="M12 15V4" /><path d="m8 8 4-4 4 4" /><path d="M5 14v3.5A1.5 1.5 0 0 0 6.5 19h11a1.5 1.5 0 0 0 1.5-1.5V14" /></>,
    plus: <><path d="M12 5v14M5 12h14" /></>,
    trash: <><path d="M5 7h14M10 11v5M14 11v5" /><path d="m8 7 .7-2h6.6l.7 2M7 7l.7 12h8.6L17 7" /></>,
    publish: <><path d="M5 12h13" /><path d="m13 6 6 6-6 6" /></>,
    warning: <><path d="M12 4 21 19H3L12 4Z" /><path d="M12 9v4M12 16h.01" /></>,
    edit: <><path d="m5 16-.8 3.8L8 19l10.5-10.5a2.1 2.1 0 0 0-3-3L5 16Z" /><path d="m14 7 3 3" /></>,
  };
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{paths[icon]}</svg>;
}

/** 通用编辑对话框：所有资源编辑器共用一致的确认、取消和关闭语义。 */
export function Drawer({
  title,
  subtitle,
  className = "",
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
  className?: string;
  onClose: () => void;
  onConfirm: () => void;
  onCancel: () => void;
  confirmLabel?: string;
  hideDelete?: boolean;
  onDelete?: () => void;
  children: ReactNode;
}) {
  const titleId = useId();
  const subtitleId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  useDialogLifecycle(dialogRef, onCancel);
  return (
    <div className="backdrop" onMouseDown={onCancel}>
      <div
        ref={dialogRef}
        className={`drawer ${className}`.trim()}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={subtitle ? subtitleId : undefined}
        tabIndex={-1}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="drawer-head">
          <div>
            <div className="eyebrow">JANUS CONFIG</div>
            <h3 id={titleId}>{title}</h3>
            {subtitle && <p id={subtitleId}>{subtitle}</p>}
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
