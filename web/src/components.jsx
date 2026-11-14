import { useEffect, useLayoutEffect, useRef, useState } from "react";

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

// 受管工作台图片加载，按页面可见性与销毁周期自动取消请求并释放 Blob URL
export function StoredImage({
  id,
  api,
  token,
  accountId,
  alt,
  className = "",
  figureClassName = "publish-result-image",
  errorClassName = "publish-result-image-error",
  loadingText = "正在读取配图…",
  caption = "",
}) {
  const [preview, setPreview] = useState({ url: "", error: "" });
  const latest = useRef(null);
  latest.current = { id, token, accountId };
  useEffect(() => {
    let disposed = false;
    let controller;
    let objectURL = "";
    const suspend = () => {
      controller?.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
      objectURL = "";
    };
    const load = async () => {
      suspend();
      if (disposed || document.hidden) return;
      const request = new AbortController();
      controller = request;
      const valid = () => !disposed && !request.signal.aborted && controller === request &&
        latest.current.id === id && latest.current.token === token && latest.current.accountId === accountId;
      setPreview({ url: "", error: "" });
      try {
        const blob = await api(`/images/${encodeURIComponent(id)}`, { token, accountId, blob: true, signal: request.signal });
        if (!valid()) return;
        if (!blob.type.startsWith("image/")) throw new Error("图片格式无法预览");
        objectURL = URL.createObjectURL(blob);
        setPreview({ url: objectURL, error: "" });
      } catch (error) {
        if (valid()) setPreview({ url: "", error: `图片读取失败：${error.message}` });
      }
    };
    const visibility = () => document.hidden ? suspend() : load();
    load();
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("pagehide", suspend);
    window.addEventListener("pageshow", load);
    return () => {
      disposed = true;
      suspend();
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("pagehide", suspend);
      window.removeEventListener("pageshow", load);
    };
  }, [api, id, token, accountId]);

  return (
    <figure className={`${figureClassName}${className ? ` ${className}` : ""}`}>
      {preview.error ? (
        <p className={errorClassName}>{preview.error}</p>
      ) : preview.url ? (
        <img src={preview.url} alt={alt} onError={() => setPreview({ url: "", error: "图片无法解码，请刷新重试。" })} />
      ) : (
        <p className="muted">{loadingText}</p>
      )}
      {caption && <figcaption className="schedule-identifiers muted">{caption}</figcaption>}
    </figure>
  );
}

export function PublishResult({ result, images, api, token, accountId, active = true }) {
  const imageList = Array.isArray(images) ? images.filter(Boolean) : [];
  const ownerAccountId = accountId || result?.account_id;
  const showImages = imageList.length > 0 && active && Boolean(api) && Boolean(ownerAccountId);

  const imageSection = showImages ? (
    <div className="publish-result-images">
      <span className="publish-result-label">配图（{imageList.length} 张）</span>
      <div className="publish-result-image-grid">
        {imageList.map((id, index) => (
          <StoredImage
            key={id}
            id={id}
            api={api}
            token={token}
            accountId={ownerAccountId}
            alt={`任务配图 ${index + 1}`}
          />
        ))}
      </div>
    </div>
  ) : null;

  if (result == null) {
    return imageSection ? <div className="publish-result">{imageSection}</div> : null;
  }
  if (typeof result === "string") {
    return (
      <div className="publish-result">
        <div className="publish-result-prose">{result}</div>
        {imageSection}
      </div>
    );
  }
  if (typeof result !== "object" || Array.isArray(result)) {
    return (
      <div className="publish-result">
        <pre className="publish-result-raw">{JSON.stringify(result, null, 2)}</pre>
        {imageSection}
      </div>
    );
  }

  const meta = [];
  if (result.status) meta.push(["发布状态", result.status]);
  if (result.title) meta.push(["笔记标题", result.title]);
  if (typeof result.images === "number") meta.push(["配图数量", `${result.images} 张`]);
  if (result.video) meta.push(["视频文件", result.video]);
  if (Array.isArray(result.at_users) && result.at_users.length > 0) {
    meta.push(["@用户", result.at_users.join("、")]);
  }
  if (result.account_id) meta.push(["账号编号", result.account_id]);
  const hasContent = typeof result.content === "string" && result.content.length > 0;

  if (meta.length === 0 && !hasContent && !imageSection) {
    return (
      <div className="publish-result">
        <pre className="publish-result-raw">{JSON.stringify(result, null, 2)}</pre>
      </div>
    );
  }

  return (
    <div className="publish-result">
      {meta.length > 0 && (
        <dl className="publish-result-meta">
          {meta.map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {hasContent && (
        <div className={`publish-result-content${meta.length === 0 ? " standalone" : ""}`}>
          <span className="publish-result-label">正文内容</span>
          <div className="publish-result-prose">{result.content}</div>
        </div>
      )}
      {imageSection}
    </div>
  );
}
