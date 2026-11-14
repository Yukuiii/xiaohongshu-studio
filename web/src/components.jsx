import { useLayoutEffect, useRef } from "react";

export function Dialog({
  open,
  onClose,
  busy,
  id,
  title,
  children,
  className = "",
  closeLabel = "关闭窗口",
}) {
  const ref = useRef(null);
  useLayoutEffect(() => {
    if (open && !ref.current.open) ref.current.showModal();
    if (!open && ref.current.open) ref.current.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      id={id}
      className={`dialog ${className}`.trim()}
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
          aria-label={closeLabel}
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
