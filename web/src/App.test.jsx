import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import App from "./App.jsx";

const A = {
  id: "A",
  name: "账号A",
  nickname: "作者A",
  user_id: "user-A",
  browser_status: "stopped",
  busy: false,
  is_logged_in: true,
  login_checked_at: "2026-11-13T08:00:00Z",
};
const expiredA = { ...A, is_logged_in: false };
const B = {
  id: "B",
  name: "账号B",
  browser_status: "stopped",
  busy: false,
  is_logged_in: false,
  login_checked_at: null,
};
const ok = (data) => ({
  ok: true,
  status: 200,
  json: async () => ({ success: true, data }),
});

function service(accounts = [A, B], mode = "native") {
  let settings = {
    initialized: true,
    text: { base_url: "", model: "", has_api_key: false },
    image: { base_url: "", model: "", has_api_key: false },
  };
  const drafts = new Map();
  const protections = new Map();
  const fetch = vi.fn(async (url, options) => {
    if (url === "/api/v1/draft") {
      const accountId = options.headers["X-XHS-Account-ID"];
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        const current = drafts.get(accountId)?.revision ?? 0;
        if (body.base_revision !== current)
          return {
            ok: false,
            status: 409,
            json: async () => ({
              success: false,
              code: "DRAFT_REVISION_CONFLICT",
              error: "版本冲突",
            }),
          };
        drafts.set(accountId, {
          payload: body.draft,
          revision: current + 1,
        });
        return ok({ revision: current + 1 });
      }
      if (options.method === "DELETE") {
        drafts.delete(accountId);
        return ok({ revision: 0 });
      }
      const record = drafts.get(accountId);
      return ok(
        record
          ? { draft: record.payload, revision: record.revision }
          : { draft: null, revision: 0 },
      );
    }
    if (url === "/api/v1/publish-protection") {
      const accountId = options.headers["X-XHS-Account-ID"];
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        const current = protections.get(accountId)?.revision ?? 0;
        if (body.base_revision !== current)
          return {
            ok: false,
            status: 409,
            json: async () => ({
              success: false,
              code: "PROTECTION_REVISION_CONFLICT",
              error: "版本冲突",
            }),
          };
        protections.set(accountId, {
          revision: current + 1,
          uncertain: body.uncertain === true,
          last_published: body.last_published || "",
        });
        return ok({ revision: current + 1 });
      }
      const record = protections.get(accountId);
      return ok(
        record
          ? {
              revision: record.revision,
              uncertain: record.uncertain,
              last_published: record.last_published,
            }
          : { revision: 0, uncertain: false, last_published: "" },
      );
    }
    if (url === "/api/v1/settings") {
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        settings = {
          initialized: true,
          ...Object.fromEntries(
            ["text", "image"].map((kind) => [
              kind,
              {
                base_url: body[kind].base_url,
                model: body[kind].model,
                has_api_key:
                  body[kind].api_key === undefined
                    ? settings[kind].has_api_key
                    : Boolean(body[kind].api_key),
              },
            ]),
          ),
        };
      }
      return ok(settings);
    }
    if (url === "/api/v1/accounts" && options.method === "POST")
      return ok({ account: { id: "C", name: "新账号" } });
    if (url.endsWith("/reset-profile")) {
      const id = url.split("/").at(-2);
      accounts = accounts.map((item) =>
        item.id === id
          ? {
              ...item,
              is_logged_in: false,
              login_checked_at: A.login_checked_at,
            }
          : item,
      );
      return ok({ account: accounts.find((item) => item.id === id) });
    }
    if (url === "/api/v1/accounts")
      return ok({ accounts, legacy_account_id: "A" });
    if (url.endsWith("/login/browser"))
      return ok({
        account_id: options.headers["X-XHS-Account-ID"],
        session_id: "session-C",
        mode,
        ...(mode === "desktop"
          ? {
              desktop_url:
                "/desktop/session-C/vnc.html?autoconnect=1&resize=scale&path=desktop/session-C/websockify",
            }
          : {}),
        status: "waiting_scan",
        is_logged_in: false,
      });
    if (url.endsWith("/login/session")) {
      return ok({
        account_id: options.headers["X-XHS-Account-ID"],
        is_logged_in: false,
        status: "waiting_scan",
      });
    }
    if (url.endsWith("/account/verify")) {
      const id = options.headers["X-XHS-Account-ID"];
      accounts = accounts.map((item) =>
        item.id === id
          ? {
              ...item,
              is_logged_in: false,
              login_checked_at: A.login_checked_at,
            }
          : item,
      );
      return ok({
        account_id: id,
        is_logged_in: false,
        status: "not_logged_in",
      });
    }
    return ok({});
  });
  fetch.seedDraft = (accountId, payload, revision = 1) =>
    drafts.set(accountId, { payload, revision });
  fetch.getDraft = (accountId) => drafts.get(accountId)?.payload;
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

function accountsPage() {
  history.replaceState(null, "", "/web/#accounts");
}

