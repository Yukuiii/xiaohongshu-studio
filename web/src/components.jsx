import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { ToastNotice } from "./notifications.jsx";

export function Dialog({
  open,
  onClose,
  busy,
  id,
  title,
  children,
  className = "",
  closeLabel = "关闭窗口",
  closeOnBackdrop = false,
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
      onClick={(event) => {
        if (closeOnBackdrop && !busy && event.target === event.currentTarget) {
          onClose();
        }
      }}
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
  figureClassName = "publish-result-thumb",
  errorClassName = "publish-result-image-error",
  loadingText = "加载中…",
  caption = "",
  onPreview = null,
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

  const imageNode = (
    <img
      src={preview.url}
      alt={alt}
      onError={() => setPreview({ url: "", error: "图片无法解码，请刷新重试。" })}
    />
  );

  return (
    <figure className={`${figureClassName}${className ? ` ${className}` : ""}`}>
      {preview.error ? (
        <p className={errorClassName}>{preview.error}</p>
      ) : preview.url ? (
        onPreview ? (
          <button
            type="button"
            className="publish-thumb-button"
            title="点击查看大图"
            aria-label={`查看大图：${alt}`}
            onClick={() => onPreview({ url: preview.url, alt })}
          >
            {imageNode}
          </button>
        ) : (
          imageNode
        )
      ) : (
        <p className="muted">{loadingText}</p>
      )}
      {caption && <figcaption className="schedule-identifiers muted">{caption}</figcaption>}
    </figure>
  );
}

function publishedNoteURL(result) {
  if (typeof result?.note_url !== "string") return "";
  try {
    const url = new URL(result.note_url);
    const match = /^\/explore\/([a-zA-Z0-9]+)$/.exec(url.pathname);
    if (url.protocol !== "https:" || url.hostname !== "www.xiaohongshu.com" || url.port || url.username || url.password || !match) return "";
    if (result.note_id && result.note_id !== match[1]) return "";
    return url.href;
  } catch {
    return "";
  }
}

export function PublishedNoteLink({ result, active = true }) {
  const url = publishedNoteURL(result);
  const error = typeof result?.note_link_error === "string" ? result.note_link_error.trim() : "";
  const [notice, setNotice] = useState(null);
  const lifecycle = useRef(0);
  useLayoutEffect(() => {
    lifecycle.current += 1;
    setNotice(null);
    return () => { lifecycle.current += 1; };
  }, [active, url]);

  const copy = async () => {
    if (!active) return;
    const version = lifecycle.current;
    try {
      await navigator.clipboard.writeText(url);
      if (lifecycle.current === version) setNotice({ type: "success", message: "链接已复制" });
    } catch {
      if (lifecycle.current === version) setNotice({ type: "error", message: "复制链接失败，请打开笔记后复制地址栏链接。" });
    }
  };

  if (!url) return error ? <p className="muted">已发布但未获取到链接：{error}</p> : null;
  return (
    <>
      <div className="button-group">
        <a className="button" href={url} target="_blank" rel="noopener noreferrer">打开笔记</a>
        <button className="button" type="button" disabled={!active} onClick={copy}>复制链接</button>
      </div>
      <ToastNotice active={active} notice={notice} />
    </>
  );
}

export function PublishResult({ result, images, api, token, accountId, active = true }) {
  const imageList = Array.isArray(images) ? images.filter(Boolean) : [];
  const ownerAccountId = accountId || result?.account_id;
  const showImages = imageList.length > 0 && active && Boolean(api) && Boolean(ownerAccountId);
  const [lightbox, setLightbox] = useState(null);
  const dialogId = useId();
  const imageKey = imageList.join(",");

  useEffect(() => {
    if (!showImages) setLightbox(null);
  }, [showImages, imageKey]);

  const thumbGrid = showImages ? (
    <div className="publish-result-thumbs">
      {imageList.map((id, index) => (
        <StoredImage
          key={id}
          id={id}
          api={api}
          token={token}
          accountId={ownerAccountId}
          alt={`任务配图 ${index + 1}`}
          onPreview={setLightbox}
        />
      ))}
    </div>
  ) : null;

  const lightboxDialog = (
    <Dialog
      open={Boolean(lightbox)}
      onClose={() => setLightbox(null)}
      id={dialogId}
      title={lightbox?.alt || "查看配图"}
      className="image-lightbox-dialog"
      closeOnBackdrop
    >
      {lightbox && (
        <div className="image-lightbox-body">
          <img className="image-lightbox-img" src={lightbox.url} alt={lightbox.alt} />
        </div>
      )}
    </Dialog>
  );

  const standaloneImageSection = thumbGrid ? (
    <div>
      <span className="publish-result-label">配图（{imageList.length} 张）</span>
      {thumbGrid}
    </div>
  ) : null;

  if (result == null) {
    return standaloneImageSection ? (
      <div className="publish-result">
        {standaloneImageSection}
        {lightboxDialog}
      </div>
    ) : null;
  }
  if (typeof result === "string") {
    return (
      <div className="publish-result">
        <div className="publish-result-prose">{result}</div>
        {standaloneImageSection}
        {lightboxDialog}
      </div>
    );
  }
  if (typeof result !== "object" || Array.isArray(result)) {
    return (
      <div className="publish-result">
        <pre className="publish-result-raw">{JSON.stringify(result, null, 2)}</pre>
        {standaloneImageSection}
        {lightboxDialog}
      </div>
    );
  }

  const meta = [];
  if (result.status) meta.push(["发布状态", result.status]);
  if (result.title) meta.push(["笔记标题", result.title]);
  if (!showImages && typeof result.images === "number") meta.push(["配图数量", `${result.images} 张`]);
  if (result.video) meta.push(["视频文件", result.video]);
  if (Array.isArray(result.at_users) && result.at_users.length > 0) {
    meta.push(["@用户", result.at_users.join("、")]);
  }
  const hasContent = typeof result.content === "string" && result.content.length > 0;
  const hasMeta = meta.length > 0 || showImages;

  if (!hasMeta && !hasContent) {
    return (
      <div className="publish-result">
        <pre className="publish-result-raw">{JSON.stringify(result, null, 2)}</pre>
      </div>
    );
  }

  return (
    <div className="publish-result">
      {hasMeta && (
        <dl className="publish-result-meta">
          {meta.map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
          {showImages && (
            <div className="publish-result-meta-images">
              <dt>配图（{imageList.length} 张）</dt>
              <dd>{thumbGrid}</dd>
            </div>
          )}
        </dl>
      )}
      {hasContent && (
        <div className={`publish-result-content${!hasMeta ? " standalone" : ""}`}>
          <span className="publish-result-label">正文内容</span>
          <div className="publish-result-prose">{result.content}</div>
        </div>
      )}
      {lightboxDialog}
    </div>
  );
}
