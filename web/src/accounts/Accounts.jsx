import { useEffect, useMemo, useState } from "react";
import { Dialog } from "../components.jsx";
import { canStartLogin, isVerificationRequired } from "./useAccount.js";
import { ToastNotice } from "../notifications.jsx";
import "./accounts.css";

function AccountAvatar({ account }) {
  const [failed, setFailed] = useState(false);
  return (
    <div
      className={`accounts-avatar${account.is_logged_in && !isVerificationRequired(account) ? " connected" : ""}`}
      aria-hidden="true"
    >
      {account.avatar && !failed ? (
        <img
          src={account.avatar}
          alt=""
          referrerPolicy="no-referrer"
          loading="lazy"
          onError={() => setFailed(true)}
        />
      ) : (
        Array.from(account.nickname || account.name || "薯")[0]
      )}
    </div>
  );
}

const statusLabels = {
  waiting_scan: "等待扫码",
  waiting_confirmation: "等待手机确认",
  verification_required: "需要安全验证",
  saving: "正在保存登录信息",
  logged_in: "已登录",
  expired: "登录会话已过期",
  failed: "登录失败",
  not_logged_in: "未登录",
};

const accountNotices = {
  conflict: {
    message: "另一个账号保留了扫码窗口。请切换到该账号并结束登录窗口后，再打开新的扫码窗口。",
    type: "warning",
  },
  browserBusy: {
    message: "正在执行工作台任务，请稍候再操作登录网页。",
    type: "busy",
  },
};

