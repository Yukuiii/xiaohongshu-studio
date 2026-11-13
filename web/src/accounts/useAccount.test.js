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
  it.each([
    {
      id: "A",
      user_id: "user-A",
      is_logged_in: true,
      login_checked_at: "2026-11-13T08:00:00Z",
    },
    { id: "A", user_id: "user-A", is_logged_in: false, login_checked_at: null },
  ])(
    "已登录或尚未检查的绑定账号不能直接打开新登录窗口：%s",
    async (summary) => {
      const api = vi.fn();
      const { result } = renderHook(() =>
        useAccount({
          api,
          active: true,
          busy: false,
          context: fixed,
          summary,
        }),
      );
      await act(async () => {
        await result.current.openLogin();
      });
      expect(api).not.toHaveBeenCalled();
      expect(result.current.loginVisible).toBe(false);
      expect(result.current.browserSession).toBe(false);
    },
  );

  it("旧点击回调在缓存恢复为已登录后不能新建登录会话", async () => {
    const api = vi.fn();
    const { result, rerender } = renderHook(
      (props) =>
        useAccount({
          api,
          active: true,
          busy: false,
          context: fixed,
          ...props,
        }),
      {
        initialProps: {
          summary: {
            id: "A",
            user_id: "user-A",
            is_logged_in: false,
            login_checked_at: "2026-11-13T08:00:00Z",
          },
        },
      },
    );
    const oldOpen = result.current.openLogin;
    rerender({
      summary: {
        id: "A",
        user_id: "user-A",
        is_logged_in: true,
        login_checked_at: "2026-11-13T08:10:00Z",
      },
    });
    await act(async () => {
      await oldOpen();
    });
    expect(api).not.toHaveBeenCalled();
    expect(result.current.loginVisible).toBe(false);
  });

  it("已有登录会话可以继续，即使正式 profile 的登录缓存仍有效", async () => {
    const api = vi.fn().mockResolvedValue(login());
    const { result } = renderHook(() =>
      useAccount({
        api,
        active: true,
        busy: false,
        context: fixed,
        summary: { id: "A", user_id: "user-A", is_logged_in: true },
        activeLogin: { account_id: "A", session_id: "session-A" },
      }),
    );
    await act(async () => {
      await result.current.openLogin();
    });
    expect(api).toHaveBeenCalledTimes(1);
    expect(result.current.sessionId).toBe("session-A");
    expect(result.current.desktopURL).toContain("/desktop/session-A/");
  });

  it("服务端确认账号已登录时静默收起陈旧窗口并同步缓存", async () => {
    const api = vi.fn().mockRejectedValue(
      Object.assign(new Error("账号已登录"), {
        status: 409,
        code: "ACCOUNT_ALREADY_LOGGED_IN",
      }),
    );
    const onStatus = vi.fn();
    const { result } = renderHook(() =>
      useAccount({
        api,
        active: true,
        busy: false,
        context: fixed,
        onStatus,
        summary: {
          id: "A",
          user_id: "user-A",
          is_logged_in: false,
          login_checked_at: "2026-11-13T08:00:00Z",
        },
        activeLogin: { account_id: "A", session_id: "old-session" },
      }),
    );
    await act(async () => {
      await result.current.openLogin();
    });
    expect(result.current.loginVisible).toBe(false);
    expect(result.current.browserSession).toBe(false);
    expect(result.current.desktopURL).toBe("");
    expect(result.current.notice.message).toBe("");
    expect(result.current.status.is_logged_in).toBe(true);
    expect(onStatus).toHaveBeenCalledWith(
      expect.objectContaining({ account_id: "A", is_logged_in: true }),
      fixed,
    );
    expect(api).toHaveBeenCalledTimes(1);
  });

  it.each([true, false])(
    "从账号缓存展示登录身份，初始化不检查浏览器（账号页：%s）",
    async (active) => {
      const api = vi.fn().mockResolvedValue(
        status("A", {
          is_logged_in: true,
          status: "logged_in",
          username: "作者A",
        }),
      );
      const { result } = renderHook(() =>
        useAccount({
          api,
          active,
          busy: false,
          context: fixed,
          summary: { id: "A", is_logged_in: true, nickname: "作者A" },
        }),
      );
      await act(async () => {});
      expect(api).not.toHaveBeenCalled();
      expect(result.current.loggedIn).toBe(true);
      expect(result.current.username).toBe("作者A");
      expect(result.current.status).toBeNull();
      expect(result.current.notice.message).toBe("");
      expect(result.current.loginVisible).toBe(false);
      if (active) {
        await act(async () => result.current.checkStatus());
        expect(api).toHaveBeenCalledTimes(1);
        expect(result.current.notice.message).toBe("");
      }
    },
  );

  it("页面切换、恢复可见和账号切换只读取传入缓存，不自动检查登录", async () => {
    const api = vi.fn();
    const { result, rerender } = renderHook(
      (props) => useAccount({ api, busy: false, ...props }),
      {
        initialProps: {
          active: false,
          context: fixed,
          summary: { id: "A", is_logged_in: true, nickname: "作者A" },
        },
      },
    );
    await act(async () => {
      rerender({
        active: true,
        context: fixed,
        summary: { id: "A", is_logged_in: false },
      });
    });
    expect(result.current.loggedIn).toBe(false);
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
      window.dispatchEvent(new Event("pageshow"));
      rerender({
        active: true,
        context: context("B", 2),
        summary: { id: "B", is_logged_in: true, nickname: "作者B" },
      });
    });
    expect(result.current.loggedIn).toBe(true);
    expect(result.current.username).toBe("作者B");
    expect(api).not.toHaveBeenCalled();
  });

  it("轮询遇到请求取消时保留当前提示，并继续检查登录结果", async () => {
    vi.useFakeTimers();
    const api = vi
      .fn()
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
    expect(api).toHaveBeenCalledTimes(3);
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
    const api = vi.fn((path, options) => {
      if (path === "/login/browser") return Promise.resolve(login());
      if (options.accountId === "A") return late.promise;
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
    expect(result.current.status).toBeNull();
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
    expect(result.current.status).toBeNull();
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

  it("刷新缓存不结束候选扫码会话或清除桌面", async () => {
    const api = vi.fn().mockResolvedValue(login());
    const { result, rerender } = renderHook(
      (props) =>
        useAccount({
          api,
          active: true,
          busy: false,
          context: fixed,
          ...props,
        }),
      { initialProps: { summary: { id: "A", is_logged_in: false } } },
    );
    await act(async () => result.current.openLogin());
    const address = result.current.desktopURL;
    await act(async () => {
      rerender({
        summary: { id: "A", is_logged_in: true, nickname: "已绑定作者" },
      });
    });
    expect(result.current.loggedIn).toBe(true);
    expect(result.current.username).toBe("已绑定作者");
    expect(result.current.status.status).toBe("waiting_scan");
    expect(result.current.browserSession).toBe(true);
    expect(result.current.browserConnected).toBe(true);
    expect(result.current.loginVisible).toBe(true);
    expect(result.current.desktopURL).toBe(address);
    expect(api).toHaveBeenCalledTimes(1);
  });

  it("已有 active_login 可恢复继续入口，普通页面不检查或轮询", async () => {
    vi.useFakeTimers();
    const api = vi.fn().mockResolvedValue(login());
    const { result, rerender } = renderHook(
      (props) =>
        useAccount({
          api,
          active: true,
          busy: false,
          context: fixed,
          ...props,
        }),
      { initialProps: { activeLogin: null } },
    );
    await act(async () => {
      rerender({ activeLogin: { account_id: "A", session_id: "session-A" } });
      vi.advanceTimersByTime(10000);
    });
    expect(api).not.toHaveBeenCalled();
    expect(result.current.browserSession).toBe(true);
    expect(result.current.sessionId).toBe("session-A");
    expect(result.current.loginVisible).toBe(false);
    await act(async () => result.current.openLogin());
    expect(result.current.desktopURL).toContain("/desktop/session-A/");
  });

  it("扫码每两秒轮询，只在弹窗可见且空闲时进行，完成只通知刷新一次", async () => {
    vi.useFakeTimers();
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    const onStatus = vi.fn();
    const api = vi
      .fn()
      .mockResolvedValueOnce(login())
      .mockResolvedValueOnce(
        status("A", {
          status: "waiting_confirmation",
          session_id: "session-A",
        }),
      )
      .mockResolvedValue(
        status("A", { is_logged_in: true, status: "logged_in" }),
      );
    const { result, rerender } = renderHook(
      (props) =>
        useAccount({ api, active: true, context: fixed, onStatus, ...props }),
      { initialProps: { busy: false } },
    );
    await act(async () => result.current.openLogin());
    await act(async () => rerender({ busy: true }));
    await act(async () => vi.advanceTimersByTime(10000));
    expect(api).toHaveBeenCalledTimes(1);
    await act(async () => rerender({ busy: false }));
    await act(async () => vi.advanceTimersByTime(1999));
    expect(api).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTime(1));
    expect(api).toHaveBeenCalledTimes(2);
    expect(onStatus).not.toHaveBeenCalled();
    await act(async () => {
      hidden.mockReturnValue(true);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => vi.advanceTimersByTime(10000));
    expect(api).toHaveBeenCalledTimes(2);
    await act(async () => {
      hidden.mockReturnValue(false);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => vi.advanceTimersByTime(2000));
    expect(result.current.loggedIn).toBe(true);
    expect(result.current.loginVisible).toBe(false);
    expect(onStatus).toHaveBeenCalledTimes(1);
    await act(async () => result.current.requireLogin());
    await act(async () => vi.advanceTimersByTime(10000));
    expect(api).toHaveBeenCalledTimes(4);
    expect(onStatus).toHaveBeenCalledTimes(1);
    await act(async () => result.current.checkStatus());
    expect(onStatus).toHaveBeenCalledTimes(2);
    hidden.mockRestore();
  });

  it("任务仍实时核验登录与账号响应，缓存不能绕过预检", async () => {
    const api = vi.fn().mockResolvedValue(status("A"));
    const { result } = renderHook(() =>
      useAccount({
        api,
        active: false,
        busy: true,
        context: fixed,
        summary: { id: "A", is_logged_in: true },
      }),
    );
    expect(api).not.toHaveBeenCalled();
    await act(async () => {
      await expect(result.current.requireLogin()).rejects.toThrow(
        "小红书尚未登录",
      );
    });
    expect(api).toHaveBeenLastCalledWith(
      "/login/status",
      expect.objectContaining({ accountId: "A", token: "token" }),
    );
    api.mockResolvedValueOnce(
      status("B", { is_logged_in: true, status: "logged_in" }),
    );
    await act(async () => {
      await expect(result.current.requireLogin()).rejects.toThrow(
        "登录响应与当前账号不一致",
      );
    });
  });

  it("登录转换只通知一次刷新，后续相同实时状态不重复通知", async () => {
    const onStatus = vi.fn();
    const api = vi
      .fn()
      .mockResolvedValue(
        status("A", { is_logged_in: true, status: "logged_in" }),
      );
    const { result, rerender } = renderHook(
      (props) =>
        useAccount({
          api,
          active: false,
          busy: false,
          context: fixed,
          onStatus,
          ...props,
        }),
      { initialProps: { summary: { id: "A", is_logged_in: false } } },
    );
    await act(async () => result.current.requireLogin());
    await act(async () => {
      rerender({ summary: { id: "A", is_logged_in: true } });
    });
    await act(async () => result.current.requireLogin());
    expect(api).toHaveBeenCalledTimes(2);
    expect(onStatus).toHaveBeenCalledTimes(1);
  });

  it("后台缓存变为未登录后，相同的实时登录结果仍通知同步缓存", async () => {
    const onStatus = vi.fn();
    const loggedIn = status("A", { is_logged_in: true, status: "logged_in" });
    const api = vi.fn().mockResolvedValue(loggedIn);
    const { result, rerender } = renderHook(
      (props) =>
        useAccount({
          api,
          active: false,
          busy: false,
          context: fixed,
          onStatus,
          ...props,
        }),
      { initialProps: { summary: { id: "A", is_logged_in: true } } },
    );
    await act(async () => result.current.requireLogin());
    expect(result.current.status).toEqual(loggedIn);
    expect(onStatus).not.toHaveBeenCalled();
    await act(async () => {
      rerender({ summary: { id: "A", is_logged_in: false } });
    });
    expect(result.current.loggedIn).toBe(false);
    await act(async () => result.current.requireLogin());
    expect(onStatus).toHaveBeenCalledTimes(1);
    expect(onStatus).toHaveBeenLastCalledWith(loggedIn, fixed);
    await act(async () => {
      rerender({ summary: { id: "A", is_logged_in: true } });
    });
    expect(result.current.loggedIn).toBe(true);
    await act(async () => result.current.requireLogin());
    expect(onStatus).toHaveBeenCalledTimes(1);
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
    let pending;
    act(() => {
      pending = result.current.checkStatus();
    });
    const signal = api.mock.calls[0][1].signal;
    await act(async () => {
      rerender({ context: context("A", 2, "new-token") });
    });
    expect(signal.aborted).toBe(true);
    expect(api).toHaveBeenCalledTimes(1);
    await act(async () => result.current.checkStatus());
    expect(api.mock.calls.at(-1)[1].token).toBe("new-token");
    await act(async () => {
      late.resolve(
        status("A", {
          is_logged_in: true,
          status: "logged_in",
          username: "旧身份",
        }),
      );
      await pending;
    });
    expect(result.current.loggedIn).toBe(false);
  });
});

const fixed = context("A");
