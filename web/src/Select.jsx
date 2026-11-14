import { Children, isValidElement, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import "./select.css";

export default function Select({ id, value, onChange, children, disabled = false, required = false, name }) {
  const generatedId = useId();
  const controlId = id || `select-${generatedId}`;
  const menuId = `${controlId}-options`;
  const items = Children.toArray(children).filter(isValidElement).map((option) => ({
    value: String(option.props.value ?? option.props.children),
    label: option.props.children,
    disabled: Boolean(option.props.disabled),
  }));
  const selected = items.findIndex((item) => item.value === String(value ?? ""));
  const trigger = useRef(null);
  const native = useRef(null);
  const menu = useRef(null);
  const search = useRef({ text: "", time: 0 });
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(selected);
  const [position, setPosition] = useState(null);
  const [invalid, setInvalid] = useState(false);

  const close = () => { setOpen(false); setPosition(null); search.current = { text: "", time: 0 }; };
  const enabled = () => !trigger.current?.matches(":disabled");
  const show = (index = selected) => {
    if (!enabled()) return;
    setActive(index >= 0 && !items[index]?.disabled ? index : items.findIndex((item) => !item.disabled));
    setOpen(true);
  };
  const choose = (index) => {
    const item = items[index];
    if (!item || item.disabled || !enabled()) return;
    // 沿用原生表单的 change 事件与校验，展开菜单由工作台绘制。
    if (native.current.value !== item.value) {
      native.current.value = item.value;
      native.current.dispatchEvent(new Event("change", { bubbles: true }));
    }
    close();
    trigger.current?.focus({ preventScroll: true });
  };

  useLayoutEffect(() => {
    if (open && trigger.current.matches(":disabled")) close();
  });

  useLayoutEffect(() => {
    if (!open) return;
    const updatePosition = (event) => {
      if (event?.type === "scroll" && menu.current?.contains(event.target)) return;
      const button = trigger.current;
      const list = menu.current;
      if (!button || !list) return;
      const rect = button.getBoundingClientRect();
      if (!button.isConnected || button.closest("[hidden]")) { close(); return; }
      const edge = 12;
      const gap = 6;
      const width = Math.min(rect.width, window.innerWidth - edge * 2);
      list.style.width = `${width}px`;
      const below = window.innerHeight - rect.bottom - edge - gap;
      const above = rect.top - edge - gap;
      const side = below >= Math.min(list.scrollHeight, 280) || below >= above ? "bottom" : "top";
      const maxHeight = Math.max(40, Math.min(280, side === "bottom" ? below : above));
      const height = Math.min(list.scrollHeight, maxHeight);
      setPosition({
        width, maxHeight, side,
        left: Math.max(edge, Math.min(rect.left, window.innerWidth - width - edge)),
        top: Math.max(edge, side === "bottom" ? rect.bottom + gap : rect.top - gap - height),
      });
    };
    const outside = (event) => {
      if (!trigger.current?.contains(event.target) && !menu.current?.contains(event.target)) close();
    };
    const hide = () => { if (document.hidden) close(); };
    updatePosition();
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("focusin", outside);
    document.addEventListener("visibilitychange", hide);
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    return () => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("focusin", outside);
      document.removeEventListener("visibilitychange", hide);
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [open, children]);

  useLayoutEffect(() => {
    if (!open || !position) return;
    const list = menu.current;
    const option = list?.querySelector(`[data-index="${active}"]`);
    if (!option) return;
    if (option.offsetTop < list.scrollTop) list.scrollTop = option.offsetTop;
    else if (option.offsetTop + option.offsetHeight > list.scrollTop + list.clientHeight)
      list.scrollTop = option.offsetTop + option.offsetHeight - list.clientHeight;
  }, [active, open, Boolean(position)]);

  const onKeyDown = (event) => {
    if (event.nativeEvent.isComposing || !enabled()) return;
    if (event.key === "Tab") { close(); return; }
    if (event.key === "Escape" && open) {
      event.preventDefault();
      event.stopPropagation();
      close();
      return;
    }
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      if (open) choose(active); else show();
      return;
    }
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const indexes = items.flatMap((item, index) => item.disabled ? [] : [index]);
      if (!indexes.length) return;
      let next;
      if (event.key === "Home") next = indexes[0];
      else if (event.key === "End") next = indexes.at(-1);
      else if (!open) next = selected >= 0 && !items[selected]?.disabled ? selected : event.key === "ArrowUp" ? indexes.at(-1) : indexes[0];
      else {
        const direction = event.key === "ArrowDown" ? 1 : -1;
        const current = indexes.indexOf(active);
        const start = current >= 0 ? current : direction > 0 ? -1 : 0;
        next = indexes[(start + direction + indexes.length) % indexes.length];
      }
      if (!open) show(next); else setActive(next);
      return;
    }
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      const now = Date.now();
      const key = event.key.toLocaleLowerCase();
      const text = now - search.current.time < 700 ? search.current.text + key : key;
      search.current = { text, time: now };
      const prefix = [...text].every((character) => character === key) ? key : text;
      const start = open ? active : selected;
      for (let offset = 1; offset <= items.length; offset++) {
        const index = (start + offset + items.length) % items.length;
        if (!items[index].disabled && native.current.options[index].text.toLocaleLowerCase().startsWith(prefix)) {
          event.preventDefault();
          if (!open) show(index); else setActive(index);
          break;
        }
      }
    }
  };

  return (
    <div className="select-field">
      <select
        ref={native} id={`${controlId}-native`} name={name} className="select-native"
        value={value} disabled={disabled} required={required} tabIndex={-1} aria-hidden="true"
        onChange={(event) => { setInvalid(false); onChange?.(event); }}
        onInvalid={(event) => { event.preventDefault(); setInvalid(true); trigger.current?.focus(); show(); }}
      >{children}</select>
      <button
        ref={trigger} id={controlId} type="button" role="combobox" className="select-trigger"
        disabled={disabled} aria-haspopup="listbox" aria-expanded={open} aria-controls={menuId}
        aria-required={required || undefined} aria-invalid={invalid || undefined}
        aria-activedescendant={open && active >= 0 ? `${menuId}-${active}` : undefined}
        data-open={open || undefined} data-placeholder={selected < 0 || items[selected].value === "" || undefined}
        onClick={() => open ? close() : show()} onKeyDown={onKeyDown}
      >
        <span className="select-value">{items[selected]?.label ?? "请选择"}</span>
        <svg className="select-chevron" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m6 8 4 4 4-4" /></svg>
      </button>
      {open && createPortal(
        <div
          ref={menu} id={menuId} role="listbox" className="select-menu" data-side={position?.side}
          aria-label={Array.from(trigger.current?.labels || [], (label) => label.textContent).join(" ") || "选项"}
          style={position ? { left: position.left, top: position.top, width: position.width, maxHeight: position.maxHeight } : { visibility: "hidden" }}
        >
          {items.map((item, index) => (
            <div
              key={item.value} id={`${menuId}-${index}`} role="option" className="select-option" data-index={index}
              aria-selected={index === selected} aria-disabled={item.disabled || undefined} data-active={index === active || undefined}
              onPointerMove={() => { if (!item.disabled) setActive(index); }}
              onMouseDown={(event) => event.preventDefault()} onClick={() => choose(index)}
            >
              <span>{item.label}</span>
              {index === selected && <svg viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m4.5 10 3.5 3.5 7.5-7.5" /></svg>}
            </div>
          ))}
        </div>,
        trigger.current?.closest("dialog[open]") || document.body,
      )}
    </div>
  );
}