export default function Accounts({ account, manager, busy }) {
  const [dialog, setDialog] = useState(null);
  const [name, setName] = useState("");
  const [originalSize, setOriginalSize] = useState(false);
  useEffect(() => {
    setDialog(null);
    setOriginalSize(false);
  }, [manager.selectedId]);
  const locked = Boolean(busy || manager.managing);
  const working = Boolean(locked || account.operation);
  const openDialog = (kind, item) => {
    setDialog({ kind, item });
    setName(item?.name || "");
  };
  async function submit(event) {
    event.preventDefault();
    const id = dialog.item?.id;
    const ok = await (dialog.kind === "create"
      ? manager.create(name.trim())
      : dialog.kind === "rename"
        ? manager.rename(id, name.trim())
        : dialog.kind === "reset"
          ? manager.reset(id)
          : manager.remove(id));
    if (ok) setDialog(null);
  }
  const embedded =
    account.active &&
    account.loginVisible &&
    account.browserConnected &&
    account.mode === "desktop" &&
    Boolean(account.desktopURL);
  const native = account.browserConnected && account.mode === "native";
  let desktopURL = account.desktopURL;
  if (embedded && originalSize) {
    const url = new URL(desktopURL, window.location.origin);
    url.searchParams.set("resize", "off");
    desktopURL = `${url.pathname}${url.search}`;
  }
  const progress =
    account.browserSession && statusLabels[account.status?.status];
  const opening = account.operation === "open";
  const connectionNotice = useMemo(() => {
    if (!account.loginVisible || opening) return null;
    if (native)
      return {
        message: "登录浏览器已在运行本服务的电脑上打开，请切换到该窗口完成登录和账号验证。",
        type: "info",
      };
    if (account.browserSession && !account.browserConnected)
      return {
        message: "后台登录浏览器会保留，点击「继续登录」可重新连接真实网页。",
        type: "info",
      };
    return null;
  }, [
    account.loginVisible,
    account.browserSession,
    account.browserConnected,
    native,
    opening,
  ]);

  return (
    <>
      <header className="intro accounts-intro">
        <div>
          <p className="eyebrow">连接你的创作身份</p>
          <h1 id="accounts-title" tabIndex={-1}>
            账号
          </h1>
          <p className="muted">
            每个账号独立保存浏览器数据、草稿和发布记录。登录状态来自最近检查，可手动刷新。
          </p>
        </div>
        <div className="button-group">
          <button
            className="button"
            disabled={locked || manager.loading}
            onClick={manager.refresh}
          >
            刷新列表
          </button>
          <button
            className="button primary"
            disabled={locked}
            onClick={() => openDialog("create")}
          >
            添加账号
          </button>
        </div>
      </header>
      <ToastNotice notice={manager.notice} />
      <ToastNotice notice={account.notice} />
      <ToastNotice notice={connectionNotice} />
      <ToastNotice
        active={Boolean(
          manager.activeLogin &&
          manager.activeLogin.account_id !== manager.selectedId,
        )}
        notice={accountNotices.conflict}
      />
      <ToastNotice active={busy && embedded} notice={accountNotices.browserBusy} />
      {!manager.loading && !manager.accounts.length && (
        <div className="card empty-state accounts-empty">
          <p>还没有账号，点击「添加账号」开始扫码。</p>
        </div>
      )}
      <div aria-label="账号列表">
        {manager.accounts.map((item) => {
          const selected = manager.selectedId === item.id;
          const verificationRequired = isVerificationRequired(item);
          const continuing =
            manager.activeLogin?.account_id === item.id ||
            (selected && account.browserSession &&
              (verificationRequired || item.is_logged_in !== true));
          const unchecked =
            !item.is_logged_in && item.user_id && !item.login_checked_at;
          const browser =
            {
              stopped: "浏览器已停止",
              running: "浏览器运行中",
              headless: "浏览器运行中",
              login: "登录窗口运行中",
            }[item.browser_status] ||
            item.browser_status ||
            "浏览器未启动";
          return (
            <section
              key={item.id}
              className={`card accounts-card${selected ? " selected" : ""}`}
              aria-label={`账号 ${item.name}`}
            >
              <AccountAvatar key={item.avatar || item.id} account={item} />
              <div className="accounts-info">
                <span className="section-kicker">
                  {selected ? "当前创作账号" : "小红书账号"}
                </span>
                <h2>{item.name}</h2>
                <p className="muted">
                  {item.nickname || "尚未验证身份"}
                  {item.user_id ? ` · ${item.user_id}` : ""}
                </p>
                <p className="helper">
                  {verificationRequired
                    ? "需要安全验证"
                    : item.is_logged_in
                      ? "已登录"
                      : unchecked
                        ? "登录状态待检查"
                        : "未登录"}
                  {` · ${browser}`}
                  {item.busy ? " · 任务进行中" : ""}
                  {selected && progress ? ` · ${progress}` : ""}
                </p>
                {verificationRequired && (
                  <p className="accounts-verification" role="status">
                    {selected && isVerificationRequired(account.status) &&
                      account.status.message
                      ? `${account.status.message} `
                      : "小红书要求完成账号安全验证。"}
                    {continuing
                      ? "请点击「继续登录」，在登录网页中完成验证。"
                      : "请点击「安全验证」，在登录网页中完成验证后再使用账号。"}
                  </p>
                )}
              </div>
              <div className="accounts-card-actions">
                <button
                  className="button small"
                  disabled={locked || selected}
                  onClick={() => manager.select(item.id)}
                >
                  {selected ? "已选择" : "切换账号"}
                </button>
                <button
                  className="button small"
                  disabled={locked}
                  onClick={() => openDialog("rename", item)}
                >
                  重命名
                </button>
                {(continuing || canStartLogin(item)) && (
                  <button
                    className="button small"
                    disabled={working}
                    onClick={() => manager.login(item.id)}
                  >
                    {continuing
                      ? "继续登录"
                      : verificationRequired
                        ? "安全验证"
                        : item.user_id
                          ? "重新登录"
                          : "登录"}
                  </button>
                )}
                {selected && (
                  <button
                    className="button small"
                    disabled={working}
                    onClick={account.checkStatus}
                  >
                    刷新状态
                  </button>
                )}
                {selected &&
                  account.browserSession &&
                  !account.loginVisible && (
                    <button
                      className="button small"
                      disabled={working}
                      onClick={account.closeLogin}
                    >
                      结束登录窗口
                    </button>
                  )}
                <button
                  className="button small"
                  disabled={locked || item.busy}
                  onClick={() => openDialog("reset", item)}
                >
                  重置浏览器数据
                </button>
                <button
                  className="button small danger"
                  disabled={locked || item.busy}
                  onClick={() => openDialog("remove", item)}
                >
                  移除
                </button>
              </div>
            </section>
          );
        })}
      </div>
      <Dialog
        open={Boolean(dialog)}
        onClose={() => setDialog(null)}
        busy={locked}
        id="account-dialog"
        title={
          {
            create: "添加账号",
            rename: "重命名账号",
            reset: "重置浏览器数据？",
            remove: "移除账号？",
          }[dialog?.kind] || "账号管理"
        }
      >
        <form onSubmit={submit}>
          {["create", "rename"].includes(dialog?.kind) ? (
            <>
              <label htmlFor="account-name">账号备注名</label>
              <input
                id="account-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
                maxLength={100}
                disabled={locked}
              />
              {dialog?.kind === "create" && (
                <p className="muted">
                  创建后会选中此账号，并直接打开扫码登录网页。
                </p>
              )}
            </>
          ) : (
            <p className="muted">
              {dialog?.kind === "reset"
                ? `将清空「${dialog.item.name}」的完整浏览器数据，包括 Cookie、站点存储、缓存、权限和配置，需要重新登录。账号 ID、身份绑定、指纹、工作台草稿和素材会保留。`
                : dialog
                  ? `将移除「${dialog.item.name}」，并清理该账号的浏览器数据、下载文件、托管图片及本浏览器草稿。其他账号和公共模型设置不受影响。此操作无法撤销。`
                  : ""}
            </p>
          )}
          <div className="dialog-actions">
            <button
              type="button"
              className="button"
              disabled={locked}
              onClick={() => setDialog(null)}
            >
              取消
            </button>
            <button
              type="submit"
              className="button primary"
              disabled={
                locked ||
                (["create", "rename"].includes(dialog?.kind) && !name.trim())
              }
            >
              {manager.managing
                ? "正在处理…"
                : dialog?.kind === "remove"
                  ? "确认移除"
                  : dialog?.kind === "reset"
                    ? "确认重置"
                    : "保存"}
            </button>
          </div>
        </form>
      </Dialog>

      {account.loginVisible && (
        <Dialog
          open={account.active}
          onClose={account.hideLogin}
          busy={locked}
          id="account-login-dialog"
          title={`扫码登录 · ${manager.selectedAccount?.name || ""}`}
          className={`accounts-login-dialog${embedded ? "" : " accounts-login-compact"}`}
          closeLabel="收起登录网页，保留后台登录浏览器"
        >
          <div
            className="accounts-login-body"
            aria-busy={
              account.operation === "open" || account.operation === "close"
            }
          >
            {embedded && (
              <div
                className={`accounts-browser${busy ? " accounts-browser-busy" : ""}`}
              >
                <iframe
                  key={account.sessionId}
                  className="accounts-browser-frame"
                  src={desktopURL}
                  title="小红书登录和账号安全验证浏览器"
                  referrerPolicy="same-origin"
                  onError={account.onFrameError}
                  tabIndex={busy ? -1 : 0}
                />
                {busy && (
                  <div className="accounts-browser-cover" aria-hidden="true" />
                )}
              </div>
            )}
            <p className="helper accounts-browser-help">
              {native
                ? "请在运行本服务的电脑上完成扫码和安全验证，登录信息保存成功后窗口会自动关闭。"
                : originalSize
                  ? "当前按原始大小显示，可滚动查看二维码。关闭弹窗会保留后台会话。"
                  : "二维码较小时可切换「原始大小」。关闭弹窗会保留会话，结束时请使用「结束登录窗口」。"}
            </p>
          </div>
          <div className="accounts-login-actions">
            {embedded && (
              <button
                type="button"
                className="button"
                disabled={locked}
                onClick={() => setOriginalSize((value) => !value)}
              >
                {originalSize ? "适应窗口" : "原始大小"}
              </button>
            )}
            {embedded &&
              (busy ? (
                <button type="button" className="button" disabled>
                  在新窗口查看
                </button>
              ) : (
                <a
                  className="button"
                  href={desktopURL}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  在新窗口查看
                </a>
              ))}
            <button
              type="button"
              className="button"
              disabled={working}
              onClick={account.checkStatus}
            >
              检查登录状态
            </button>
            {account.browserSession && (
              <button
                type="button"
                className="button"
                disabled={working}
                onClick={account.closeLogin}
              >
                {account.operation === "close" ? "正在结束…" : "结束登录窗口"}
              </button>
            )}
            {(account.browserSession ||
              canStartLogin(manager.selectedAccount)) && (
              <button
                type="button"
                className="button primary"
                disabled={working}
                onClick={account.openLogin}
              >
                {account.operation === "open"
                  ? "正在打开…"
                  : account.browserSession
                    ? "继续登录"
                    : "打开登录网页"}
              </button>
            )}
          </div>
        </Dialog>
      )}
    </>
  );
}
