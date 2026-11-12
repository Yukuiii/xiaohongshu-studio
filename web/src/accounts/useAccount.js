import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

const pendingStatuses = new Set([
  "waiting_scan",
  "waiting_confirmation",
  "verification_required",
  "saving",
]);

const messages = {
  waiting_scan: "请在登录网页中使用小红书 App 扫码登录。",
  waiting_confirmation: "扫码成功，请在手机上确认本次网页登录。",
  verification_required:
    "请在登录网页中完成账号安全验证，使用已登录该账号的小红书 App 扫码验证身份。",
  saving: "正在保存登录信息，请稍候。",
  expired: "登录浏览器会话已到期，请重新打开登录网页。",
  failed: "登录未完成，请结束登录窗口后重试。",
  not_logged_in: "尚未登录，请点击「添加账号」打开真实登录网页。",
};

function isLoggedIn(data) {
  return (
    data?.is_logged_in === true && (!data.status || data.status === "logged_in")
  );
}

function desktopAddress(value, sessionId) {
  const url = new URL(value, window.location.origin);
  if (
    url.origin !== window.location.origin ||
    url.username ||
    url.password ||
    url.hash ||
    !/^[a-zA-Z0-9_-]+$/.test(sessionId || "") ||
    url.pathname !== `/desktop/${sessionId}/vnc.html` ||
    url.searchParams.size !== 3 ||
    [...url.searchParams.keys()].some(
      (key) => !["autoconnect", "resize", "path"].includes(key),
    ) ||
    url.searchParams.get("autoconnect") !== "1" ||
    url.searchParams.get("resize") !== "scale" ||
    url.searchParams.get("path") !== `desktop/${sessionId}/websockify`
  ) {
    throw new Error("服务返回的登录浏览器地址无效，请检查服务配置。");
  }
  return `${url.pathname}${url.search}`;
}

const initialState = {
  status: null,
  sessionId: "",
  notice: { message: "", type: "" },
  loginVisible: false,
  browserSession: false,
  browserConnected: false,
  mode: "",
  desktopURL: "",
  operation: "",
  polling: false,
  completedRequest: 0,
};

