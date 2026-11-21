import { useEffect, useRef, useState } from "react";
import { Dialog } from "./components.jsx";
import "./desktop-update.css";

const phaseLabels = {
  checking: "正在检查更新…",
  current: "已是最新版本。",
  available: "",
  downloading: "正在下载更新…",
  ready: "下载并校验完成，可以安装。",
  installing: "正在退出应用并安装更新…",
  error: "",
};

export default function DesktopUpdate({ api, token, busy }) {
  const [snapshot, setSnapshot] = useState(null);
  const [requestError, setRequestError] = useState("");
  const [pending, setPending] = useState("");
  const [confirmInstall, setConfirmInstall] = useState(false);
  const request = useRef(null);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    let phase = "";
    let timer;
    let inFlight = false;

    // 串行读取和操作，离开设置后停止轮询。
    async function run(action = "status") {
      if (signal.aborted || inFlight) return;
      clearTimeout(timer);
      inFlight = true;
      const status = action === "status";
      setPending(action);
      if (!status) setRequestError("");
      try {
        const data = await api(
          status ? "/app-update" : `/app-update/${action}`,
          { method: status ? "GET" : "POST", token, signal },
        );
        if (signal.aborted) return;
        if (data?.current_version && Object.hasOwn(phaseLabels, data.phase)) {
          phase = data.phase;
          setSnapshot(data);
        } else if (!status || phase) {
          throw new Error("服务返回了无法识别的更新状态。");
        }
        setRequestError("");
      } catch (error) {
        if (signal.aborted) return;
        if (status && error?.status === 404) {
          phase = "";
          setSnapshot(null);
          setRequestError("");
        } else {
          setRequestError(error?.message || "无法获取更新状态，请稍后重试。");
        }
      } finally {
        inFlight = false;
        if (!signal.aborted) {
          setPending("");
          if (phase === "checking" || phase === "downloading") {
            timer = setTimeout(run, 1000);
          }
        }
      }
    }

    request.current = run;
    run();
    return () => {
      request.current = null;
      controller.abort();
      clearTimeout(timer);
    };
  }, [api, token]);

  if (!snapshot && !requestError) return null;

  const phase =
    pending === "check"
      ? "checking"
      : pending === "download"
        ? "downloading"
        : snapshot?.phase;
  const downloading = phase === "downloading";
  const ready = phase === "ready";
  const disabled =
    busy || Boolean(pending) || downloading ||
    phase === "checking" || phase === "installing";
  const size = snapshot?.size || 0;
  const downloaded = pending === "download" ? 0 : snapshot?.downloaded || 0;
  const percent = size > 0
    ? Math.floor(Math.min(1, downloaded / size) * 100)
    : null;
  const status = pending === "install" ? "正在请求安装…" : phaseLabels[phase];
  const updateError = snapshot?.message || snapshot?.install_error ||
    (phase === "error" ? "检查更新失败，请重试。" : "");
  const error = requestError || ((!pending || pending === "status") && updateError);

  return (
    <section className="card settings-form app-update" aria-labelledby="app-update-title">
      <div className="section-heading">
        <h2 id="app-update-title">应用更新</h2>
      </div>
      <div className="app-update-details">
        {snapshot && <p className="muted">当前版本 {snapshot.current_version}</p>}
        {snapshot?.version && <p className="muted">可用版本 {snapshot.version}</p>}
        {status && (
          <p className="muted" role="status">
            {status}
            {downloading && percent !== null && ` ${percent}%`}
          </p>
        )}
        {downloading && (
          <progress
            aria-label="下载进度"
            max={size > 0 ? size : 1}
            value={percent === null ? undefined : downloaded}
          />
        )}
        {error && <p className="muted app-update-error" role="alert">{error}</p>}
        {busy && ready && (
          <p className="muted" role="status">
            工作台正在处理任务，请在任务完成后安装。
          </p>
        )}
      </div>
      <div className="button-group app-update-actions">
        <button
          type="button"
          className="button"
          disabled={disabled}
          onClick={() => request.current?.(snapshot ? "check" : "status")}
        >
          {snapshot ? "检查更新" : pending ? "正在读取…" : "重试"}
        </button>
        {phase === "available" && (
          <button
            type="button"
            className="button primary"
            disabled={disabled}
            onClick={() => request.current?.("download")}
          >
            {snapshot.message ? "重新下载" : "下载更新"}
          </button>
        )}
        {ready && (
          <button
            type="button"
            className="button primary"
            disabled={disabled}
            onClick={() => setConfirmInstall(true)}
          >
            安装更新
          </button>
        )}
      </div>
      <Dialog
        open={confirmInstall && ready}
        onClose={() => setConfirmInstall(false)}
        id="install-app-update"
        title="安装更新并重启应用？"
      >
        <p className="muted">
          应用将先退出，安装 {snapshot?.version} 后重新启动。
        </p>
        {busy && <p className="muted" role="status">请等待工作台任务完成。</p>}
        <div className="dialog-actions">
          <button
            type="button"
            className="button"
            onClick={() => setConfirmInstall(false)}
          >
            取消
          </button>
          <button
            type="button"
            className="button primary"
            disabled={disabled}
            onClick={() => {
              setConfirmInstall(false);
              request.current?.("install");
            }}
          >
            确认安装
          </button>
        </div>
      </Dialog>
    </section>
  );
}
