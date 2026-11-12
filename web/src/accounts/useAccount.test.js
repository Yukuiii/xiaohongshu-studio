import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useAccount } from "./useAccount.js";

const context = (accountId, generation = 1, token = "token") => ({
  accountId,
  generation,
  token,
});
const status = (accountId, extra = {}) => ({
  account_id: accountId,
  is_logged_in: false,
  status: "not_logged_in",
  ...extra,
});
const login = (session = "session-A") =>
  status("A", {
    status: "waiting_scan",
    session_id: session,
    mode: "desktop",
    desktop_url: `/desktop/${session}/vnc.html?autoconnect=1&resize=scale&path=desktop%2F${session}%2Fwebsockify`,
  });
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

describe("登录请求隔离", () => {
  it("自动检查失败不显示错误横幅", async () => {
    const api = vi.fn().mockRejectedValue(new Error("浏览器启动失败"));
    const { result } = renderHook(() =>
      useAccount({ api, active: true, busy: false, context: fixed }),
    );
    await act(async () => {});
    expect(api).toHaveBeenCalledTimes(1);
    expect(result.current.notice.message).toBe("");
    expect(result.current.operation).toBe("");
  });

  it("轮询遇到请求取消时保留当前提示，并继续检查登录结果", async () => {
    vi.useFakeTimers();
    const api = vi
      .fn()
      .mockResolvedValueOnce(status("A"))
      .mockResolvedValueOnce(login())
      .mockRejectedValueOnce(
        new Error("检查登录状态失败：等待浏览器启动: context canceled"),
      )
      .mockResolvedValueOnce(
        status("A", { is_logged_in: true, status: "logged_in" }),
      );
    const { result } = renderHook(() =>
      useAccount({ api, active: true, busy: false, context: fixed }),
    );
    await act(async () => {});
    await act(async () => {
      await result.current.openLogin();
    });
    const notice = result.current.notice;
    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    expect(result.current.notice).toEqual(notice);
    expect(result.current.browserSession).toBe(true);
    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    expect(api).toHaveBeenCalledTimes(4);
    expect(result.current.loggedIn).toBe(true);
    expect(result.current.loginVisible).toBe(false);
  });

  it.each([
    new DOMException("请求已取消", "AbortError"),
    new Error("检查登录状态失败：等待浏览器启动: context canceled"),
  ])("手动检查取消后恢复原提示，不残留检查进度或错误：%s", async (error) => {
    const api = vi.fn().mockResolvedValue(status("A"));
    const { result } = renderHook(() =>
      useAccount({ api, active: true, busy: false, context: fixed }),
    );
    await act(async () => {});
    const notice = result.current.notice;
    api.mockRejectedValueOnce(error);
    await act(async () => {
      await result.current.checkStatus();
    });
    expect(result.current.notice).toEqual(notice);
    expect(result.current.operation).toBe("");
  });

  it("切换账号取消轮询，清空桌面，迟到的 A 状态不能覆盖 B", async () => {
    vi.useFakeTimers();
    const late = deferred();
    let reads = 0;
    const api = vi.fn((path, options) => {
      if (path === "/login/browser") return Promise.resolve(login());
      if (options.accountId === "A" && ++reads > 1) return late.promise;
      return Promise.resolve(status(options.accountId));
    });
    const { result, rerender } = renderHook(
      (props) => useAccount({ api, active: true, busy: false, ...props }),
      { initialProps: { context: context("A") } },
    );
    await act(async () => {});
    await act(async () => {
      await result.current.openLogin();
    });
    expect(result.current.desktopURL).toContain("/desktop/session-A/");
    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    const polling = api.mock.calls.at(-1)[1];
    await act(async () => {
      rerender({ context: context("B", 2) });
    });
    expect(polling.signal.aborted).toBe(true);
    expect(result.current.desktopURL).toBe("");
    expect(result.current.loginVisible).toBe(false);
    await act(async () => {
      late.resolve(
        status("A", {
          is_logged_in: true,
          status: "logged_in",
          username: "迟到A",
        }),
      );
    });
    expect(result.current.status.account_id).toBe("B");
    expect(result.current.username).not.toBe("迟到A");
    const calls = api.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(10000);
    });
    expect(api).toHaveBeenCalledTimes(calls);
  });

  it("切换时取消发布所需的 requiredRequests，并拒绝迟到身份", async () => {
    const late = deferred();
    const api = vi.fn().mockResolvedValue(status("A"));
    const { result, rerender } = renderHook(
      (props) => useAccount({ api, active: true, busy: false, ...props }),
      { initialProps: { context: context("A") } },
    );
    await act(async () => {});
    api.mockImplementationOnce(() => late.promise);
    const pending = result.current.requireLogin();
    const assertion = expect(pending).rejects.toMatchObject({
      name: "AbortError",
    });
    const signal = api.mock.calls.at(-1)[1].signal;
    api.mockResolvedValue(status("B"));
    await act(async () => {
      rerender({ context: context("B", 2) });
    });
    expect(signal.aborted).toBe(true);
    await act(async () => {
      late.resolve(status("A", { is_logged_in: true, status: "logged_in" }));
      await assertion;
    });
    expect(result.current.status.account_id).toBe("B");
  });

  it("仅接受匹配会话的同源桌面地址，结束时携带 session_id 和账号", async () => {
    const api = vi.fn().mockResolvedValue(status("A"));
    const onLoginSession = vi.fn();
    const { result } = renderHook(() =>
      useAccount({
        api,
        active: true,
        busy: false,
        context: fixed,
        onLoginSession,
      }),
    );
    await act(async () => {});
    api.mockResolvedValueOnce(login());
    await act(async () => {
      await result.current.openLogin();
    });
    expect(result.current.browserConnected).toBe(true);
    expect(onLoginSession).toHaveBeenLastCalledWith(
      { account_id: "A", session_id: "session-A" },
      "A",
    );
    await act(async () => {
      await result.current.closeLogin();
    });
    expect(onLoginSession).toHaveBeenLastCalledWith(null, "A");
    expect(api).toHaveBeenLastCalledWith(
      "/login/browser?session_id=session-A",
      expect.objectContaining({ method: "DELETE", accountId: "A" }),
    );
    api.mockResolvedValueOnce({
      ...login(),
      desktop_url:
        "/desktop/other/vnc.html?autoconnect=1&resize=scale&path=desktop%2Fother%2Fwebsockify",
    });
    await act(async () => {
      await result.current.openLogin();
    });
    expect(result.current.browserConnected).toBe(false);
    expect(result.current.notice.message).toContain("地址无效");
  });

  it("关闭窗口返回 503 BROWSER_CLOSE_FAILED 时保留会话及再次结束入口", async () => {
    const api = vi.fn().mockResolvedValue(status("A"));
    const onLoginSession = vi.fn();
    const { result } = renderHook(() =>
      useAccount({
        api,
        active: true,
        busy: false,
        context: fixed,
        onLoginSession,
      }),
    );
    await act(async () => {});
    api.mockResolvedValueOnce(login());
    await act(async () => {
      await result.current.openLogin();
    });
    onLoginSession.mockClear();
    api.mockRejectedValueOnce(
      Object.assign(new Error("浏览器关闭失败"), {
        status: 503,
        code: "BROWSER_CLOSE_FAILED",
      }),
    );
    await act(async () => {
      await result.current.closeLogin();
    });
    expect(result.current.browserSession).toBe(true);
    expect(result.current.sessionId).toBe("session-A");
    expect(result.current.loginVisible).toBe(true);
    expect(result.current.operation).toBe("");
    expect(result.current.notice.message).toContain(
      "再次点击「结束登录窗口」重试",
    );
    expect(onLoginSession).not.toHaveBeenCalledWith(null, "A");
    api.mockResolvedValueOnce({});
    await act(async () => {
      await result.current.closeLogin();
    });
    expect(api).toHaveBeenLastCalledWith(
      "/login/browser?session_id=session-A",
      expect.objectContaining({ method: "DELETE", accountId: "A" }),
    );
    expect(result.current.browserSession).toBe(false);
    expect(result.current.sessionId).toBe("");
  });

  it("相同账号的令牌代次变化使旧请求失效", async () => {
    const late = deferred();
    const api = vi
      .fn()
      .mockImplementationOnce(() => late.promise)
      .mockResolvedValue(status("A"));
    const { result, rerender } = renderHook(
      (props) => useAccount({ api, active: true, busy: false, ...props }),
      { initialProps: { context: context("A") } },
    );
    const signal = api.mock.calls[0][1].signal;
    await act(async () => {
      rerender({ context: context("A", 2, "new-token") });
    });
    expect(signal.aborted).toBe(true);
    expect(api.mock.calls.at(-1)[1].token).toBe("new-token");
    await act(async () => {
      late.resolve(
        status("A", {
          is_logged_in: true,
          status: "logged_in",
          username: "旧身份",
        }),
      );
    });
    expect(result.current.loggedIn).toBe(false);
  });
});

const fixed = context("A");
