import { useEffect, useRef, useState } from "react";
import Workspace from "./Workspace.jsx";
import Settings from "./Settings.jsx";
import Accounts from "./accounts/Accounts.jsx";
import { useAccount } from "./accounts/useAccount.js";
import { useWorkbench } from "./useWorkbench.js";

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
  const account = useAccount({
    api: wb.api,
    active: page === "accounts",
    busy: wb.busy,
  });
  const route = useRef(null);
  route.current = {
    page,
    busy: wb.busy,
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
    if (!wb.busy) location.hash = next;
  }

  return (
    <div className={`app-shell${wb.busy ? " working" : ""}`}>
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
              disabled={wb.busy}
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
          <Workspace
            workbench={wb}
            account={account}
            openSettings={() => navigate("settings")}
          />
        </section>
        <section
          className="page-view"
          data-view="accounts"
          aria-labelledby="accounts-title"
          hidden={page !== "accounts"}
        >
          <Accounts account={account} busy={wb.busy} />
        </section>
        <section
          className="page-view"
          data-view="settings"
          aria-labelledby="settings-title"
          hidden={page !== "settings"}
        >
          <Settings workbench={wb} />
        </section>
      </main>
    </div>
  );
}