export function useAccount({
  api,
  active,
  busy,
  context,
  activeLogin,
  onLoginSession,
}) {
  const [state, setState] = useState(initialState);
  const [pageVisible, setPageVisible] = useState(() => !document.hidden);
  const stateRef = useRef(state);
  const inputs = useRef({ api, active, busy, context, onLoginSession });
  inputs.current = { api, active, busy, context, onLoginSession };
  const mounted = useRef(true);
  const visible = useRef(!document.hidden);
  const request = useRef(null);
  const timer = useRef(null);
  const sequence = useRef(0);
  const appliedSequence = useRef(0);
  const requiredRequests = useRef(new Set());

  const update = useCallback((patch) => {
    if (!mounted.current) return;
    stateRef.current = { ...stateRef.current, ...patch };
    setState(stateRef.current);
  }, []);

  const clearTimer = useCallback(() => {
    window.clearTimeout(timer.current);
    timer.current = null;
  }, []);

  const cancelRequest = useCallback(() => {
    clearTimer();
    const current = request.current;
    request.current = null;
    current?.controller.abort();
    update({ operation: "" });
  }, [clearTimer, update]);

  const accountVisible = useCallback(
    () =>
      mounted.current &&
      inputs.current.context?.accountId &&
      inputs.current.active &&
      visible.current &&
      !document.hidden,
    [],
  );

  const pause = useCallback(() => {
    const changingBrowser = ["open", "close"].includes(
      stateRef.current.operation,
    );
    cancelRequest();
    if (changingBrowser) {
      update({
        browserConnected: false,
        notice: {
          message:
            "连接请求已暂停，后台登录浏览器可能仍在运行。重新打开登录网页可继续，也可结束登录窗口。",
          type: "warning",
        },
      });
    }
  }, [cancelRequest, update]);

  const applyStatus = useCallback(
    (data, version, scope) => {
      if (
        !mounted.current ||
        inputs.current.context !== scope ||
        version < appliedSequence.current
      )
        return;
      if (data?.account_id !== scope.accountId)
        throw new Error("登录响应与当前账号不一致，请刷新状态。");
      appliedSequence.current = version;
      const loggedIn = isLoggedIn(data);
      const pending = pendingStatuses.has(data?.status);
      const ended =
        loggedIn ||
        ["expired", "failed", "not_logged_in"].includes(data?.status);
      const patch = {
        status: data,
        sessionId: data.session_id || "",
        polling: pending,
        notice: {
          message: loggedIn
            ? "小红书登录成功，登录信息已保存，登录网页已自动收起。"
            : data?.message ||
              messages[data?.status] ||
              "请按登录网页中的提示完成登录。",
          type: loggedIn ? "" : data?.status === "failed" ? "error" : "warning",
        },
      };
      if (pending) patch.browserSession = true;
      if (ended) {
        patch.browserSession = false;
        patch.browserConnected = false;
        patch.mode = "";
        patch.desktopURL = "";
      }
      if (pending && data.session_id)
        inputs.current.onLoginSession?.(
          { account_id: scope.accountId, session_id: data.session_id },
          scope.accountId,
        );
      else if (ended) inputs.current.onLoginSession?.(null, scope.accountId);
      if (loggedIn) patch.loginVisible = false;
      update(patch);
    },
    [update],
  );

  const startRequest = useCallback(
    (operation) => {
      cancelRequest();
      const current = {
        controller: new AbortController(),
        version: ++sequence.current,
        scope: inputs.current.context,
      };
      request.current = current;
      update({ operation });
      return current;
    },
    [cancelRequest, update],
  );

  const currentRequest = useCallback(
    (current) =>
      request.current === current &&
      inputs.current.context === current.scope &&
      !current.controller.signal.aborted &&
      current.version >= appliedSequence.current &&
      accountVisible(),
    [accountVisible],
  );

  const finishRequest = useCallback(
    (current) => {
      if (request.current !== current) return;
      request.current = null;
      update({ operation: "", completedRequest: current.version });
    },
    [update],
  );

  const readStatus = useCallback(
    async (manual) => {
      if (inputs.current.busy || !accountVisible() || request.current) return;
      const previousNotice = stateRef.current.notice;
      const current = startRequest("status");
      if (manual)
        update({ notice: { message: "正在检查登录状态…", type: "busy" } });
      try {
        const data = await inputs.current.api("/login/status", {
          signal: current.controller.signal,
          accountId: current.scope.accountId,
          token: current.scope.token,
        });
        if (currentRequest(current))
          applyStatus(data, current.version, current.scope);
      } catch (error) {
        if (!currentRequest(current)) return;
        const canceled =
          error.name === "AbortError" ||
          error.message?.includes("context canceled");
        if (!manual || canceled) {
          if (manual) update({ notice: previousNotice });
          return;
        }
        update({
          polling: false,
          notice: {
            message:
              error.status === 401 || error.status === 403
                ? error.message
                : "暂时无法检查登录状态，请稍后重试。",
            type: "error",
          },
        });
      } finally {
        finishRequest(current);
      }
    },
    [
      accountVisible,
      applyStatus,
      currentRequest,
      finishRequest,
      startRequest,
      update,
    ],
  );

  const checkStatus = useCallback(() => readStatus(true), [readStatus]);

  const openLogin = useCallback(async () => {
    if (
      inputs.current.busy ||
      !accountVisible() ||
      ["open", "close"].includes(stateRef.current.operation)
    )
      return;
    const current = startRequest("open");
    // POST 被取消时服务端可能已打开浏览器，保留手动结束入口。
    update({
      loginVisible: true,
      browserSession: true,
      browserConnected: false,
      polling: false,
      notice: { message: "正在打开或连接已有的登录网页…", type: "busy" },
    });
    try {
      const data = await inputs.current.api("/login/browser", {
        method: "POST",
        signal: current.controller.signal,
        accountId: current.scope.accountId,
        token: current.scope.token,
      });
      if (!currentRequest(current) || !stateRef.current.loginVisible) return;
      if (!["desktop", "native"].includes(data?.mode)) {
        throw new Error("服务未返回可用的登录浏览器模式，请检查服务配置。");
      }
      const desktopURL =
        data.mode === "desktop"
          ? desktopAddress(data.desktop_url, data.session_id)
          : "";
      if (current.version < appliedSequence.current) return;
      if (!data.session_id || data.account_id !== current.scope.accountId)
        throw new Error("服务未返回匹配的账号登录会话，请刷新状态。");
      update({
        mode: data.mode,
        desktopURL,
        browserConnected: true,
        sessionId: data.session_id,
      });
      applyStatus(data, current.version, current.scope);
    } catch (error) {
      if (!currentRequest(current) || error.name === "AbortError") return;
      update({
        ...(error.code === "LOGIN_BROWSER_BUSY"
          ? { browserSession: false, loginVisible: false }
          : {}),
        notice: { message: error.message, type: "error" },
      });
    } finally {
      finishRequest(current);
    }
  }, [
    accountVisible,
    applyStatus,
    currentRequest,
    finishRequest,
    startRequest,
    update,
  ]);

  const hideLogin = useCallback(() => {
    if (inputs.current.busy) return;
    pause();
    update({ loginVisible: false });
  }, [pause, update]);

  const closeLogin = useCallback(async () => {
    if (
      inputs.current.busy ||
      !accountVisible() ||
      !stateRef.current.browserSession ||
      ["open", "close"].includes(stateRef.current.operation)
    )
      return;
    const current = startRequest("close");
    appliedSequence.current = current.version;
    update({
      polling: false,
      browserConnected: false,
      notice: { message: "正在结束登录窗口…", type: "busy" },
    });
    try {
      const sessionId = stateRef.current.sessionId;
      if (!sessionId)
        throw new Error("尚未取得登录会话，请刷新状态后再结束窗口。");
      await inputs.current.api(
        `/login/browser?session_id=${encodeURIComponent(sessionId)}`,
        {
          method: "DELETE",
          signal: current.controller.signal,
          accountId: current.scope.accountId,
          token: current.scope.token,
        },
      );
      if (!currentRequest(current)) return;
      inputs.current.onLoginSession?.(null, current.scope.accountId);
      update({
        loginVisible: false,
        browserSession: false,
        sessionId: "",
        mode: "",
        desktopURL: "",
        status: isLoggedIn(stateRef.current.status)
          ? stateRef.current.status
          : null,
        notice: {
          message: "登录窗口已结束，已保存的登录信息会保留。",
          type: "",
        },
      });
    } catch (error) {
      if (!currentRequest(current) || error.name === "AbortError") return;
      update({
        notice: {
          message: `${error.message} 可再次点击「结束登录窗口」重试。`,
          type: "error",
        },
      });
    } finally {
      finishRequest(current);
    }
  }, [accountVisible, currentRequest, finishRequest, startRequest, update]);

  const requireLogin = useCallback(
    async (signal, scope = inputs.current.context) => {
      if (!scope?.accountId || scope !== inputs.current.context)
        throw new DOMException("账号上下文已变更", "AbortError");
      const controller = new AbortController();
      const abort = () => controller.abort();
      signal?.addEventListener("abort", abort, { once: true });
      if (signal?.aborted || !mounted.current) controller.abort();
      requiredRequests.current.add(controller);
      const version = ++sequence.current;
      try {
        if (controller.signal.aborted)
          throw new DOMException("请求已取消", "AbortError");
        const data = await inputs.current.api("/login/status", {
          signal: controller.signal,
          accountId: scope.accountId,
          token: scope.token,
        });
        if (
          controller.signal.aborted ||
          !mounted.current ||
          inputs.current.context !== scope
        )
          throw new DOMException("请求已取消", "AbortError");
        applyStatus(data, version, scope);
        if (!isLoggedIn(data)) {
          throw new Error(
            `${data?.message || "小红书尚未登录。"} 请前往左侧「账号」页，添加账号并完成登录和账号验证后继续。`,
          );
        }
        return data;
      } finally {
        signal?.removeEventListener("abort", abort);
        requiredRequests.current.delete(controller);
      }
    },
    [applyStatus],
  );

  const onFrameError = useCallback(() => {
    if (
      !accountVisible() ||
      !stateRef.current.loginVisible ||
      !stateRef.current.browserConnected
    )
      return;
    update({
      notice: {
        message:
          "登录画面加载失败，可点击「在新窗口查看」，或重新打开登录网页连接。",
        type: "error",
      },
    });
  }, [accountVisible, update]);

  const invalidate = useCallback(() => {
    cancelRequest();
    requiredRequests.current.forEach((controller) => controller.abort());
    requiredRequests.current.clear();
    appliedSequence.current = ++sequence.current;
    update(initialState);
  }, [cancelRequest, update]);

  useLayoutEffect(() => {
    invalidate();
    if (activeLogin?.account_id === context?.accountId) {
      update({ browserSession: true, sessionId: activeLogin.session_id });
    }
  }, [context, invalidate]);

  useEffect(() => {
    mounted.current = true;
    const suspend = () => {
      visible.current = false;
      setPageVisible(false);
      pause();
    };
    const resume = () => {
      visible.current = !document.hidden;
      setPageVisible(visible.current);
    };
    const visibilityChanged = () => (document.hidden ? suspend() : resume());
    document.addEventListener("visibilitychange", visibilityChanged);
    window.addEventListener("pagehide", suspend);
    window.addEventListener("pageshow", resume);
    return () => {
      document.removeEventListener("visibilitychange", visibilityChanged);
      window.removeEventListener("pagehide", suspend);
      window.removeEventListener("pageshow", resume);
      mounted.current = false;
      cancelRequest();
      requiredRequests.current.forEach((controller) => controller.abort());
      requiredRequests.current.clear();
    };
  }, [cancelRequest, pause]);

  useEffect(() => {
    if (!context?.accountId || active) return;
    const controller = new AbortController();
    requireLogin(controller.signal, context).catch(() => {});
    return () => controller.abort();
  }, [context, requireLogin]);

  useEffect(() => {
    if (!active) pause();
    else if (
      context?.accountId &&
      !busy &&
      pageVisible &&
      (!stateRef.current.browserSession || !stateRef.current.status)
    )
      readStatus(false);
  }, [active, busy, pageVisible, context, pause, readStatus]);

  useEffect(() => {
    clearTimer();
    if (
      context?.accountId &&
      active &&
      pageVisible &&
      state.loginVisible &&
      state.browserSession &&
      state.polling &&
      !busy &&
      !state.operation
    ) {
      timer.current = window.setTimeout(() => readStatus(false), 2000);
    }
    return clearTimer;
  }, [
    active,
    busy,
    context,
    pageVisible,
    state.loginVisible,
    state.browserSession,
    state.polling,
    state.operation,
    state.completedRequest,
    readStatus,
    clearTimer,
  ]);

  return {
    active: Boolean(active && pageVisible),
    loggedIn: isLoggedIn(state.status),
    username: isLoggedIn(state.status) ? state.status.username || "" : "",
    status: state.status,
    notice: state.notice,
    loginVisible: state.loginVisible,
    browserSession: state.browserSession,
    browserConnected: state.browserConnected,
    mode: state.mode,
    desktopURL: state.desktopURL,
    sessionId: state.sessionId,
    operation: state.operation,
    openLogin,
    hideLogin,
    closeLogin,
    checkStatus,
    requireLogin,
    onFrameError,
    invalidate,
  };
}
