import { useLayoutEffect, useRef } from "react";

export function Notice({ notice, id }) {
  if (!notice?.message) return null;
  return (
    <div
      id={id}
      className={`notice ${notice.type || ""}`}
      role="status"
      aria-live="polite"
    >
      {notice.message}
    </div>
  );
}

export function Dialog({ open, onClose, busy, id, title, children }) {
  const ref = useRef(null);
  useLayoutEffect(() => {
    if (open && !ref.current.open) ref.current.showModal();
    if (!open && ref.current.open) ref.current.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      id={id}
      className="dialog"
      aria-labelledby={`${id}-title`}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
    >
      <div className="dialog-header">
        <h2 id={`${id}-title`}>{title}</h2>
        <button
          type="button"
          className="icon-button"
          aria-label="关闭窗口"
          disabled={busy}
          onClick={onClose}
        >
          ×
        </button>
      </div>
      {children}
    </dialog>
  );
}
