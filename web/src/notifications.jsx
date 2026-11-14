import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Slide, ToastContainer, toast } from "react-toastify/unstyled";
import "react-toastify/ReactToastify.css";
import "./notifications.css";

const TOAST_DURATION = 2000;

export function NotificationHost() {
  const [host] = useState(() => {
    const element = document.createElement("div");
    element.className = "toast-portal";
    return element;
  });

  useLayoutEffect(() => {
    const syncTarget = () => {
      const dialogs = document.querySelectorAll("dialog[open]");
      const target = dialogs[dialogs.length - 1] || document.body;
      if (host.parentNode !== target) target.appendChild(host);
    };
    // 原生 dialog 位于顶层，吐司容器需跟随当前弹窗。
    const observer = new MutationObserver(syncTarget);
    observer.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["open"],
    });
    syncTarget();
    return () => {
      observer.disconnect();
      host.remove();
    };
  }, [host]);

  return createPortal(
    <ToastContainer
      position="top-right"
      autoClose={TOAST_DURATION}
      newestOnTop
      hideProgressBar
      closeOnClick={false}
      closeButton={false}
      pauseOnHover={false}
      pauseOnFocusLoss={false}
      draggable={false}
      transition={Slide}
      ariaLabel="操作提示"
    />,
    host,
  );
}

export function ToastNotice({ notice, active = true }) {
  const currentToast = useRef(null);
  useEffect(() => {
    if (!active || !notice?.message) {
      if (currentToast.current !== null) toast.dismiss(currentToast.current);
      currentToast.current = null;
      return;
    }
    const loading = notice.type === "busy";
    const type = loading ? "default" : notice.type || "success";
    const urgent = type === "error" || type === "warning";
    const options = {
      type,
      role: urgent ? "alert" : "status",
      isLoading: loading,
      autoClose: loading ? false : TOAST_DURATION,
    };
    if (currentToast.current !== null && toast.isActive(currentToast.current))
      toast.update(currentToast.current, {
        ...options,
        render: notice.message,
        delay: 0,
      });
    else currentToast.current = toast(notice.message, options);
  }, [active, notice]);

  useEffect(
    () => () => {
      if (currentToast.current !== null) toast.dismiss(currentToast.current);
    },
    [],
  );

  return null;
}