async function navigatePage(page) {
  await act(async () => {
    history.replaceState(null, "", `/web/#${page}`);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
}

const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const listCalls = (fetch) =>
  fetch.mock.calls.filter(([url]) => url === "/api/v1/accounts");

describe("分步创作工作台", () => {
  function workspaceService(accounts = [A]) {
    const fetch = service(accounts);
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) => {
      if (path.startsWith("/api/v1/images/"))
        return Promise.resolve({
          ok: true,
          blob: async () => new Blob(["image"], { type: "image/png" }),
        });
      if (path === "/api/v1/account/verify")
        return Promise.resolve(
          ok({
            account_id: options.headers["X-XHS-Account-ID"],
            user_id: "user-A",
            username: "作者A",
            is_logged_in: true,
            status: "logged_in",
          }),
        );
      if (path === "/api/v1/publish")
        return Promise.resolve(ok({ status: "发布完成" }));
      return original(path, options);
    });
    return fetch;
  }

  it("每次只展示当前步骤，往返保留文字、图片和同一个预览节点", async () => {
    const fetch = workspaceService();
    fetch.seedDraft("A", {
      title: "原稿标题",
      content: "原稿正文",
      tags: "#分享",
      images: [
        { id: "one", name: "封面", path: "/images/one.png" },
        { id: "two", name: "内页", path: "/images/two.png" },
      ],
      sources: [
        { id: "ref", token: "token", title: "参考笔记", selected: true },
      ],
    });
    render(<App />);
    const first = await screen.findByRole("tab", { name: "寻找灵感" });
    await waitFor(() => expect(first).toBeEnabled());
    expect(screen.getAllByRole("tabpanel")).toHaveLength(1);
    expect(screen.getByRole("tabpanel", { name: "寻找灵感" })).toBeVisible();
    expect(screen.getByLabelText("参考笔记")).toBeChecked();
    const preview = screen.getByRole("complementary", { name: "笔记实时预览" });
    const cover = within(preview).getByRole("img", { name: "笔记配图 1" });
    fireEvent.click(screen.getByRole("button", { name: "预览下一张配图" }));
    expect(within(preview).getByRole("img", { name: "笔记配图 2" })).toBe(
      cover,
    );
    fireEvent.click(screen.getByRole("button", { name: /下一步：撰写文案/ }));
    expect(screen.getByRole("tabpanel", { name: "撰写文案" })).toHaveFocus();
    fireEvent.change(screen.getByLabelText("笔记标题"), {
      target: { value: "修改后的标题" },
    });
    fireEvent.change(screen.getByLabelText("正文"), {
      target: { value: "修改后的正文" },
    });
    expect(preview.querySelector("#preview-title")).toHaveTextContent(
      "修改后的标题",
    );
    expect(preview.querySelector("#preview-content")).toHaveTextContent(
      "修改后的正文",
    );
    fireEvent.click(screen.getByRole("button", { name: /下一步：制作配图/ }));
    expect(screen.getByRole("tabpanel", { name: "制作配图" })).toBeVisible();
    expect(screen.getByRole("img", { name: "内页" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "上一步" }));
    expect(screen.getByLabelText("笔记标题")).toHaveValue("修改后的标题");
    expect(screen.getByLabelText("正文")).toHaveValue("修改后的正文");
    fireEvent.click(first);
    expect(screen.getByLabelText("参考笔记")).toBeChecked();
    expect(screen.getByRole("complementary", { name: "笔记实时预览" })).toBe(
      preview,
    );
    expect(within(preview).getByRole("img", { name: "笔记配图 2" })).toBe(
      cover,
    );
    expect(
      fetch.mock.calls.some(
        ([path, options]) =>
          /\/feeds\/|\/ai\/compose/.test(path) ||
          (path === "/api/v1/publish" && options.method === "POST"),
      ),
    ).toBe(false);
  });

  it("Tab 支持键盘切换，无参考时可手写并直接进入后续步骤", async () => {
    workspaceService();
    render(<App />);
    const first = await screen.findByRole("tab", { name: "寻找灵感" });
    fireEvent.keyDown(first, { key: "ArrowRight" });
    const writing = screen.getByRole("tab", { name: "撰写文案" });
    expect(writing).toHaveFocus();
    expect(writing).toHaveAttribute("aria-selected", "true");
    expect(
      screen.getByRole("button", { name: /按参考生成文案/ }),
    ).toBeDisabled();
    fireEvent.change(screen.getByLabelText("笔记标题"), {
      target: { value: "手写笔记" },
    });
    fireEvent.keyDown(writing, { key: "End" });
    const publish = screen.getByRole("tab", { name: "确认发布" });
    expect(publish).toHaveFocus();
    expect(screen.getByRole("tabpanel", { name: "确认发布" })).toBeVisible();
    fireEvent.keyDown(publish, { key: "Home" });
    expect(first).toHaveFocus();
    fireEvent.keyDown(first, { key: "ArrowLeft" });
    expect(publish).toHaveFocus();
    expect(document.querySelector("#preview-title")).toHaveTextContent(
      "手写笔记",
    );
  });

  it("切换账号后回到第一步，重新进入文案时读取对应账号草稿", async () => {
    localStorage.setItem("hongshu-workbench.account.v1", "A");
    const fetch = workspaceService([A, B]);
    fetch.seedDraft("A", { title: "A草稿" });
    fetch.seedDraft("B", { title: "B草稿" });
    render(<App />);
    fireEvent.click(await screen.findByRole("tab", { name: "撰写文案" }));
    expect(screen.getByLabelText("笔记标题")).toHaveValue("A草稿");
    await navigatePage("accounts");
    const row = screen.getByRole("region", { name: "账号 账号B" });
    fireEvent.click(within(row).getByRole("button", { name: "切换账号" }));
    await navigatePage("workspace");
    expect(screen.getByRole("tab", { name: "寻找灵感" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(document.querySelector("#preview-title")).toHaveTextContent("B草稿");
    fireEvent.click(screen.getByRole("tab", { name: "撰写文案" }));
    expect(screen.getByLabelText("笔记标题")).toHaveValue("B草稿");
    expect(fetch.getDraft("A").title).toBe("A草稿");
  });

  it("只有第四步可检查发布，进入步骤不提交，确认勾选后才发送发布请求", async () => {
    const fetch = workspaceService();
    fetch.seedDraft("A", {
      title: "待发布笔记",
      content: "已核对的正文",
      images: [{ id: "pic", name: "配图", path: "/images/pic.png" }],
    });
    render(<App />);
    const first = await screen.findByRole("tab", { name: "寻找灵感" });
    await waitFor(() => expect(first).toBeEnabled());
    expect(
      screen.queryByRole("button", { name: /检查并发布/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "确认发布" }));
    expect(screen.getByLabelText("谁可以看")).toBeVisible();
    expect(fetch.mock.calls.some(([path]) => path === "/api/v1/publish")).toBe(
      false,
    );
    fireEvent.click(screen.getByRole("button", { name: /检查并发布/ }));
    const dialog = await screen.findByRole("dialog", {
      name: "准备好分享了吗？",
    });
    const confirm = within(dialog).getByRole("button", {
      name: "确认发布",
      exact: true,
    });
    expect(confirm).toBeDisabled();
    expect(fetch.mock.calls.some(([path]) => path === "/api/v1/publish")).toBe(
      false,
    );
    fireEvent.click(
      within(dialog).getByRole("checkbox", { name: /我已核对正文/ }),
    );
    fireEvent.click(confirm);
    await waitFor(() =>
      expect(
        fetch.mock.calls.filter(([path]) => path === "/api/v1/publish"),
      ).toHaveLength(1),
    );
    const request = fetch.mock.calls.find(
      ([path]) => path === "/api/v1/publish",
    );
    expect(request[1].headers["X-XHS-Account-ID"]).toBe("A");
    await screen.findByText(/笔记发布完成！/);
  });
});

describe("账号选择界面", () => {
  it("已登录账号隐藏登录入口，加载和切换均不打开登录窗口", async () => {
    accountsPage();
    const fetch = service([A, B]);
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    expect(within(row).getByText(/已登录/)).toBeVisible();
    expect(
      within(row).queryByRole("button", { name: /^(重新登录|登录|继续登录)$/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(within(row).getByRole("button", { name: "切换账号" }));
    expect(
      within(row).queryByRole("button", { name: "重新登录" }),
    ).not.toBeInTheDocument();
    expect(
      fetch.mock.calls.some(([path]) => path.endsWith("/login/browser")),
    ).toBe(false);
  });

  it("账号卡片优先显示头像，缺少头像时使用昵称首字", async () => {
    accountsPage();
    const avatar = "https://sns-avatar-qc.xhscdn.com/avatar/example.webp";
    service([{ ...A, avatar }, B]);
    render(<App />);
    const rowA = await screen.findByRole("region", { name: "账号 账号A" });
    const image = rowA.querySelector(".accounts-avatar img");
    expect(image).toHaveAttribute("src", avatar);
    expect(image).toHaveAttribute("referrerpolicy", "no-referrer");
    fireEvent.error(image);
    expect(rowA.querySelector(".accounts-avatar img")).toBeNull();
    expect(rowA.querySelector(".accounts-avatar")).toHaveTextContent("作");
    const rowB = screen.getByRole("region", { name: "账号 账号B" });
    expect(rowB.querySelector(".accounts-avatar img")).toBeNull();
    expect(rowB.querySelector(".accounts-avatar")).toHaveTextContent("账");
  });

  it("绑定账号状态未知时先检查，确认失效后才提供重新登录", async () => {
    accountsPage();
    const fetch = service([{ ...expiredA, login_checked_at: null }]);
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    expect(within(row).getByText(/登录状态待检查/)).toBeVisible();
    expect(
      within(row).queryByRole("button", { name: "重新登录" }),
    ).not.toBeInTheDocument();
    fireEvent.click(within(row).getByRole("button", { name: "刷新状态" }));
    fireEvent.click(
      await within(row).findByRole("button", { name: "重新登录" }),
    );
    await screen.findByRole("dialog", { name: "扫码登录 · 账号A" });
    expect(
      fetch.mock.calls.filter(([path]) => path.endsWith("/account/verify")),
    ).toHaveLength(1);
    expect(
      fetch.mock.calls.filter(
        ([path, options]) =>
          path.endsWith("/login/browser") && options.method === "POST",
      ),
    ).toHaveLength(1);
  });

  it.each([true, false])(
    "检查异常不把已登录或未知账号当成失效（已登录：%s）",
    async (loggedIn) => {
      accountsPage();
      const fetch = service([
        loggedIn ? A : { ...expiredA, login_checked_at: null },
      ]);
      const original = fetch.getMockImplementation();
      fetch.mockImplementation((path, options) =>
        path.endsWith("/account/verify")
          ? Promise.resolve({
              ok: false,
              status: 503,
              json: async () => ({ success: false, error: "检查暂时失败" }),
            })
          : original(path, options),
      );
      render(<App />);
      const row = await screen.findByRole("region", { name: "账号 账号A" });
      fireEvent.click(within(row).getByRole("button", { name: "刷新状态" }));
      await screen.findByText("暂时无法检查登录状态，请稍后重试。");
      expect(
        within(row).queryByRole("button", { name: "重新登录" }),
      ).not.toBeInTheDocument();
      expect(
        fetch.mock.calls.some(([path]) => path.endsWith("/login/browser")),
      ).toBe(false);
    },
  );

  it("未绑定的新账号保留首次登录入口", async () => {
    accountsPage();
    const fetch = service([B]);
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号B" });
    expect(
      within(row).queryByRole("button", { name: "重新登录" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      within(row).getByRole("button", { name: "登录", exact: true }),
    );
    await screen.findByRole("dialog", { name: "扫码登录 · 账号B" });
    const request = fetch.mock.calls.find(
      ([path, options]) =>
        path.endsWith("/login/browser") && options.method === "POST",
    );
    expect(request[1].headers["X-XHS-Account-ID"]).toBe("B");
  });

  it("已有登录窗口只显示继续登录，恢复原会话", async () => {
    accountsPage();
    const fetch = service([A], "desktop");
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) =>
      path === "/api/v1/accounts"
        ? Promise.resolve(
            ok({
              accounts: [A],
              legacy_account_id: "A",
              active_login: { account_id: "A", session_id: "session-C" },
            }),
          )
        : original(path, options),
    );
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    expect(
      within(row).queryByRole("button", { name: "重新登录" }),
    ).not.toBeInTheDocument();
    fireEvent.click(within(row).getByRole("button", { name: "继续登录" }));
    const dialog = await screen.findByRole("dialog", {
      name: "扫码登录 · 账号A",
    });
    const frame =
      await within(dialog).findByTitle("小红书登录和账号安全验证浏览器");
    expect(frame.getAttribute("src")).toContain("/desktop/session-C/");
  });

  it("巡检缓存确认失效才出现重新登录，恢复登录后自动隐藏", async () => {
    vi.useFakeTimers();
    accountsPage();
    let cached = A;
    const fetch = service([A]);
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) =>
      path === "/api/v1/accounts"
        ? Promise.resolve(ok({ accounts: [cached], legacy_account_id: "A" }))
        : original(path, options),
    );
    render(<App />);
    await act(async () => {});
    const row = screen.getByRole("region", { name: "账号 账号A" });
    expect(
      within(row).queryByRole("button", { name: "重新登录" }),
    ).not.toBeInTheDocument();
    cached = expiredA;
    await act(async () => {
      vi.advanceTimersByTime(60000);
    });
    expect(within(row).getByRole("button", { name: "重新登录" })).toBeVisible();
    cached = A;
    await act(async () => {
      vi.advanceTimersByTime(60000);
    });
    expect(
      within(row).queryByRole("button", { name: "重新登录" }),
    ).not.toBeInTheDocument();
    expect(
      fetch.mock.calls.filter(
        ([path]) =>
          path.endsWith("/account/verify") || path.endsWith("/login/browser"),
      ),
    ).toHaveLength(0);
  });

  it("普通加载、页面切换、恢复可见和账号切换仅展示缓存，不自动检查登录", async () => {
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    localStorage.setItem("hongshu-workbench.account.v1", "A");
    const fetch = service();
    render(<App />);
    await waitFor(() =>
      expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号A"),
    );
    expect(
      document.querySelector('[data-page="accounts"] .status-dot'),
    ).toHaveClass("connected");
    await navigatePage("accounts");
    const rowA = screen.getByRole("region", { name: "账号 账号A" });
    const rowB = screen.getByRole("region", { name: "账号 账号B" });
    expect(within(rowA).getByText(/已登录 · 浏览器已停止/)).toBeVisible();
    expect(within(rowB).getByText(/未登录 · 浏览器已停止/)).toBeVisible();
    expect(listCalls(fetch)).toHaveLength(2);
    fireEvent.click(within(rowB).getByRole("button", { name: "切换账号" }));
    expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号B");
    await act(async () => {
      hidden.mockReturnValue(true);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => {
      hidden.mockReturnValue(false);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(listCalls(fetch)).toHaveLength(3);
    await navigatePage("workspace");
    expect(
      fetch.mock.calls.filter(([url]) => url.endsWith("/account/verify")),
    ).toHaveLength(0);
    hidden.mockRestore();
  });

  it("账号页可见时每60秒静默刷新，保留卡片、当前账号和未保存草稿", async () => {
    vi.useFakeTimers();
    accountsPage();
    const fetch = service([A]);
    const original = fetch.getMockImplementation();
    const late = deferred();
    let pending = false;
    fetch.mockImplementation((url, options) =>
      url === "/api/v1/accounts" && pending
        ? late.promise
        : original(url, options),
    );
    render(<App />);
    await act(async () => {});
    await navigatePage("workspace");
    fireEvent.click(screen.getByRole("tab", { name: "撰写文案" }));
    fireEvent.change(screen.getByLabelText(/笔记标题/), {
      target: { value: "正在写的草稿" },
    });
    await navigatePage("accounts");
    expect(listCalls(fetch)).toHaveLength(2);
    pending = true;
    await act(async () => vi.advanceTimersByTime(59999));
    expect(listCalls(fetch)).toHaveLength(2);
    await act(async () => vi.advanceTimersByTime(1));
    expect(listCalls(fetch)).toHaveLength(3);
    expect(screen.queryByText("正在读取账号列表…")).not.toBeInTheDocument();
    expect(screen.getByRole("region", { name: "账号 账号A" })).toBeVisible();
    await act(async () =>
      late.resolve(ok({ accounts: [A], legacy_account_id: "A" })),
    );
    expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号A");
    await navigatePage("workspace");
    expect(screen.getByLabelText(/笔记标题/)).toHaveValue("正在写的草稿");
    await act(async () => vi.advanceTimersByTime(120000));
    expect(listCalls(fetch)).toHaveLength(3);
  });

  it("隐藏和卸载取消列表读取，恢复可见后旧响应不能覆盖新缓存", async () => {
    vi.useFakeTimers();
    accountsPage();
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    const fetch = service([A]);
    const original = fetch.getMockImplementation();
    const old = deferred();
    let pending = false;
    fetch.mockImplementation((url, options) =>
      url === "/api/v1/accounts" && pending
        ? old.promise
        : original(url, options),
    );
    const { unmount } = render(<App />);
    await act(async () => {});
    pending = true;
    await act(async () => vi.advanceTimersByTime(60000));
    const oldSignal = listCalls(fetch).at(-1)[1].signal;
    await act(async () => {
      hidden.mockReturnValue(true);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(oldSignal.aborted).toBe(true);
    await act(async () => vi.advanceTimersByTime(120000));
    expect(listCalls(fetch)).toHaveLength(2);
    pending = false;
    await act(async () => {
      hidden.mockReturnValue(false);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(listCalls(fetch)).toHaveLength(3);
    await act(async () => old.resolve(ok({ accounts: [B] })));
    expect(screen.getByRole("region", { name: "账号 账号A" })).toBeVisible();
    expect(
      screen.queryByRole("region", { name: "账号 账号B" }),
    ).not.toBeInTheDocument();
    pending = true;
    fireEvent.click(screen.getByRole("button", { name: "刷新列表" }));
    const signal = listCalls(fetch).at(-1)[1].signal;
    unmount();
    expect(signal.aborted).toBe(true);
    hidden.mockRestore();
  });

  it("切换页面时保留正在读取的列表，返回后仍能选中原账号", async () => {
    const late = deferred();
    const fetch = service([A]);
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((url, options) =>
      url === "/api/v1/accounts" ? late.promise : original(url, options),
    );
    render(<App />);
    const signal = listCalls(fetch)[0][1].signal;
    await navigatePage("settings");
    expect(signal.aborted).toBe(false);
    await act(async () =>
      late.resolve(ok({ accounts: [A], legacy_account_id: "A" })),
    );
    expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号A");
    expect(screen.queryByText("正在读取账号列表…")).not.toBeInTheDocument();
    expect(
      fetch.mock.calls.filter(([url]) => url.endsWith("/account/verify")),
    ).toHaveLength(0);
  });

  it("手动检查刷新账号缓存并取消旧列表，迟到响应不能回退刚登录的结果", async () => {
    accountsPage();
    let cached = { ...A, is_logged_in: false, login_checked_at: null };
    const fetch = service([cached]);
    const original = fetch.getMockImplementation();
    const old = deferred();
    let reads = 0;
    fetch.mockImplementation((url, options) => {
      if (url === "/api/v1/accounts") {
        if (++reads === 2) return old.promise;
        return Promise.resolve(
          ok({ accounts: [cached], legacy_account_id: "A" }),
        );
      }
      if (url.endsWith("/account/verify")) {
        cached = A;
        return Promise.resolve(
          ok({
            account_id: "A",
            status: "logged_in",
            is_logged_in: true,
            username: "作者A",
          }),
        );
      }
      return original(url, options);
    });
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    expect(within(row).getByText(/登录状态待检查/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "刷新列表" }));
    const oldSignal = listCalls(fetch).at(-1)[1].signal;
    fireEvent.click(within(row).getByRole("button", { name: "刷新状态" }));
    await within(row).findByText(/已登录/);
    expect(oldSignal.aborted).toBe(true);
    await act(async () =>
      old.resolve(
        ok({
          accounts: [{ ...A, is_logged_in: false }],
          legacy_account_id: "A",
        }),
      ),
    );
    expect(within(row).getByText(/已登录/)).toBeVisible();
    expect(listCalls(fetch)).toHaveLength(3);
    expect(
      fetch.mock.calls.filter(([url]) => url.endsWith("/account/verify")),
    ).toHaveLength(1);
  });

  it("扫码轮询完成后刷新列表一次并停止轮询", async () => {
    vi.useFakeTimers();
    accountsPage();
    let loggedIn = false;
    const fetch = service([{ ...A, is_logged_in: false }], "desktop");
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((url, options) => {
      if (url === "/api/v1/accounts")
        return Promise.resolve(
          ok({
            accounts: [{ ...A, is_logged_in: loggedIn }],
            legacy_account_id: "A",
          }),
        );
      if (url.endsWith("/login/session")) {
        loggedIn = true;
        return Promise.resolve(
          ok({
            account_id: "A",
            is_logged_in: true,
            status: "logged_in",
            username: "作者A",
          }),
        );
      }
      return original(url, options);
    });
    render(<App />);
    await act(async () => {});
    const row = screen.getByRole("region", { name: "账号 账号A" });
    fireEvent.click(within(row).getByRole("button", { name: "重新登录" }));
    await act(async () => {});
    expect(screen.getByTitle("小红书登录和账号安全验证浏览器")).toBeVisible();
    await act(async () => vi.advanceTimersByTime(2000));
    expect(
      screen.queryByRole("dialog", { name: "扫码登录 · 账号A" }),
    ).not.toBeInTheDocument();
    expect(within(row).getByText(/已登录/)).toBeVisible();
    expect(listCalls(fetch)).toHaveLength(2);
    await act(async () => vi.advanceTimersByTime(10000));
    expect(listCalls(fetch)).toHaveLength(2);
    expect(
      fetch.mock.calls.filter(([url]) => url.endsWith("/login/session")),
    ).toHaveLength(1);
  });

  it("重置成功刷新缓存，不启动登录检查并保留当前草稿", async () => {
    accountsPage();
    const fetch = service([A]);
    fetch.seedDraft("A", { title: "保留的草稿" });
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    fireEvent.click(
      within(row).getByRole("button", { name: "重置浏览器数据" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "确认重置" }));
    await within(row).findByText(/未登录/);
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "重置浏览器数据？" }),
      ).not.toBeInTheDocument(),
    );
    expect(listCalls(fetch)).toHaveLength(2);
    expect(
      fetch.mock.calls.filter(([url]) => url.endsWith("/account/verify")),
    ).toHaveLength(0);
    await navigatePage("workspace");
    fireEvent.click(screen.getByRole("tab", { name: "撰写文案" }));
    expect(screen.getByLabelText(/笔记标题/)).toHaveValue("保留的草稿");
  });
  it("仅有一个账号时自动选择，其他标签页更新最近 ID 不切换本页", async () => {
    service([A]);
    render(<App />);
    await waitFor(() =>
      expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号A"),
    );
    expect(localStorage.getItem("hongshu-workbench.account.v1")).toBe("A");
    localStorage.setItem("hongshu-workbench.account.v1", "B");
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: "hongshu-workbench.account.v1",
        newValue: "B",
      }),
    );
    expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号A");
  });

  it("已保存 ID 被删除后即使只剩一个账号也不自动切换，仍能打开设置", async () => {
    localStorage.setItem("hongshu-workbench.account.v1", "removed");
    service([B]);
    render(<App />);
    await screen.findByText("请选择创作账号");
    expect(screen.getByLabelText("当前账号")).toHaveTextContent("尚未选择账号");
    fireEvent.click(screen.getByRole("button", { name: "打开设置" }));
    await screen.findByRole("heading", { name: "设置", level: 1 });
    expect(localStorage.getItem("hongshu-workbench.account.v1")).toBe(
      "removed",
    );
  });

  it("选择 A/B 重新加载各自草稿，顶部和预览显示当前账号", async () => {
    accountsPage();
    const fetch = service();
    fetch.seedDraft("A", { title: "A标题" });
    fetch.seedDraft("B", { title: "B标题" });
    render(<App />);
    const rowA = await screen.findByRole("region", { name: "账号 账号A" });
    fireEvent.click(within(rowA).getByRole("button", { name: "切换账号" }));
    await waitFor(() =>
      expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号A"),
    );
    const rowB = screen.getByRole("region", { name: "账号 账号B" });
    fireEvent.click(within(rowB).getByRole("button", { name: "切换账号" }));
    await waitFor(() =>
      expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号B"),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "工作台", exact: true }),
    );
    await screen.findByRole("heading", { name: "今天，想分享什么？" });
    fireEvent.click(screen.getByRole("tab", { name: "撰写文案" }));
    expect(screen.getByLabelText(/笔记标题/)).toHaveValue("B标题");
    expect(document.querySelector("#preview-author")).toHaveTextContent(
      "账号B",
    );
    expect(
      fetch.mock.calls.filter(([url]) => url.endsWith("/account/verify")),
    ).toHaveLength(0);
  });

  it("添加账号先取得服务端 ID，再选中并直接打开该账号的扫码窗口", async () => {
    accountsPage();
    const fetch = service([]);
    render(<App />);
    await waitFor(() =>
      expect(screen.queryByText("正在读取账号列表…")).not.toBeInTheDocument(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "添加账号", exact: true }),
    );
    fireEvent.change(screen.getByLabelText("账号备注名"), {
      target: { value: "新账号" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存", exact: true }));
    await waitFor(() =>
      expect(
        fetch.mock.calls.some(
          ([url, options]) =>
            url === "/api/v1/login/browser" && options.method === "POST",
        ),
      ).toBe(true),
    );
    expect(screen.getByLabelText("当前账号")).toHaveTextContent("新账号");
    const post = fetch.mock.calls.find(
      ([url, options]) =>
        url === "/api/v1/accounts" && options.method === "POST",
    );
    expect(JSON.parse(post[1].body)).toEqual({ name: "新账号" });
    expect(post[1].headers).not.toHaveProperty("X-XHS-Account-ID");
    const open = fetch.mock.calls.find(
      ([url, options]) =>
        url === "/api/v1/login/browser" && options.method === "POST",
    );
    expect(open[1].headers["X-XHS-Account-ID"]).toBe("C");
    const dialog = await screen.findByRole("dialog", {
      name: "扫码登录 · 新账号",
    });
    expect(dialog).toHaveAttribute("open");
    expect(
      within(dialog).getByText(/登录浏览器已在运行本服务的电脑上打开/),
    ).toBeVisible();
  });

  it.each(["关闭按钮", "Esc"])(
    "%s 收起扫码弹窗后保留后台会话，继续登录可重新打开并主动结束",
    async (dismiss) => {
      accountsPage();
      const fetch = service([expiredA], "desktop");
      render(<App />);
      const row = await screen.findByRole("region", { name: "账号 账号A" });
      fireEvent.click(within(row).getByRole("button", { name: "重新登录" }));
      const dialog = await screen.findByRole("dialog", {
        name: "扫码登录 · 账号A",
      });
      const frame =
        await within(dialog).findByTitle("小红书登录和账号安全验证浏览器");
      expect(dialog).toHaveAttribute("open");
      expect(frame.closest("dialog")).toBe(dialog);
      expect(
        within(dialog).getByText("请在登录网页中使用小红书 App 扫码登录。"),
      ).toBeVisible();
      const address = frame.getAttribute("src");
      if (dismiss === "Esc") {
        fireEvent(dialog, new Event("cancel", { cancelable: true }));
      } else {
        fireEvent.click(
          within(dialog).getByRole("button", {
            name: "收起登录网页，保留后台登录浏览器",
          }),
        );
      }
      expect(
        screen.queryByRole("dialog", { name: "扫码登录 · 账号A" }),
      ).not.toBeInTheDocument();
      expect(frame).not.toBeInTheDocument();
      expect(
        fetch.mock.calls.some(([, options]) => options.method === "DELETE"),
      ).toBe(false);

      fireEvent.click(within(row).getByRole("button", { name: "继续登录" }));
      const reopened = await screen.findByRole("dialog", {
        name: "扫码登录 · 账号A",
      });
      expect(
        await within(reopened).findByTitle("小红书登录和账号安全验证浏览器"),
      ).toHaveAttribute("src", address);
      fireEvent.click(
        within(reopened).getByRole("button", { name: "结束登录窗口" }),
      );
      await waitFor(() =>
        expect(
          screen.queryByRole("dialog", { name: "扫码登录 · 账号A" }),
        ).not.toBeInTheDocument(),
      );
      const close = fetch.mock.calls.find(
        ([url, options]) =>
          url === "/api/v1/login/browser?session_id=session-C" &&
          options.method === "DELETE",
      );
      expect(close).toBeDefined();
      expect(close[1].headers["X-XHS-Account-ID"]).toBe("A");
    },
  );

  it("原始大小切换仅调整显示比例，保留登录会话且不重新打开浏览器", async () => {
    accountsPage();
    const fetch = service([expiredA], "desktop");
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    fireEvent.click(within(row).getByRole("button", { name: "重新登录" }));
    const dialog = await screen.findByRole("dialog", {
      name: "扫码登录 · 账号A",
    });
    const frame =
      await within(dialog).findByTitle("小红书登录和账号安全验证浏览器");
    const original = new URL(frame.getAttribute("src"), location.origin);
    fireEvent.click(within(dialog).getByRole("button", { name: "原始大小" }));
    const unscaled = new URL(frame.getAttribute("src"), location.origin);
    expect(unscaled.searchParams.get("resize")).toBe("off");
    expect(unscaled.pathname).toBe(original.pathname);
    expect(unscaled.searchParams.get("path")).toBe(
      original.searchParams.get("path"),
    );
    expect(
      within(dialog).getByRole("link", { name: "在新窗口查看" }),
    ).toHaveAttribute("href", `${unscaled.pathname}${unscaled.search}`);
    fireEvent.click(within(dialog).getByRole("button", { name: "适应窗口" }));
    expect(frame).toHaveAttribute(
      "src",
      `${original.pathname}${original.search}`,
    );
    expect(
      fetch.mock.calls.filter(
        ([url, options]) =>
          url === "/api/v1/login/browser" && options.method === "POST",
      ),
    ).toHaveLength(1);
    expect(
      fetch.mock.calls.some(([, options]) => options.method === "DELETE"),
    ).toBe(false);
  });

  it("登录检查失败时在弹窗中提示，确认登录成功后自动收起", async () => {
    accountsPage();
    const fetch = service([expiredA], "desktop");
    const original = fetch.getMockImplementation();
    let status = "not_logged_in";
    fetch.mockImplementation((url, options) => {
      if (url === "/api/v1/accounts")
        return Promise.resolve(
          ok({
            accounts: [{ ...expiredA, is_logged_in: status === "logged_in" }],
            legacy_account_id: "A",
          }),
        );
      if (url.endsWith("/account/verify") && status === "error")
        return Promise.resolve({
          ok: false,
          status: 503,
          json: async () => ({ success: false, message: "暂时无法检查登录" }),
        });
      if (url.endsWith("/account/verify") && status === "logged_in")
        return Promise.resolve(
          ok({
            account_id: "A",
            status: "logged_in",
            is_logged_in: true,
            username: "作者A",
          }),
        );
      return original(url, options);
    });
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    fireEvent.click(within(row).getByRole("button", { name: "重新登录" }));
    const dialog = await screen.findByRole("dialog", {
      name: "扫码登录 · 账号A",
    });
    await within(dialog).findByTitle("小红书登录和账号安全验证浏览器");
    status = "error";
    fireEvent.click(
      within(dialog).getByRole("button", { name: "检查登录状态" }),
    );
    expect(await within(dialog).findByText(/暂时无法检查登录/)).toBeVisible();
    status = "logged_in";
    fireEvent.click(
      within(dialog).getByRole("button", { name: "检查登录状态" }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "扫码登录 · 账号A" }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/小红书登录成功，登录信息已保存/)).toBeVisible();
    fireEvent.click(within(row).getByRole("button", { name: "刷新状态" }));
    await waitFor(() =>
      expect(
        screen.queryByText(/小红书登录成功，登录信息已保存/),
      ).not.toBeInTheDocument(),
    );
  });

  it("令牌变化重新加载账号，旧列表响应不能替换新令牌下的选择", async () => {
    let resolveOld;
    const old = new Promise((resolve) => {
      resolveOld = resolve;
    });
    const fetch = service([B]);
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((url, options) =>
      url === "/api/v1/accounts" && !options.headers.Authorization
        ? old
        : original(url, options),
    );
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "打开设置" }));
    await screen.findByRole("heading", { name: "设置", level: 1 });
    fireEvent.change(screen.getByLabelText("工作台访问令牌"), {
      target: { value: "new-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() =>
      expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号B"),
    );
    resolveOld(ok({ accounts: [A], legacy_account_id: "A" }));
    await waitFor(() =>
      expect(
        fetch.mock.calls.filter(([url]) => url === "/api/v1/accounts"),
      ).toHaveLength(2),
    );
    expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号B");
    const requests = fetch.mock.calls.filter(
      ([url]) => url === "/api/v1/accounts",
    );
    expect(requests[0][1].signal.aborted).toBe(true);
    expect(requests[1][1].headers.Authorization).toBe("Bearer new-token");
  });

  it("清空模型配置先确认共享范围，确认后调用全局接口并保留访问令牌", async () => {
    history.replaceState(null, "", "/web/#settings");
    localStorage.setItem("hongshu-workbench.token.v1", "service-token");
    const fetch = service([A]);
    render(<App />);
    const clear = await screen.findByRole("button", { name: "清空模型配置" });
    await waitFor(() => expect(clear).toBeEnabled());
    fireEvent.click(clear);
    let dialog = await screen.findByRole("dialog", {
      name: "清空服务端模型配置？",
    });
    expect(
      within(dialog).getByText(/所有使用此工作台的浏览器都会受到影响/),
    ).toBeVisible();
    expect(
      fetch.mock.calls.some(([, options]) => options.method === "DELETE"),
    ).toBe(false);
    fireEvent.click(
      within(dialog).getByRole("button", { name: "取消", exact: true }),
    );
    expect(
      screen.queryByRole("dialog", { name: "清空服务端模型配置？" }),
    ).not.toBeInTheDocument();
    fireEvent.click(clear);
    dialog = await screen.findByRole("dialog", {
      name: "清空服务端模型配置？",
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "确认清空" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "清空服务端模型配置？" }),
      ).not.toBeInTheDocument(),
    );
    const request = fetch.mock.calls.find(
      ([path, options]) =>
        path === "/api/v1/settings" && options.method === "DELETE",
    );
    expect(request[1].headers.Authorization).toBe("Bearer service-token");
    expect(request[1].headers).not.toHaveProperty("X-XHS-Account-ID");
    expect(localStorage.getItem("hongshu-workbench.token.v1")).toBe(
      "service-token",
    );
  });

  it("重置和移除先说明范围并确认，删除当前账号后不自动选择其他账号", async () => {
    accountsPage();
    localStorage.setItem("hongshu-workbench.account.v1", "A");
    const fetch = service();
    fetch.seedDraft("A", { title: "A草稿" });
    render(<App />);
    const row = await screen.findByRole("region", { name: "账号 账号A" });
    await waitFor(() =>
      expect(screen.getByLabelText("当前账号")).toHaveTextContent("账号A"),
    );
    fireEvent.click(
      within(row).getByRole("button", { name: "重置浏览器数据" }),
    );
    expect(
      screen.getByText(/Cookie、站点存储、缓存、权限和配置/),
    ).toBeVisible();
    expect(
      fetch.mock.calls.some(([url]) => url.includes("reset-profile")),
    ).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "取消", exact: true }));
    fireEvent.click(
      within(row).getByRole("button", { name: "移除", exact: true }),
    );
    expect(screen.getByText(/下载文件、托管图片及本浏览器草稿/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "确认移除" }));
    await waitFor(() =>
      expect(screen.getByLabelText("当前账号")).toHaveTextContent(
        "尚未选择账号",
      ),
    );
    expect(localStorage.getItem("hongshu-workbench.draft.v1:A")).toBeNull();
    expect(localStorage.getItem("hongshu-workbench.account.v1")).toBe("A");
    expect(
      fetch.mock.calls.some(
        ([url, options]) =>
          url === "/api/v1/accounts/A" && options.method === "DELETE",
      ),
    ).toBe(true);
  });
});
