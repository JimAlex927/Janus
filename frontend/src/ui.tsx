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

type ConfirmDialogOptions = {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: DialogTone;
};

type PromptDialogOptions = ConfirmDialogOptions & {
  initialValue?: string;
  placeholder?: string;
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
  return (
    <div className="dialog-backdrop" onMouseDown={onCancel}>
      <div
        ref={dialogRef}
        className={`decision-dialog decision-dialog-${tone}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={messageId}
        tabIndex={-1}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <form onSubmit={(event) => { event.preventDefault(); onConfirm(value); }}>
          <div className="decision-dialog-main">
            <span className="decision-dialog-mark" aria-hidden="true">{tone === "danger" ? "!" : "?"}</span>
            <div>
              <div className="eyebrow">JANUS CONTROL</div>
              <h3 id={titleId}>{request.title}</h3>
              <p id={messageId}>{request.message}</p>
            </div>
            <button type="button" className="icon-button decision-dialog-close" onClick={onCancel} aria-label="关闭">×</button>
          </div>
          {isPrompt && (
            <input
              ref={inputRef}
              className="decision-dialog-input"
              value={value}
              placeholder={request.placeholder}
              onChange={(event) => setValue(event.target.value)}
              aria-label="输入内容"
              autoComplete="off"
            />
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
  const subtitleId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  useDialogLifecycle(dialogRef, onCancel);
  return (
    <div className="backdrop" onMouseDown={onCancel}>
      <div
        ref={dialogRef}
        className="drawer"
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
