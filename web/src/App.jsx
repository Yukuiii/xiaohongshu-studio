import { useEffect, useRef, useState } from "react";
import Workspace from "./Workspace.jsx";
import Settings from "./Settings.jsx";
import Accounts from "./accounts/Accounts.jsx";
import { canStartLogin, useAccount } from "./accounts/useAccount.js";
import { useWorkbench } from "./useWorkbench.js";

const ACCOUNT_KEY = "hongshu-workbench.account.v1";

function savedAccount() {
  try {
    return localStorage.getItem(ACCOUNT_KEY) || "";
  } catch {
    return "";
  }
}

const pages = { workspace: "工作台", accounts: "账号", settings: "设置" };
const currentPage = () =>
  Object.hasOwn(pages, location.hash.slice(1))
    ? location.hash.slice(1)
    : "workspace";

function NavIcon({ page }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {page === "workspace" ? (
        <>
          <rect x="3" y="3" width="7" height="7" rx="1.5" />
          <rect x="14" y="3" width="7" height="7" rx="1.5" />
          <rect x="3" y="14" width="7" height="7" rx="1.5" />
          <rect x="14" y="14" width="7" height="7" rx="1.5" />
        </>
      ) : page === "accounts" ? (
        <>
          <circle cx="12" cy="8" r="4" />
          <path d="M4 21v-2a8 8 0 0 1 16 0v2" />
        </>
      ) : (
        <>
          <path d="M5 3v18M12 3v18M19 3v18M2 8h6M9 16h6M16 8h6" />
          <circle cx="5" cy="8" r="2" fill="currentColor" stroke="none" />
          <circle cx="12" cy="16" r="2" fill="currentColor" stroke="none" />
          <circle cx="19" cy="8" r="2" fill="currentColor" stroke="none" />
        </>
      )}
    </svg>
  );
}

