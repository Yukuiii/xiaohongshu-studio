import {
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
  browser_status: "stopped",
  busy: false,
};
const B = { id: "B", name: "账号B", browser_status: "stopped", busy: false };
const ok = (data) => ({
  ok: true,
  status: 200,
  json: async () => ({ success: true, data }),
});

function service(accounts = [A, B]) {
  const fetch = vi.fn(async (url, options) => {
    if (url === "/api/v1/accounts" && options.method === "POST")
      return ok({ account: { id: "C", name: "新账号" } });
    if (url === "/api/v1/accounts")
      return ok({ accounts, legacy_account_id: "A" });
    if (url.endsWith("/login/browser"))
      return ok({
        account_id: options.headers["X-XHS-Account-ID"],
        session_id: "session-C",
        mode: "native",
        status: "waiting_scan",
        is_logged_in: false,
      });
    if (url.endsWith("/login/status"))
      return ok({
        account_id: options.headers["X-XHS-Account-ID"],
        is_logged_in: false,
        status: "not_logged_in",
      });
    return ok({});
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

function accountsPage() {
  history.replaceState(null, "", "/web/#accounts");
}

describe("账号选择界面", () => {
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
    localStorage.setItem(
      "hongshu-workbench.draft.v1:A",
      JSON.stringify({ title: "A标题" }),
    );
    localStorage.setItem(
      "hongshu-workbench.draft.v1:B",
      JSON.stringify({ title: "B标题" }),
    );
    const fetch = service();
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
    expect(screen.getByLabelText(/笔记标题/)).toHaveValue("B标题");
    expect(document.querySelector("#preview-author")).toHaveTextContent(
      "账号B",
    );
    expect(
      fetch.mock.calls
        .filter(([url]) => url.endsWith("/login/status"))
        .map(([, options]) => options.headers["X-XHS-Account-ID"]),
    ).toEqual(expect.arrayContaining(["A", "B"]));
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
  });

  it("令牌变化重新加载账号，旧列表响应不能替换新令牌下的选择", async () => {
    let resolveOld;
    const old = new Promise((resolve) => {
      resolveOld = resolve;
    });
    const fetch = service([B]);
    fetch.mockImplementationOnce(() => old);
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

  it("重置和移除先说明范围并确认，删除当前账号后不自动选择其他账号", async () => {
    accountsPage();
    localStorage.setItem("hongshu-workbench.account.v1", "A");
    localStorage.setItem(
      "hongshu-workbench.draft.v1:A",
      JSON.stringify({ title: "A草稿" }),
    );
    const fetch = service();
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
