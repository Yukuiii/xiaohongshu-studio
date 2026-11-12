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

export default function Accounts({ account, busy }) {
  const working = Boolean(busy || account.operation);
  const embedded =
    account.active &&
    account.loginVisible &&
    account.browserConnected &&
    account.mode === "desktop" &&
    Boolean(account.desktopURL);
  const native = account.browserConnected && account.mode === "native";
  const label = account.loggedIn
    ? "已连接"
    : statusLabels[account.status?.status] || "未连接";
  const openLabel =
    account.operation === "open"
      ? "正在打开登录网页…"
      : account.browserSession
        ? "继续登录"
        : account.loggedIn
          ? "打开账号网页"
          : "添加账号";

  return (
    <>
      <header className="intro accounts-intro">
        <div>
          <p className="eyebrow">连接你的创作身份</p>
          <h1 id="accounts-title" tabIndex={-1}>
            账号
          </h1>
          <p className="muted">
            在真实的小红书网页中扫码，完成手机确认和账号安全验证。
          </p>
        </div>
        <button
          type="button"
          className="button primary"
          disabled={busy || ["open", "close"].includes(account.operation)}
          onClick={account.openLogin}
        >
          {openLabel}
        </button>
      </header>

      {account.notice.message && (
        <div
          className={`notice ${account.notice.type || ""}`}
          role="status"
          aria-live="polite"
        >
          {account.notice.message}
        </div>
      )}

      <section
        className="card accounts-card"
        aria-labelledby="connected-account-name"
        aria-busy={account.operation === "status"}
      >
        <div
          className={`accounts-avatar${account.loggedIn ? " connected" : ""}`}
          aria-hidden="true"
        >
          {account.loggedIn && account.username
            ? Array.from(account.username)[0]
            : "薯"}
        </div>
        <div className="accounts-info">
          <span className="section-kicker">小红书账号</span>
          <h2 id="connected-account-name">
            {account.loggedIn
              ? account.username || "已登录小红书账号"
              : "连接你的小红书账号"}
          </h2>
          <p className="muted">
            {account.loggedIn
              ? "当前服务使用此账号进行搜索和发布。"
              : account.browserSession
                ? "登录浏览器已保留，点击「继续登录」完成扫码和验证。"
                : "点击「添加账号」打开登录网页，扫码后即可开始创作。"}
          </p>
          {account.loggedIn && account.status?.user_id && (
            <p className="muted accounts-user-id">
              账号 ID：{account.status.user_id}
            </p>
          )}
        </div>
        <div className="accounts-card-actions">
          <span
            className={`accounts-state${account.loggedIn ? " connected" : ""}`}
          >
            <span aria-hidden="true" />
            {label}
          </span>
          <button
            type="button"
            className="button small"
            disabled={working}
            onClick={account.checkStatus}
          >
            {account.operation === "status" ? "正在检查…" : "刷新状态"}
          </button>
          {account.browserSession && !account.loginVisible && (
            <button
              type="button"
              className="button small"
              disabled={working}
              onClick={account.closeLogin}
            >
              {account.operation === "close" ? "正在结束…" : "结束登录窗口"}
            </button>
          )}
        </div>
      </section>

      {account.loginVisible && (
        <section
          className="card accounts-login-panel"
          aria-labelledby="accounts-login-title"
          aria-busy={
            account.operation === "open" || account.operation === "close"
          }
        >
          <div className="accounts-panel-header">
            <div>
              <span className="section-kicker">连接小红书</span>
              <h2 id="accounts-login-title">扫码添加账号</h2>
            </div>
            <button
              type="button"
              className="icon-button accounts-collapse"
              aria-label="收起登录网页，保留后台登录浏览器"
              disabled={busy}
              onClick={account.hideLogin}
            >
              ×
            </button>
          </div>
          <p className="muted">
            使用小红书 App 扫描网页中的二维码，并按真实页面提示完成登录。
          </p>
          {embedded ? (
            <div
              className={`accounts-browser${busy ? " accounts-browser-busy" : ""}`}
            >
              <iframe
                className="accounts-browser-frame"
                src={account.desktopURL}
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
              ? "请在运行本服务的电脑上弹出的 Chromium 浏览器中完成扫码、手机确认和账号安全验证。登录信息保存成功后，浏览器会自动关闭。"
              : "收起网页、切换页面或隐藏标签页会断开画面和前端检查，后台浏览器继续保留。登录信息保存成功后会自动关闭，未完成的会话最长保留 20 分钟，二维码是否过期以真实页面为准。"}
          </p>
          <div className="accounts-login-actions">
            {embedded &&
              (busy ? (
                <button type="button" className="button" disabled>
                  在新窗口查看
                </button>
              ) : (
                <a
                  className="button"
                  href={account.desktopURL}
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
        </section>
      )}
    </>
  );
}
