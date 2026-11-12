"use strict";

(() => {
  const $ = (id) => document.getElementById(id);
  const SETTINGS_KEY = "hongshu-workbench.settings.v1";
  const DRAFT_KEY = "hongshu-workbench.draft.v1";
  const fields = [
    "topic",
    "keywords",
    "instructions",
    "title",
    "content",
    "tags",
    "image-prompt",
    "visibility",
  ];
  const emptySettings = () => ({
    text: { base_url: "", api_key: "", model: "" },
    image: { base_url: "", api_key: "", model: "" },
    token: "",
  });
  let settings = emptySettings();
  let sources = [];
  let images = [];
  let busy = false;
  let researchController = null;
  let loggedIn = false;
  let username = "";
  let previewIndex = 0;
  let saveTimer;
  let loginPollTimer;
  let loginController = null;
  let loginRequest = 0;
  let loginPollEnabled = false;
  let loginOperation = "";
  let loginBrowserSession = false;
  let loginBrowserConnected = false;
  let loginBrowserMode = "";
  let loginDesktopURL = "";
  let lastLoginStatus = null;
  let uncertain = false;
  let lastPublished = "";
  let persistenceAvailable = true;
  const objectURLs = new Map();

  function notice(id, message, type = "") {
    const node = $(id);
    node.textContent = message;
    node.className = `notice ${type}`;
    node.hidden = !message;
  }

  function storageFailure() {
    persistenceAvailable = false;
    $("save-status").textContent = "仅本次使用 · 未保存到浏览器";
    notice(
      "storage-notice",
      "浏览器本地存储不可用或空间不足。你仍可继续本次创作，但刷新可能丢失配置和草稿，请先复制重要内容。",
      "warning",
    );
  }

  function loadLocal(key) {
    try {
      const value = localStorage.getItem(key);
      return value ? JSON.parse(value) : null;
    } catch (_) {
      storageFailure();
      return null;
    }
  }

  function storeLocal(key, value) {
    try {
      localStorage.setItem(key, JSON.stringify(value));
      return true;
    } catch (_) {
      storageFailure();
      return false;
    }
  }

  function safeConfig(value) {
    return {
      base_url: typeof value?.base_url === "string" ? value.base_url : "",
      api_key: typeof value?.api_key === "string" ? value.api_key : "",
      model: typeof value?.model === "string" ? value.model : "",
    };
  }

  function saveDraft() {
    clearTimeout(saveTimer);
    const draft = Object.fromEntries(fields.map((id) => [id, $(id).value]));
    draft.sources = sources;
    draft.images = images.map(({ id, name, path, url }) => ({
      id,
      name,
      path,
      url,
    }));
    draft.uncertain = uncertain;
    draft.lastPublished = lastPublished;
    if (storeLocal(DRAFT_KEY, draft)) {
      $("save-status").textContent =
        `已保存 · ${new Date().toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`;
      return true;
    }
    return false;
  }

  function updateLocks() {
    document.querySelectorAll("[data-lock]").forEach((node) => {
      node.disabled = busy;
    });
    document.querySelectorAll("[data-source-index]").forEach((node) => {
      node.disabled = busy || !sources[Number(node.dataset.sourceIndex)]?.token;
    });
    $("regenerate").disabled =
      busy || !sources.some((source) => source.selected);
    $("publish-confirm").disabled = busy || !$("publish-agree").checked;
    $("unlock-publish").disabled = busy || !$("reviewed-publish").checked;
    $("upload").disabled = busy || images.length >= 9;
    $("generate-image").disabled = busy || images.length >= 9;
    $("upload-zone").classList.toggle("busy", busy || images.length >= 9);
    $("cancel-research").hidden = !researchController;
    document.body.classList.toggle("working", busy);
    document.querySelectorAll("[data-close]").forEach((node) => {
      node.disabled = busy;
    });
    updateLoginControls();
  }

  function redact(message) {
    let result = String(message || "请求失败，请稍后检查连接或设置。");
    const secrets = [
      settings.token,
      settings.text.api_key,
      settings.image.api_key,
      $("service-token").value,
      $("text-key").value,
      $("image-key").value,
    ];
    for (const secret of secrets)
      if (secret) result = result.split(secret).join("[已隐藏凭证]");
    return result.slice(0, 400);
  }

  async function api(
    path,
    { method = "GET", body, signal, token = settings.token, blob = false } = {},
  ) {
    const headers = {};
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body && !(body instanceof FormData))
      headers["Content-Type"] = "application/json";
    let response;
    try {
      response = await fetch(`/api/v1${path}`, {
        method,
        headers,
        body:
          body instanceof FormData
            ? body
            : body
              ? JSON.stringify(body)
              : undefined,
        signal,
        credentials: "same-origin",
        cache: "no-store",
      });
    } catch (error) {
      if (error.name === "AbortError") throw error;
      throw new Error("无法连接服务，请检查网络以及工作台服务是否运行。");
    }
    if (blob && response.ok) return response.blob();
    let payload;
    try {
      payload = await response.json();
    } catch (_) {
      throw new Error(
        `服务返回了无法识别的响应（${response.status}），请检查服务或接口地址。`,
      );
    }
    if (!response.ok || payload.success !== true) {
      if (response.status === 401 || response.status === 403)
        throw new Error(
          "访问被拒绝，请在创作设置的高级选项中检查服务访问令牌。",
        );
      const message =
        payload.error || payload.message || `请求失败（${response.status}）`;
      const details =
        typeof payload.details === "string" ? payload.details : "";
      throw new Error(redact(details ? `${message}：${details}` : message));
    }
    return payload.data;
  }

  async function run(buttonId, label, action, statusId = "status") {
    if (busy) return;
    const button = buttonId ? $(buttonId) : null;
    const previous = button?.textContent;
    busy = true;
    if (button) button.textContent = label;
    updateLocks();
    notice(statusId, label, "busy");
    try {
      await action();
    } catch (error) {
      notice(
        statusId,
        error.name === "AbortError"
          ? "已取消研究，原有草稿已保留。"
          : redact(error.message),
        error.name === "AbortError" ? "warning" : "error",
      );
    } finally {
      busy = false;
      if (button) button.textContent = previous;
      updateLocks();
    }
  }

  function configFromForm(kind) {
    return {
      base_url: $(`${kind}-url`).value.trim(),
      api_key: $(`${kind}-key`).value.trim(),
      model: $(`${kind}-model`).value.trim(),
    };
  }

  function validateConfig(config, name, requireModel = true) {
    let url;
    try {
      url = new URL(config.base_url);
    } catch (_) {
      throw new Error(`请在创作设置中填写${name}的完整接口地址。`);
    }
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password
    )
      throw new Error(
        `${name}接口地址须为 HTTP 或 HTTPS 地址，且不能包含登录凭证。`,
      );
    if (
      url.search ||
      url.hash ||
      config.base_url.includes("?") ||
      config.base_url.includes("#")
    )
      throw new Error(
        `${name}接口地址不能包含查询参数或片段，请填写基础接口地址。`,
      );
    if (!config.api_key.trim())
      throw new Error(`请在创作设置中填写${name}的 API Key。`);
    if (requireModel && !config.model)
      throw new Error(`请在创作设置中填写${name}的模型名称。`);
  }

  function openSettings() {
    for (const kind of ["text", "image"]) {
      $(`${kind}-url`).value = settings[kind].base_url;
      $(`${kind}-key`).value = settings[kind].api_key;
      $(`${kind}-model`).value = settings[kind].model;
    }
    $("service-token").value = settings.token;
    notice("settings-status", "");
    $("settings-dialog").showModal();
  }

  function updateAccount(data) {
    loggedIn =
      data?.is_logged_in === true &&
      (!data.status || data.status === "logged_in");
    username = loggedIn ? data.username || "已登录账号" : "";
    $("account-label").textContent = loggedIn ? username : "连接小红书";
    $("preview-author").textContent = loggedIn ? username : "你的创作账号";
    $("account-dot").classList.toggle("connected", loggedIn);
    $("onboarding").hidden =
      loggedIn && Boolean(settings.text.base_url && settings.text.model);
  }

  async function requireLogin(signal) {
    const data = await api("/login/status", { signal });
    updateAccount(data);
    if (!loggedIn)
      throw new Error(
        data?.message ||
          "小红书尚未登录，请点击顶部「连接小红书」，打开登录浏览器完成登录和账号验证后继续。",
      );
  }

  function updateLoginControls() {
    const changingBrowser = ["open", "close"].includes(loginOperation);
    $("open-login-browser").disabled = busy || changingBrowser;
    $("open-login-browser").textContent =
      loginOperation === "open" ? "正在打开登录浏览器…" : "打开登录浏览器";
    $("close-login-browser").hidden = !loginBrowserSession;
    $("close-login-browser").disabled = busy || changingBrowser;
    $("close-login-browser").textContent =
      loginOperation === "close" ? "正在结束…" : "结束登录窗口";
    $("check-login").disabled =
      busy ||
      Boolean(loginController) ||
      !loginBrowserSession ||
      !loginPending(lastLoginStatus);
  }

  function cancelLoginRequest() {
    clearTimeout(loginPollTimer);
    loginPollTimer = null;
    loginRequest++;
    loginController?.abort();
    loginController = null;
    loginOperation = "";
    updateLoginControls();
  }

  function accountVisible() {
    return $("account-dialog").open && !document.hidden;
  }

  function loginPending(data) {
    return [
      "waiting_scan",
      "waiting_confirmation",
      "verification_required",
      "saving",
    ].includes(data?.status);
  }

  function detachLoginBrowser() {
    $("login-browser-frame").hidden = true;
    $("login-browser-frame").removeAttribute("src");
    $("login-browser-new-window").hidden = true;
    $("login-browser-new-window").removeAttribute("href");
  }

  function renderLoginBrowser() {
    const embedded =
      loginBrowserConnected &&
      loginBrowserMode === "desktop" &&
      Boolean(loginDesktopURL) &&
      accountVisible();
    if (embedded) {
      const frame = $("login-browser-frame");
      if (frame.getAttribute("src") !== loginDesktopURL)
        frame.src = loginDesktopURL;
      frame.hidden = false;
      $("login-browser-new-window").href = loginDesktopURL;
      $("login-browser-new-window").hidden = false;
    } else {
      detachLoginBrowser();
    }
    $("login-browser-placeholder").hidden = embedded;
    $("login-browser-placeholder").textContent =
      loginBrowserConnected && loginBrowserMode === "native"
        ? "请在本机打开的浏览器中完成登录和账号验证。"
        : loginBrowserSession
          ? "后台登录浏览器会保留，点击「打开登录浏览器」可继续查看。"
          : "点击「打开登录浏览器」，即可查看并操作完整登录页面。";
    $("login-browser-help").textContent =
      loginBrowserConnected && loginBrowserMode === "native"
        ? "请切换到运行本服务的电脑上弹出的 Chromium 浏览器，完成扫码、手机确认和账号安全验证。登录信息保存成功后会自动关闭浏览器。"
        : "验证期间收起窗口会断开画面，后台浏览器保留；登录信息保存成功后自动关闭。未完成的登录会话最长保留 20 分钟。";
  }

  function showLoginStatus(data) {
    lastLoginStatus = data;
    updateAccount(data);
    if (loggedIn) {
      loginPollEnabled = false;
      loginBrowserSession = false;
      loginBrowserConnected = false;
      loginBrowserMode = "";
      loginDesktopURL = "";
      renderLoginBrowser();
      notice("status", "小红书登录成功，登录信息已保存，登录窗口已自动关闭。");
      if ($("account-dialog").open) $("account-dialog").close();
    }
    const messages = {
      waiting_scan: "请在登录浏览器中使用小红书 App 扫码登录。",
      waiting_confirmation: "扫码成功，请在手机确认本次网页登录。",
      verification_required:
        "请按浏览器中的提示，使用已登录该账号的小红书 App 扫码验证身份。验证完成后会继续等待登录。",
      saving: "正在保存登录信息，请稍候。",
      expired: "登录浏览器会话已到期，请点击「打开登录浏览器」继续。",
      failed: "登录未完成，请点击「打开登录浏览器」重试。",
      not_logged_in: "尚未登录，请在登录浏览器中完成登录和账号验证。",
    };
    if (["expired", "failed"].includes(data?.status)) {
      loginBrowserSession = false;
      loginBrowserConnected = false;
      loginDesktopURL = "";
      renderLoginBrowser();
    }
    notice(
      "account-status",
      data?.message ||
        (loggedIn
          ? "登录成功，登录信息已保存，登录浏览器已自动关闭。"
          : messages[data?.status] || "请按登录浏览器中的提示完成登录。"),
      loggedIn ? "" : data?.status === "failed" ? "error" : "warning",
    );
    updateLoginControls();
  }

  function scheduleLoginPoll() {
    clearTimeout(loginPollTimer);
    loginPollTimer = null;
    if (!accountVisible() || !loginPollEnabled || loginController) return;
    loginPollTimer = setTimeout(() => {
      loginPollTimer = null;
      if (busy) {
        scheduleLoginPoll();
        return;
      }
      checkLoginStatus();
    }, 2000);
  }

  function resumeLoginProgress() {
    if (!accountVisible()) return;
    renderLoginBrowser();
    scheduleLoginPoll();
  }

  function pauseLoginProgress() {
    const changingBrowser = ["open", "close"].includes(loginOperation);
    cancelLoginRequest();
    detachLoginBrowser();
    if (changingBrowser)
      notice(
        "account-status",
        "连接请求已暂停，后台登录浏览器可能仍在运行。点击「打开登录浏览器」继续查看，或点击「结束登录窗口」关闭。",
        "warning",
      );
  }

  async function checkLoginStatus(manual = false) {
    if (busy || !accountVisible() || !loginBrowserSession) return;
    if (loginController || !loginPending(lastLoginStatus)) return;
    if (!manual && !loginPollEnabled) return;
    cancelLoginRequest();
    const request = loginRequest;
    const controller = new AbortController();
    loginController = controller;
    loginOperation = "status";
    updateLoginControls();
    if (manual) notice("account-status", "正在检查登录…", "busy");
    try {
      const data = await api("/login/status", { signal: controller.signal });
      if (request !== loginRequest || !accountVisible()) return;
      showLoginStatus(data);
      loginPollEnabled = loginBrowserSession && loginPending(data);
    } catch (error) {
      if (request !== loginRequest || error.name === "AbortError") return;
      loginPollEnabled = false;
      notice(
        "account-status",
        `${redact(error.message)} 可点击「检查登录状态」重试，或重新打开登录浏览器。`,
        "error",
      );
    } finally {
      if (request === loginRequest) {
        loginController = null;
        loginOperation = "";
        updateLoginControls();
        scheduleLoginPoll();
      }
    }
  }

  function loginDesktopAddress(value) {
    const url = new URL(value, location.origin);
    if (
      url.origin !== location.origin ||
      url.username ||
      url.password ||
      url.hash ||
      url.pathname !== "/desktop/vnc.html" ||
      [...url.searchParams.keys()].some(
        (key) => !["autoconnect", "resize", "path"].includes(key),
      ) ||
      url.searchParams.get("path") !== "desktop/websockify"
    )
      throw new Error("服务返回的登录浏览器地址无效，请检查服务配置。");
    return `${url.pathname}${url.search}`;
  }

  async function openLoginBrowser() {
    if (busy || !accountVisible() || ["open", "close"].includes(loginOperation))
      return;
    pauseLoginProgress();
    loginPollEnabled = false;
    loginBrowserConnected = false;
    // 请求取消时服务端仍可能已打开浏览器，保留手动结束入口。
    loginBrowserSession = true;
    const request = loginRequest;
    const controller = new AbortController();
    loginController = controller;
    loginOperation = "open";
    updateLoginControls();
    $("login-browser-placeholder").hidden = false;
    $("login-browser-placeholder").textContent = "正在打开登录浏览器，请稍候…";
    notice("account-status", "正在打开或连接已有的登录浏览器…", "busy");
    try {
      const data = await api("/login/browser", {
        method: "POST",
        signal: controller.signal,
      });
      if (request !== loginRequest || !accountVisible()) return;
      if (!["desktop", "native"].includes(data?.mode))
        throw new Error("服务未返回可用的登录浏览器模式，请检查服务配置。");
      loginDesktopURL =
        data.mode === "desktop" ? loginDesktopAddress(data.desktop_url) : "";
      loginBrowserMode = data.mode;
      loginBrowserConnected = true;
      showLoginStatus(data);
      renderLoginBrowser();
      loginPollEnabled = loginBrowserSession && loginPending(data);
    } catch (error) {
      if (request !== loginRequest || error.name === "AbortError") return;
      renderLoginBrowser();
      notice("account-status", redact(error.message), "error");
    } finally {
      if (request === loginRequest) {
        loginController = null;
        loginOperation = "";
        updateLoginControls();
        scheduleLoginPoll();
      }
    }
  }

  async function closeLoginBrowser() {
    if (busy || !accountVisible() || ["open", "close"].includes(loginOperation))
      return;
    pauseLoginProgress();
    loginPollEnabled = false;
    loginBrowserConnected = false;
    const request = loginRequest;
    const controller = new AbortController();
    loginController = controller;
    loginOperation = "close";
    updateLoginControls();
    renderLoginBrowser();
    notice("account-status", "正在结束登录窗口…", "busy");
    try {
      await api("/login/browser", {
        method: "DELETE",
        signal: controller.signal,
      });
      if (request !== loginRequest || !accountVisible()) return;
      loginBrowserSession = false;
      loginBrowserMode = "";
      loginDesktopURL = "";
      lastLoginStatus = null;
      renderLoginBrowser();
      notice("account-status", "登录窗口已结束，已保存的登录信息会保留。");
    } catch (error) {
      if (request !== loginRequest || error.name === "AbortError") return;
      notice(
        "account-status",
        `${redact(error.message)} 可再次点击「结束登录窗口」重试。`,
        "error",
      );
    } finally {
      if (request === loginRequest) {
        loginController = null;
        loginOperation = "";
        updateLoginControls();
      }
    }
  }

  function titleLength(text) {
    let width = 0;
    for (let index = 0; index < text.length; index++)
      width += text.charCodeAt(index) <= 127 ? 1 : 2;
    return Math.ceil(width / 2);
  }

  function tags() {
    return [
      ...new Set(
        $("tags")
          .value.split(/[\s,，#]+/u)
          .map((tag) => tag.trim())
          .filter(Boolean),
      ),
    ];
  }

  function updatePreview() {
    const title = $("title").value;
    const content = $("content").value;
    const titleCount = titleLength(title);
    const contentCount = Array.from(content).length;
    $("title-count").textContent = `${titleCount} / 20`;
    $("content-count").textContent = `${contentCount} / 1000`;
    $("title-count").classList.toggle("over", titleCount > 20);
    $("content-count").classList.toggle("over", contentCount > 1000);
    $("preview-title").textContent = title || "你的下一篇好笔记";
    $("preview-content").textContent =
      content || "从左侧的一个选题开始，或者直接写下你想分享的内容。";
    $("preview-tags").textContent = tags()
      .map((tag) => `#${tag}`)
      .join(" ");
    $("step-edit").classList.toggle("active", Boolean(title || content));
    $("step-publish").classList.toggle(
      "active",
      Boolean(title && content && images.length),
    );
  }

  function element(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function sourceURL(source) {
    const url = new URL(
      `https://www.xiaohongshu.com/explore/${encodeURIComponent(source.id)}`,
    );
    url.searchParams.set("xsec_token", source.token);
    url.searchParams.set("xsec_source", "pc_search");
    return url.href;
  }

  function renderSources() {
    const list = $("source-list");
    list.replaceChildren();
    $("source-count").textContent =
      `${sources.filter((source) => source.selected).length} / ${sources.length}`;
    if (!sources.length) {
      const empty = element("div", "empty-state");
      empty.append(
        element("span", "empty-symbol", "⌕"),
        element("p", "", "好内容，从了解开始"),
        element("small", "", "搜索后，你的参考笔记会出现在这里。"),
      );
      list.append(empty);
    }
    sources.forEach((source, index) => {
      const row = element(
        "div",
        `source-item${source.selected ? " selected" : ""}`,
      );
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.id = `source-${index}`;
      checkbox.checked = source.selected;
      checkbox.dataset.sourceIndex = String(index);
      checkbox.addEventListener("change", () => {
        if (
          checkbox.checked &&
          sources.filter((item) => item.selected).length >= 5
        ) {
          checkbox.checked = false;
          notice("status", "最多选择 5 篇参考笔记，请先取消一篇。", "warning");
          return;
        }
        source.selected = checkbox.checked;
        renderSources();
        saveDraft();
      });
      const info = element("div", "source-info");
      const label = element("label", "", source.title || "未命名笔记");
      label.htmlFor = checkbox.id;
      const meta = element("div", "source-meta");
      meta.append(
        element("span", "", source.author || "小红书创作者"),
        element(
          "span",
          "",
          source.used
            ? "已用于当前草稿"
            : source.content
              ? "正文已读取"
              : source.token
                ? "待读取正文"
                : "缺少访问凭证，无法选取",
        ),
      );
      if (source.token) {
        const link = element("a", "", "查看原笔记 ↗");
        link.href = sourceURL(source);
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        meta.append(link);
      }
      info.append(label, meta);
      if (source.content)
        info.append(element("p", "source-excerpt", source.content));
      if (source.error) info.append(element("p", "source-error", source.error));
      row.append(checkbox, info);
      list.append(row);
    });
    updateLocks();
  }

  function renderCover() {
    const cover = $("preview-cover");
    cover.replaceChildren();
    if (!images.length) {
      const placeholder = element("div", "preview-placeholder");
      const text = element("span");
      text.append(
        document.createTextNode("把生活的灵感"),
        document.createElement("br"),
        document.createTextNode("装进一篇笔记"),
      );
      placeholder.append(
        text,
        element("small", "", "上传或生成图片，即可预览封面"),
      );
      cover.append(placeholder);
      return;
    }
    previewIndex = Math.min(previewIndex, images.length - 1);
    const image = images[previewIndex];
    if (image.blobURL) {
      const img = element("img");
      img.src = image.blobURL;
      img.alt = `笔记配图 ${previewIndex + 1}`;
      cover.append(img);
    } else {
      cover.append(
        element("div", "image-error", image.error || "正在读取配图…"),
      );
    }
    if (images.length > 1) {
      const controls = element("div", "cover-controls");
      const previous = element("button", "", "‹");
      previous.setAttribute("aria-label", "预览上一张配图");
      previous.addEventListener("click", () => {
        previewIndex = (previewIndex + images.length - 1) % images.length;
        renderCover();
      });
      const next = element("button", "", "›");
      next.setAttribute("aria-label", "预览下一张配图");
      next.addEventListener("click", () => {
        previewIndex = (previewIndex + 1) % images.length;
        renderCover();
      });
      controls.append(
        previous,
        element("span", "", `${previewIndex + 1} / ${images.length}`),
        next,
      );
      cover.append(controls);
    }
  }

  function renderImages() {
    $("image-count").textContent = `${images.length} / 9 张`;
    $("image-list").replaceChildren();
    images.forEach((image, index) => {
      const tile = element("div", "image-tile");
      if (image.blobURL) {
        const img = element("img");
        img.src = image.blobURL;
        img.alt = image.name || `配图 ${index + 1}`;
        tile.append(img);
      } else
        tile.append(
          element("div", "image-error", image.error || "正在读取图片…"),
        );
      const remove = element("button", "image-remove", "×");
      remove.dataset.lock = "";
      remove.setAttribute("aria-label", `移除第 ${index + 1} 张配图`);
      remove.addEventListener("click", () => {
        if (busy) return;
        if (image.blobURL) URL.revokeObjectURL(image.blobURL);
        objectURLs.delete(image.id);
        images.splice(index, 1);
        renderImages();
        saveDraft();
      });
      tile.append(
        remove,
        element(
          "span",
          "image-badge",
          index === 0 ? "封面" : String(index + 1),
        ),
      );
      $("image-list").append(tile);
    });
    renderCover();
    updatePreview();
    updateLocks();
  }

  function validImage(image) {
    return (
      image &&
      typeof image.id === "string" &&
      /^[a-zA-Z0-9._-]+$/.test(image.id) &&
      typeof image.path === "string" &&
      image.path.length > 0
    );
  }

  async function loadImage(image) {
    try {
      const blob = await api(`/images/${encodeURIComponent(image.id)}`, {
        blob: true,
      });
      if (!blob.type.startsWith("image/")) throw new Error("图片格式无法预览");
      const url = URL.createObjectURL(blob);
      try {
        const probe = new Image();
        probe.src = url;
        await probe.decode();
      } catch (_) {
        URL.revokeObjectURL(url);
        throw new Error("图片无法解码");
      }
      if (image.blobURL) URL.revokeObjectURL(image.blobURL);
      image.blobURL = url;
      objectURLs.set(image.id, url);
      image.error = "";
    } catch (error) {
      image.error = `图片读取失败，请检查服务访问令牌，或移除后重新上传。${redact(error.message)}`;
    }
  }

  async function refreshImages() {
    await Promise.all(images.map(loadImage));
    renderImages();
    if (images.some((image) => image.error))
      notice(
        "status",
        "部分配图读取失败，请检查设置后重新保存设置，或移除并重新上传。失败配图修复前无法发布。",
        "warning",
      );
  }

  async function composeSelected(signal) {
    const selected = sources.filter((source) => source.selected);
    if (!selected.length || selected.length > 5)
      throw new Error("请选择 1–5 篇参考笔记。");
    const complete = [];
    let failures = 0;
    for (const source of selected) {
      signal.throwIfAborted();
      if (!source.content) {
        notice(
          "status",
          `正在读取参考正文（${complete.length + failures + 1} / ${selected.length}）…`,
          "busy",
        );
        try {
          const data = await api("/feeds/detail", {
            method: "POST",
            body: {
              feed_id: source.id,
              xsec_token: source.token,
              load_all_comments: false,
            },
            signal,
          });
          const note = data?.data?.note;
          if (typeof note?.desc !== "string" || !note.desc.trim())
            throw new Error("该笔记没有可用正文，已跳过。");
          source.content = note.desc;
          if (typeof note.title === "string" && note.title.trim())
            source.title = note.title;
          source.author =
            note.user?.nickname || note.user?.nickName || source.author;
          source.error = "";
        } catch (error) {
          if (error.name === "AbortError") throw error;
          source.error = redact(error.message);
          source.selected = false;
          failures++;
        }
      }
      if (source.content) complete.push(source);
      renderSources();
      saveDraft();
    }
    signal.throwIfAborted();
    if (!complete.length)
      throw new Error(
        "所选笔记均未成功读取正文，未调用写稿模型。请更换参考资料后重试。原有草稿已保留。",
      );
    notice(
      "status",
      `${failures ? `${failures} 篇读取失败，已在参考列表标明。` : ""}正在基于 ${complete.length} 篇真实资料生成草稿…`,
      "busy",
    );
    const draft = await api("/ai/compose", {
      method: "POST",
      signal,
      body: {
        config: settings.text,
        topic: $("topic").value.trim(),
        instructions: $("instructions").value.trim(),
        sources: complete.map((source) => ({
          id: source.id,
          title: source.title,
          content: source.content,
          author: source.author,
          url: sourceURL(source),
        })),
      },
    });
    signal.throwIfAborted();
    if (
      !draft ||
      typeof draft.title !== "string" ||
      typeof draft.content !== "string" ||
      !draft.title.trim() ||
      !draft.content.trim()
    )
      throw new Error("模型没有返回完整草稿，原有草稿已保留。");
    $("title").value = draft.title;
    $("content").value = draft.content;
    $("tags").value = Array.isArray(draft.tags)
      ? draft.tags
          .filter((tag) => typeof tag === "string")
          .map((tag) => `#${tag}`)
          .join(" ")
      : "";
    $("image-prompt").value =
      typeof draft.image_prompt === "string" ? draft.image_prompt : "";
    for (const source of sources) source.used = complete.includes(source);
    updatePreview();
    renderSources();
    saveDraft();
    const over =
      titleLength(draft.title) > 20 || Array.from(draft.content).length > 1000;
    notice(
      "status",
      `草稿已生成，参考了 ${complete.length} 篇笔记。${failures ? `另有 ${failures} 篇读取失败，已跳过。` : ""}${over ? "标题或正文超出字数限制，请在发布前编辑缩短。" : "请核对事实、修改表达并添加配图。"}`,
      over || failures ? "warning" : "",
    );
  }

  async function research(search) {
    if (busy) return;
    await run(
      search ? "research-button" : "regenerate",
      search ? "正在搜索与写稿…" : "正在重新生成…",
      async () => {
        validateConfig(settings.text, "文字模型");
        if (!$("topic").value.trim()) throw new Error("请先填写创作选题。");
        const keywords = [
          ...new Set(
            ($("keywords").value.trim() || $("topic").value.trim())
              .split(/[,，\n]+/u)
              .map((keyword) => keyword.trim())
              .filter(Boolean),
          ),
        ];
        if (search && (!keywords.length || keywords.length > 3))
          throw new Error("请填写 1–3 个关键词，用逗号或换行分隔。");
        researchController = new AbortController();
        const signal = researchController.signal;
        updateLocks();
        try {
          await requireLogin(signal);
          if (search) {
            const found = new Map();
            for (let index = 0; index < keywords.length; index++) {
              signal.throwIfAborted();
              notice(
                "status",
                `正在搜索「${keywords[index]}」（${index + 1} / ${keywords.length}）…`,
                "busy",
              );
              const data = await api("/feeds/search", {
                method: "POST",
                signal,
                body: { keyword: keywords[index] },
              });
              for (const feed of Array.isArray(data?.feeds) ? data.feeds : []) {
                if (typeof feed.id !== "string" || !feed.id) continue;
                const token =
                  typeof feed.xsecToken === "string" ? feed.xsecToken : "";
                if (found.has(feed.id) && (found.get(feed.id).token || !token))
                  continue;
                found.set(feed.id, {
                  id: feed.id,
                  token,
                  title: feed.noteCard?.displayTitle || "未命名笔记",
                  author:
                    feed.noteCard?.user?.nickname ||
                    feed.noteCard?.user?.nickName ||
                    "",
                  content: "",
                  selected: false,
                  used: false,
                  error: "",
                });
              }
            }
            signal.throwIfAborted();
            sources = [...found.values()];
            let selected = 0;
            for (const source of sources)
              if (source.token && selected < 3) {
                source.selected = true;
                selected++;
              }
            renderSources();
            saveDraft();
            if (!sources.length)
              throw new Error(
                "没有找到参考笔记，请换一个更具体的关键词。原有草稿已保留。",
              );
            if (!selected)
              throw new Error(
                "搜索结果缺少可读取正文的访问凭证，请换一组关键词。未调用写稿模型。",
              );
          }
          await composeSelected(signal);
        } finally {
          researchController = null;
        }
      },
    );
  }

  function publishBody() {
    return {
      title: $("title").value.trim(),
      content: $("content").value.trim(),
      images: images.map((image) => image.path),
      tags: tags(),
      visibility: $("visibility").value,
    };
  }

  function validatePublish() {
    const draft = publishBody();
    if (uncertain)
      throw new Error(
        "上次发布结果尚未确认。请先核对小红书账号并解除页面顶部的发布锁定。",
      );
    if (!draft.title || !draft.content)
      throw new Error("请填写笔记标题和正文。");
    if (titleLength($("title").value) > 20)
      throw new Error("标题超过 20 字，请缩短后发布（英文和数字按半字计）。");
    if (Array.from($("content").value).length > 1000)
      throw new Error("正文超过 1000 字，请缩短后发布。");
    if (images.length < 1 || images.length > 9)
      throw new Error("请添加 1–9 张图片。");
    if (images.some((image) => !image.blobURL || image.error))
      throw new Error("存在未成功读取的配图，请修复或移除后再发布。");
    if (JSON.stringify(draft) === lastPublished)
      throw new Error("这份内容已经发布成功，无需重复发布。");
    return draft;
  }

  function updateUncertain() {
    $("publish-uncertain").hidden = !uncertain;
    $("reviewed-publish").checked = false;
    updateLocks();
  }

  $("settings-open").addEventListener("click", openSettings);
  $("setup-start").addEventListener("click", openSettings);
  document.querySelectorAll("[data-close]").forEach((button) =>
    button.addEventListener("click", () => {
      if (!busy) $(button.dataset.close).close();
    }),
  );
  document.querySelectorAll("dialog").forEach((dialog) =>
    dialog.addEventListener("cancel", (event) => {
      if (busy) event.preventDefault();
    }),
  );
  $("settings-form").addEventListener("submit", (event) => {
    event.preventDefault();
    if (busy) return;
    try {
      const next = {
        text: configFromForm("text"),
        image: configFromForm("image"),
        token: $("service-token").value.trim(),
      };
      if (next.text.base_url || next.text.api_key || next.text.model)
        validateConfig(next.text, "文字模型");
      if (next.image.base_url || next.image.api_key || next.image.model)
        validateConfig(next.image, "生图模型");
      settings = next;
      const saved = storeLocal(SETTINGS_KEY, settings);
      $("settings-dialog").close();
      $("onboarding").hidden =
        loggedIn && Boolean(settings.text.base_url && settings.text.model);
      notice(
        "status",
        saved
          ? "设置已保存。接下来可连接小红书账号并开始创作。"
          : "设置已应用于本次使用，但浏览器未能保存。",
        saved ? "" : "warning",
      );
      if (images.length)
        run(null, "正在读取已保存的配图…", async () => {
          await refreshImages();
          if (images.every((image) => !image.error))
            notice("status", "设置已应用，所有配图已成功读取。");
        });
    } catch (error) {
      notice("settings-status", redact(error.message), "error");
    }
  });
  $("clear-settings").addEventListener("click", () => {
    if (busy) return;
    settings = emptySettings();
    let removed = true;
    try {
      localStorage.removeItem(SETTINGS_KEY);
    } catch (_) {
      removed = false;
      storageFailure();
    }
    for (const kind of ["text", "image"])
      for (const field of ["url", "key", "model"])
        $(`${kind}-${field}`).value = "";
    $("service-token").value = "";
    $("text-models").replaceChildren();
    $("image-models").replaceChildren();
    $("onboarding").hidden = false;
    notice(
      "settings-status",
      removed
        ? "已清除当前配置与浏览器保存的凭证；草稿已保留。"
        : "本次配置已清空，但无法删除浏览器中保存的凭证。请在浏览器设置中清除此站点的数据。",
      removed ? "" : "warning",
    );
  });
  for (const kind of ["text", "image"]) {
    $(`${kind}-models-button`).addEventListener("click", () =>
      run(
        `${kind}-models-button`,
        "正在读取…",
        async () => {
          const config = configFromForm(kind);
          validateConfig(
            config,
            kind === "text" ? "文字模型" : "生图模型",
            false,
          );
          const data = await api("/ai/models", {
            method: "POST",
            body: { config },
            token: $("service-token").value.trim(),
          });
          const models = Array.isArray(data?.models)
            ? data.models.filter((model) => typeof model === "string")
            : [];
          $(`${kind}-models`).replaceChildren(
            ...models.map((model) => {
              const option = element("option");
              option.value = model;
              return option;
            }),
          );
          notice(
            "settings-status",
            models.length
              ? `已读取 ${models.length} 个模型。点击模型名称输入框选择，也可以手动填写。`
              : "接口未返回模型列表，请手动填写模型名称。",
            models.length ? "" : "warning",
          );
        },
        "settings-status",
      ),
    );
  }
  $("account-open").addEventListener("click", () => {
    notice(
      "account-status",
      loggedIn
        ? `当前已连接：${username}。打开登录浏览器可查看账号页面和处理验证。`
        : "点击「打开登录浏览器」，在真实页面中完成登录和账号验证。",
    );
    $("account-dialog").showModal();
    renderLoginBrowser();
    updateLoginControls();
  });
  $("account-dialog").addEventListener("close", () => {
    loginBrowserConnected = false;
    loginPollEnabled = false;
    pauseLoginProgress();
  });
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) pauseLoginProgress();
    else resumeLoginProgress();
  });
  window.addEventListener("pagehide", pauseLoginProgress);
  window.addEventListener("pageshow", resumeLoginProgress);
  $("open-login-browser").addEventListener("click", openLoginBrowser);
  $("close-login-browser").addEventListener("click", closeLoginBrowser);
  $("check-login").addEventListener("click", () => checkLoginStatus(true));
  $("login-browser-frame").addEventListener("error", () => {
    if (!accountVisible() || !loginBrowserConnected) return;
    notice(
      "account-status",
      "登录画面加载失败，可点击「在新窗口查看」，或点击「打开登录浏览器」重新连接。",
      "error",
    );
  });
  $("research-form").addEventListener("submit", (event) => {
    event.preventDefault();
    research(true);
  });
  $("regenerate").addEventListener("click", () => research(false));
  $("cancel-research").addEventListener("click", () =>
    researchController?.abort(),
  );
  for (const id of fields)
    $(id).addEventListener("input", () => {
      updatePreview();
      $("publish-validation").hidden = true;
      $("save-status").textContent = "正在保存…";
      clearTimeout(saveTimer);
      saveTimer = setTimeout(saveDraft, 450);
    });
  $("save-draft").addEventListener("click", () =>
    notice(
      "status",
      saveDraft()
        ? "草稿已保存在此浏览器。下次打开可继续编辑。"
        : "草稿未能保存到浏览器，请复制重要内容后再离开。",
      persistenceAvailable ? "" : "warning",
    ),
  );
  $("upload").addEventListener("change", () => {
    const files = [...$("upload").files];
    $("upload").value = "";
    if (!files.length) return;
    run(null, "正在上传配图…", async () => {
      if (images.length + files.length > 9)
        throw new Error(
          `最多添加 9 张图片，当前还可添加 ${9 - images.length} 张。`,
        );
      for (const file of files) {
        if (file.size > 10 * 1024 * 1024)
          throw new Error(`「${file.name}」超过 10 MB，请压缩后上传。`);
        if (
          !["image/jpeg", "image/png", "image/webp", "image/gif"].includes(
            file.type,
          )
        )
          throw new Error(
            `「${file.name}」格式不支持，请选择 JPEG、PNG、WebP 或 GIF 图片。`,
          );
      }
      const form = new FormData();
      for (const file of files) form.append("files", file);
      const data = await api("/images/upload", { method: "POST", body: form });
      if (
        !Array.isArray(data?.images) ||
        data.images.length !== files.length ||
        !data.images.every(validImage)
      )
        throw new Error("上传返回的图片信息不完整，请检查服务后重新上传。");
      const added = data.images.map((image) => ({
        ...image,
        error: "",
        blobURL: "",
      }));
      images.push(...added);
      saveDraft();
      renderImages();
      await Promise.all(added.map(loadImage));
      renderImages();
      notice(
        "status",
        added.some((image) => image.error)
          ? "图片已上传，但部分预览读取失败。请检查设置，或移除后重新上传。"
          : `已添加 ${added.length} 张图片。第一张将作为封面。`,
        added.some((image) => image.error) ? "warning" : "",
      );
    });
  });
  $("generate-image").addEventListener("click", () =>
    run("generate-image", "正在生成配图…", async () => {
      validateConfig(settings.image, "生图模型");
      const prompt = $("image-prompt").value.trim();
      if (!prompt) throw new Error("请先填写画面描述。");
      if (images.length >= 9) throw new Error("配图已达 9 张，请先移除一张。");
      const data = await api("/ai/image", {
        method: "POST",
        body: { config: settings.image, prompt },
      });
      if (!validImage(data?.image))
        throw new Error("生图服务未返回有效图片，请检查模型设置。");
      const image = { ...data.image, error: "", blobURL: "" };
      images.push(image);
      saveDraft();
      renderImages();
      await loadImage(image);
      renderImages();
      notice(
        "status",
        image.error
          ? "配图已生成，但预览读取失败。请检查设置，或移除后重新生成。"
          : "配图已生成并加入笔记。请核对画面细节与文字。",
        image.error ? "warning" : "",
      );
    }),
  );
  $("publish-open").addEventListener("click", async () => {
    if (busy) return;
    try {
      validatePublish();
    } catch (error) {
      $("publish-validation").textContent = error.message;
      $("publish-validation").hidden = false;
      return;
    }
    await run("publish-open", "正在检查账号…", async () => {
      await requireLogin();
      const draft = validatePublish();
      $("confirm-account").textContent = username;
      $("confirm-title").textContent = draft.title;
      $("confirm-images").textContent = `${images.length} 张`;
      $("confirm-visibility").textContent = draft.visibility;
      $("publish-agree").checked = false;
      notice("publish-status", "");
      notice("status", "内容检查通过，请在确认窗口核对后决定是否发布。");
      $("publish-dialog").showModal();
    });
  });
  $("publish-agree").addEventListener("change", updateLocks);
  $("reviewed-publish").addEventListener("change", updateLocks);
  $("unlock-publish").addEventListener("click", () => {
    if (busy || !$("reviewed-publish").checked) return;
    uncertain = false;
    updateUncertain();
    saveDraft();
    notice(
      "status",
      "已解除发布锁定。如果刚才的笔记已发布成功，请勿再次发布相同内容。",
      "warning",
    );
  });
  $("publish-confirm").addEventListener("click", () =>
    run(
      "publish-confirm",
      "正在发布，请勿关闭…",
      async () => {
        if (!$("publish-agree").checked) throw new Error("请先勾选发布确认。");
        const draft = validatePublish();
        uncertain = true;
        updateUncertain();
        saveDraft();
        try {
          const data = await api("/publish", { method: "POST", body: draft });
          if (data?.status !== "发布完成")
            throw new Error("服务未返回明确的发布完成状态。");
          uncertain = false;
          lastPublished = JSON.stringify(draft);
          updateUncertain();
          saveDraft();
          $("publish-dialog").close();
          notice(
            "status",
            "笔记发布完成！草稿仍保存在此浏览器，你可以到小红书账号查看。不会再次自动发布。",
          );
        } catch (error) {
          saveDraft();
          $("publish-dialog").close();
          notice(
            "status",
            `发布未能得到完成确认：${redact(error.message)} 请求可能已生效，请先在小红书账号核对结果，避免重复发布。已锁定再次发布。`,
            "error",
          );
        }
      },
      "publish-status",
    ),
  );
  $("clear-draft").addEventListener("click", () =>
    $("clear-dialog").showModal(),
  );
  $("clear-draft-confirm").addEventListener("click", () => {
    if (busy) return;
    for (const id of fields)
      $(id).value = id === "visibility" ? "公开可见" : "";
    for (const url of objectURLs.values()) URL.revokeObjectURL(url);
    objectURLs.clear();
    sources = [];
    images = [];
    previewIndex = 0;
    // 发布不明标记单独保留，清空草稿不能绕过防重复发布保护。
    renderSources();
    renderImages();
    saveDraft();
    $("clear-dialog").close();
    notice("status", "草稿已清空，可以开始新的创作。");
  });
  window.addEventListener("pagehide", saveDraft);
  window.addEventListener("beforeunload", (event) => {
    if (busy) {
      event.preventDefault();
      event.returnValue = "";
    }
  });

  const savedSettings = loadLocal(SETTINGS_KEY);
  if (savedSettings && typeof savedSettings === "object")
    settings = {
      text: safeConfig(savedSettings.text),
      image: safeConfig(savedSettings.image),
      token: typeof savedSettings.token === "string" ? savedSettings.token : "",
    };
  const savedDraft = loadLocal(DRAFT_KEY);
  if (savedDraft && typeof savedDraft === "object") {
    for (const id of fields)
      if (typeof savedDraft[id] === "string") $(id).value = savedDraft[id];
    if (
      !["公开可见", "仅自己可见", "仅互关好友可见"].includes(
        $("visibility").value,
      )
    )
      $("visibility").value = "公开可见";
    sources = Array.isArray(savedDraft.sources)
      ? savedDraft.sources
          .filter((source) => source && typeof source.id === "string")
          .map((source) => ({
            id: source.id,
            token: typeof source.token === "string" ? source.token : "",
            title:
              typeof source.title === "string" ? source.title : "未命名笔记",
            author: typeof source.author === "string" ? source.author : "",
            content: typeof source.content === "string" ? source.content : "",
            selected: Boolean(source.selected),
            used: Boolean(source.used),
            error: typeof source.error === "string" ? source.error : "",
          }))
      : [];
    images = Array.isArray(savedDraft.images)
      ? savedDraft.images
          .filter(validImage)
          .slice(0, 9)
          .map((image) => ({
            id: image.id,
            name: typeof image.name === "string" ? image.name : "配图",
            path: image.path,
            url: image.url,
            blobURL: "",
            error: "",
          }))
      : [];
    uncertain = savedDraft.uncertain === true;
    lastPublished =
      typeof savedDraft.lastPublished === "string"
        ? savedDraft.lastPublished
        : "";
    if (persistenceAvailable) $("save-status").textContent = "已恢复本地草稿";
  }
  updateUncertain();
  renderSources();
  renderImages();
  if (images.length)
    run(null, "正在恢复草稿配图…", async () => {
      await refreshImages();
      if (images.every((image) => !image.error))
        notice("status", "已恢复上次保存的草稿与配图，可以继续创作。");
    });
})();