export default function App() {
  const [page, setPage] = useState(currentPage);
  const wb = useWorkbench();
  const [accounts, setAccounts] = useState([]);
  const [selectedId, setSelectedId] = useState(null);
  const [legacyId, setLegacyId] = useState(null);
  const [activeLogin, setActiveLogin] = useState(null);
  const [listNotice, setListNotice] = useState({ message: "", type: "" });
  const [managing, setManaging] = useState("");
  const [loading, setLoading] = useState(true);
  const [loginAfterSelect, setLoginAfterSelect] = useState(null);
  const preferredId = useRef(savedAccount());
  const listRequest = useRef(null);
  const initialList = useRef(true);
  const mutation = useRef(false);
  const latest = useRef(null);
  latest.current = { wb, selectedId };
  const selectedAccount = accounts.find((item) => item.id === selectedId);
  const account = useAccount({
    api: wb.api,
    active: page === "accounts",
    busy: wb.busy || Boolean(managing),
    context: wb.context,
    summary: selectedAccount,
    activeLogin,
    onStatus: (data, scope) => {
      if (latest.current.wb.context !== scope) return;
      if (data.is_logged_in || data.status === "not_logged_in") {
        setAccounts((items) =>
          items.map((item) =>
            item.id === scope.accountId
              ? {
                  ...item,
                  is_logged_in: data.is_logged_in === true,
                  nickname: data.username || item.nickname,
                  user_id: data.user_id || item.user_id,
                }
              : item,
          ),
        );
      }
      reloadAccounts({ silent: true });
    },
    onLoginSession: (session, id) =>
      setActiveLogin((previous) => {
        if (!session) return previous?.account_id === id ? null : previous;
        return previous?.account_id === session.account_id &&
          previous?.session_id === session.session_id
          ? previous
          : session;
      }),
  });

  function selectAccount(
    id,
    items = accounts,
    legacy = legacyId,
    { remember = true, discard = false } = {},
  ) {
    if (id === latest.current.selectedId && wb.context.accountId === id)
      return true;
    if (wb.busy || (id && !items.some((item) => item.id === id))) return false;
    if (!wb.activateAccount(id, legacy, { discard })) {
      setListNotice({
        message:
          "草稿未能保存，已保留当前账号。请复制重要内容或恢复本地存储后重试。",
        type: "error",
      });
      return false;
    }
    account.invalidate();
    setSelectedId(id);
    if (id && remember) {
      preferredId.current = id;
      try {
        localStorage.setItem(ACCOUNT_KEY, id);
      } catch {
        /* 选择仍在本页生效。 */
      }
    }
    setListNotice({ message: "", type: "" });
    return true;
  }

  async function reloadAccounts({ silent = false } = {}) {
    listRequest.current?.abort();
    if (document.hidden) {
      listRequest.current = null;
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    listRequest.current = controller;
    const token = latest.current.wb.settings.token;
    const initial = initialList.current;
    if (!silent || initial) setLoading(true);
    try {
      const data = await latest.current.wb.api("/accounts", {
        signal: controller.signal,
        token,
      });
      if (
        listRequest.current !== controller ||
        controller.signal.aborted ||
        latest.current.wb.settings.token !== token
      )
        return;
      initialList.current = false;
      const items = Array.isArray(data?.accounts) ? data.accounts : [];
      setAccounts(items);
      setLegacyId(data.legacy_account_id || null);
      setActiveLogin(data.active_login || null);
      if (initial) {
        const preferred = preferredId.current;
        const next = preferred
          ? items.find((item) => item.id === preferred)
          : items.length === 1
            ? items[0]
            : null;
        if (next) selectAccount(next.id, items, data.legacy_account_id);
        else
          setListNotice({
            message: preferred
              ? "上次选择的账号已不可用，请明确选择其他账号或添加账号。"
              : "请选择创作账号，或添加账号后扫码登录。",
            type: "warning",
          });
      } else if (
        latest.current.selectedId &&
        !items.some((item) => item.id === latest.current.selectedId)
      ) {
        if (selectAccount(null, items, data.legacy_account_id))
          setListNotice({
            message: "当前账号已被移除，请重新选择账号。",
            type: "warning",
          });
      }
    } catch (error) {
      if (
        listRequest.current === controller &&
        !controller.signal.aborted &&
        (!silent || initial)
      )
        setListNotice({ message: error.message, type: "error" });
    } finally {
      if (listRequest.current === controller) {
        listRequest.current = null;
        if (!controller.signal.aborted) setLoading(false);
      }
    }
  }

  useEffect(() => {
    initialList.current = true;
    setSelectedId(null);
    setAccounts([]);
    setActiveLogin(null);
    setLoginAfterSelect(null);
    account.invalidate();
    reloadAccounts();
    return () => listRequest.current?.abort();
  }, [wb.settings.token]);

  useEffect(() => {
    let timer;
    const suspend = () => {
      window.clearInterval(timer);
      listRequest.current?.abort();
      listRequest.current = null;
      setLoading(false);
    };
    const resume = () => {
      window.clearInterval(timer);
      if (document.hidden) return;
      if (!mutation.current && !listRequest.current)
        reloadAccounts({ silent: true });
      if (page === "accounts") {
        timer = window.setInterval(() => {
          if (!document.hidden && !mutation.current && !listRequest.current)
            reloadAccounts({ silent: true });
        }, 60000);
      }
    };
    const visibilityChanged = () => (document.hidden ? suspend() : resume());
    if (page === "accounts" && !document.hidden) resume();
    document.addEventListener("visibilitychange", visibilityChanged);
    window.addEventListener("pagehide", suspend);
    window.addEventListener("pageshow", resume);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", visibilityChanged);
      window.removeEventListener("pagehide", suspend);
      window.removeEventListener("pageshow", resume);
    };
  }, [page, wb.settings.token]);

  useEffect(() => {
    if (
      loginAfterSelect &&
      selectedId === loginAfterSelect &&
      wb.context.accountId === selectedId &&
      !managing
    ) {
      setLoginAfterSelect(null);
      account.openLogin();
    }
  }, [loginAfterSelect, selectedId, wb.context, managing]);

  async function manage(operation, action) {
    if (latest.current.wb.busy || mutation.current) return false;
    mutation.current = true;
    listRequest.current?.abort();
    listRequest.current = null;
    setLoading(false);
    setManaging(operation);
    setListNotice({ message: "", type: "" });
    const scope = latest.current.wb.context;
    const token = latest.current.wb.settings.token;
    const managementAPI = async (path, options) => {
      const data = await wb.api(path, { ...options, token });
      if (
        latest.current.wb.context !== scope ||
        latest.current.wb.settings.token !== token
      )
        throw new DOMException("账号上下文已变更", "AbortError");
      return data;
    };
    try {
      await action(managementAPI);
      return true;
    } catch (error) {
      if (error.name !== "AbortError")
        setListNotice({ message: error.message, type: "error" });
      return false;
    } finally {
      mutation.current = false;
      setManaging("");
    }
  }

  const manager = {
    accounts,
    selectedId,
    selectedAccount,
    loading,
    managing,
    notice: listNotice,
    activeLogin,
    refresh: () => reloadAccounts(),
    select: (id) => {
      if (!mutation.current) selectAccount(id);
    },
    login: (id) => {
      const item = accounts.find((item) => item.id === id);
      const continuing =
        activeLogin?.account_id === id ||
        (selectedId === id &&
          account.browserSession &&
          item?.is_logged_in !== true);
      if (
        !mutation.current &&
        item &&
        (continuing || canStartLogin(item)) &&
        selectAccount(id)
      )
        setLoginAfterSelect(id);
    },
    create: (name) =>
      manage("create", async (api) => {
        if (!wb.flushDraft())
          throw new Error("草稿未能保存，暂时无法切换账号。");
        const data = await api("/accounts", { method: "POST", body: { name } });
        const items = [...accounts, data.account];
        setAccounts(items);
        if (selectAccount(data.account.id, items))
          setLoginAfterSelect(data.account.id);
      }),
    rename: (id, name) =>
      manage("rename", async (api) => {
        const data = await api(`/accounts/${encodeURIComponent(id)}`, {
          method: "PATCH",
          body: { name },
        });
        setAccounts((items) =>
          items.map((item) => (item.id === id ? data.account : item)),
        );
      }),
    reset: (id) =>
      manage("reset", async (api) => {
        const data = await api(
          `/accounts/${encodeURIComponent(id)}/reset-profile`,
          { method: "POST" },
        );
        setAccounts((items) =>
          items.map((item) => (item.id === id ? data.account : item)),
        );
        if (id === selectedId) account.invalidate();
        await reloadAccounts({ silent: true });
        setListNotice({
          message: "浏览器数据已重置。账号身份、草稿和素材已保留，请重新登录。",
          type: "",
        });
      }),
    remove: (id) =>
      manage("remove", async (api) => {
        if (id === selectedId && !wb.flushDraft())
          throw new Error("草稿未能保存，账号尚未移除。");
        await api(`/accounts/${encodeURIComponent(id)}`, { method: "DELETE" });
        if (id === selectedId)
          selectAccount(null, accounts, legacyId, { discard: true });
        setAccounts((items) => items.filter((item) => item.id !== id));
        try {
          localStorage.removeItem(`hongshu-workbench.draft.v1:${id}`);
          localStorage.removeItem(`hongshu-workbench.publish.v1:${id}`);
        } catch {
          throw new Error(
            "账号已移除，但本浏览器草稿未能清理，请在浏览器设置中清除此站点的数据。",
          );
        }
        setListNotice({
          message:
            "账号及其浏览器数据、下载文件、托管图片和本浏览器草稿已移除。",
          type: "",
        });
      }),
  };
  const uiBusy = wb.busy || Boolean(managing);
  const route = useRef(null);
  route.current = {
    page,
    busy: uiBusy,
    closeWorkspaceDialogs: wb.closeWorkspaceDialogs,
  };
  const previousPage = useRef(page);

  useEffect(() => {
    function onHashChange() {
      if (route.current.busy) {
        history.replaceState(null, "", `#${route.current.page}`);
        return;
      }
      const next = currentPage();
      if (next !== "workspace") route.current.closeWorkspaceDialogs();
      setPage(next);
    }
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  useEffect(() => {
    document.title = `${pages[page]} · 红薯创作台`;
    if (previousPage.current !== page) {
      window.scrollTo({ top: 0, behavior: "instant" });
      document
        .querySelector(`[data-view="${page}"] h1`)
        ?.focus({ preventScroll: true });
      previousPage.current = page;
    }
  }, [page]);

  function navigate(next) {
    if (!uiBusy) location.hash = next;
  }

  return (
    <div className={`app-shell${uiBusy ? " working" : ""}`}>
      <aside className="sidebar" aria-label="主导航">
        <a
          className="brand"
          href="#workspace"
          aria-label="红薯创作台首页"
          onClick={(event) => {
            event.preventDefault();
            navigate("workspace");
          }}
        >
          <span className="brand-mark">薯</span>
          <span>
            红薯创作台<small>YOUR CREATIVE DESK</small>
          </span>
        </a>
        <nav className="sidebar-nav" aria-label="页面导航">
          {Object.entries(pages).map(([key, label]) => (
            <button
              type="button"
              key={key}
              className="nav-item"
              data-page={key}
              aria-current={page === key ? "page" : undefined}
              disabled={uiBusy}
              onClick={() => navigate(key)}
            >
              <NavIcon page={key} />
              <span>{label}</span>
              {key === "accounts" && (
                <span
                  className={`status-dot${account.loggedIn ? " connected" : ""}`}
                  aria-hidden="true"
                />
              )}
            </button>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span className="sidebar-footer-mark"></span>让灵感成为好笔记
        </div>
      </aside>
      <header className="topbar">
        <span className="page-label">{pages[page]}</span>
        <span className="current-account" aria-label="当前账号">
          {selectedAccount
            ? `当前账号 · ${selectedAccount.name}${selectedAccount.nickname ? ` / ${selectedAccount.nickname}` : ""}`
            : "尚未选择账号"}
        </span>
        <span id="save-status" className="save-status" role="status">
          {wb.saveStatus}
        </span>
      </header>
      <main className="workspace">
        {!wb.persistenceAvailable && (
          <div id="storage-notice" className="notice warning" role="status">
            浏览器本地存储不可用或空间不足。你仍可继续本次创作，但刷新可能丢失配置和草稿，请先复制重要内容。
          </div>
        )}
        <section
          className="page-view"
          data-view="workspace"
          aria-labelledby="workspace-title"
          hidden={page !== "workspace"}
        >
          {selectedId ? (
            <Workspace
              workbench={wb}
              account={account}
              selectedAccount={selectedAccount}
              openSettings={() => navigate("settings")}
            />
          ) : (
            <section className="card">
              <p className="eyebrow">连接你的创作身份</p>
              <h1 id="workspace-title" tabIndex={-1}>
                请选择创作账号
              </h1>
              <p className="muted">
                每个账号拥有独立草稿和发布记录。选择账号后即可继续创作，模型设置和服务访问令牌共用。
              </p>
              {listNotice.message && <p role="status">{listNotice.message}</p>}
              <div className="button-group">
                <button
                  className="button primary"
                  disabled={uiBusy}
                  onClick={() => navigate("accounts")}
                >
                  前往账号页
                </button>
                <button
                  className="button"
                  disabled={uiBusy}
                  onClick={() => navigate("settings")}
                >
                  打开设置
                </button>
              </div>
            </section>
          )}
        </section>
        <section
          className="page-view"
          data-view="accounts"
          aria-labelledby="accounts-title"
          hidden={page !== "accounts"}
        >
          <Accounts account={account} manager={manager} busy={wb.busy} />
        </section>
        <section
          className="page-view"
          data-view="settings"
          aria-labelledby="settings-title"
          hidden={page !== "settings"}
        >
          <Settings workbench={wb} active={page === "settings"} />
        </section>
      </main>
    </div>
  );
}
