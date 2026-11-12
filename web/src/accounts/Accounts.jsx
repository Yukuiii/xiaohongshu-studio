import { useEffect, useState } from "react";
import { Dialog, Notice } from "../components.jsx";
import "./accounts.css";

const statusLabels = {
  waiting_scan: "等待扫码",
  waiting_confirmation: "等待手机确认",
  verification_required: "需要安全验证",
  saving: "正在保存登录信息",
  logged_in: "已连接",
  expired: "登录会话已过期",
  failed: "登录失败",
  not_logged_in: "未连接",
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
  const label = account.loggedIn
    ? "已连接"
    : statusLabels[account.status?.status] || "未连接";

  return (
    <>
      <header className="intro accounts-intro">
        <div>
          <p className="eyebrow">连接你的创作身份</p>
          <h1 id="accounts-title" tabIndex={-1}>
            账号
          </h1>
          <p className="muted">
            每个账号独立保存浏览器数据、草稿和发布记录。选中后会检查真实登录状态。
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
      <Notice notice={manager.notice} />
      {!account.loginVisible && <Notice notice={account.notice} />}
      {manager.activeLogin &&
        manager.activeLogin.account_id !== manager.selectedId && (
          <p className="notice warning">
            另一个账号保留了扫码窗口。请切换到该账号并结束登录窗口后，再打开新的扫码窗口。
          </p>
        )}
      {manager.loading && <p role="status">正在读取账号列表…</p>}
      {!manager.loading && !manager.accounts.length && (
        <p className="muted">还没有账号，点击「添加账号」开始扫码。</p>
      )}
      <div aria-label="账号列表">
        {manager.accounts.map((item) => {
          const selected = manager.selectedId === item.id;
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
              <div
                className={`accounts-avatar${selected && account.loggedIn ? " connected" : ""}`}
                aria-hidden="true"
              >
                {Array.from(item.nickname || item.name || "薯")[0]}
              </div>
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
                  {browser}
                  {item.busy ? " · 任务进行中" : ""}
                  {selected ? ` · ${label}` : " · 登录状态待检查"}
                </p>
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
                <button
                  className="button small"
                  disabled={
                    locked || ["open", "close"].includes(account.operation)
                  }
                  onClick={() => manager.login(item.id)}
                >
                  {selected && account.browserSession ? "继续登录" : "重新登录"}
                </button>
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
                  className="button small"
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
          <Notice
            notice={manager.notice.type === "error" ? manager.notice : null}
          />
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
          className="accounts-login-dialog"
          closeLabel="收起登录网页，保留后台登录浏览器"
        >
          <div
            className="accounts-login-body"
            aria-busy={
              account.operation === "open" || account.operation === "close"
            }
          >
            <Notice notice={account.notice} />
            {embedded ? (
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
                  <div className="accounts-browser-cover" role="status">
                    正在执行工作台任务，请稍候再操作登录网页。
                  </div>
                )}
              </div>
            ) : (
              <div className="accounts-browser-placeholder" role="status">
                {account.operation === "open"
                  ? "正在打开真实登录网页，请稍候…"
                  : native
                    ? "登录浏览器已在运行本服务的电脑上打开，请切换到该窗口完成登录和账号验证。"
                    : account.browserSession
                      ? "后台登录浏览器会保留，点击「继续登录」可重新连接真实网页。"
                      : "点击「打开登录网页」，查看并操作完整的小红书登录页面。"}
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
          </div>
        </Dialog>
      )}
    </>
  );
}
