import { useCallback, useEffect, useRef, useState } from "react";
import { createAPI, redact } from "./api.js";
import {
  protectionKey,
  readProtection,
  writeProtection,
  withPublishLock,
} from "./publishProtection.js";

function protectionPatch(protection, value) {
  const changed =
    protection.revision !== value.publishRevision ||
    protection.uncertain !== value.uncertain ||
    protection.lastPublished !== value.lastPublished;
  return {
    uncertain: protection.uncertain,
    lastPublished: protection.lastPublished,
    publishRevision: protection.revision,
    ...(changed ? { reviewedPublish: false, reviewedRevision: null } : {}),
  };
}

const SETTINGS_KEY = "hongshu-workbench.settings.v1";
const TOKEN_KEY = "hongshu-workbench.token.v1";
const DRAFT_KEY = "hongshu-workbench.draft.v1";
const emptySettings = (token = "") => ({
  text: { base_url: "", api_key: "", model: "", has_api_key: false },
  image: { base_url: "", api_key: "", model: "", has_api_key: false },
  token,
});
const emptyDraft = () => ({
  topic: "",
  keywords: "",
  instructions: "",
  title: "",
  content: "",
  tags: "",
  "image-prompt": "",
  visibility: "公开可见",
});
const emptyNotice = () => ({ message: "", type: "" });

export function titleLength(text) {
  let width = 0;
  for (let index = 0; index < text.length; index++)
    width += text.charCodeAt(index) <= 127 ? 1 : 2;
  return Math.ceil(width / 2);
}

export function parseTags(value) {
  return [
    ...new Set(
      value
        .split(/[\s,，#]+/u)
        .map((tag) => tag.trim())
        .filter(Boolean),
    ),
  ];
}

export function sourceURL(source) {
  const url = new URL(
    `https://www.xiaohongshu.com/explore/${encodeURIComponent(source.id)}`,
  );
  url.searchParams.set("xsec_token", source.token);
  url.searchParams.set("xsec_source", "pc_search");
  return url.href;
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

function safeConfig(value) {
  return {
    ...Object.fromEntries(
      ["base_url", "api_key", "model"].map((key) => [
        key,
        typeof value?.[key] === "string" ? value[key] : "",
      ]),
    ),
    has_api_key: value?.has_api_key === true,
  };
}

function trimmedConfig(value) {
  const config = safeConfig(value);
  return {
    ...config,
    base_url: config.base_url.trim(),
    api_key: config.api_key.trim(),
    model: config.model.trim(),
  };
}

function serverSettings(data, token) {
  if (typeof data?.initialized !== "boolean")
    throw new Error("服务端模型配置响应无效，请检查服务版本。");
  return {
    text: safeConfig({ ...data.text, api_key: "" }),
    image: safeConfig({ ...data.image, api_key: "" }),
    token,
  };
}

function settingsBody(settings) {
  return Object.fromEntries(
    ["text", "image"].map((kind) => {
      const config = trimmedConfig(settings[kind]);
      return [
        kind,
        {
          base_url: config.base_url,
          model: config.model,
          ...(!config.has_api_key ||
          config.api_key ||
          (!config.base_url && !config.model)
            ? { api_key: config.api_key }
            : {}),
        },
      ];
    }),
  );
}

function initialState(saved = null) {
  let available = true;
  function load(key) {
    try {
      const value = localStorage.getItem(key);
      return value ? JSON.parse(value) : null;
    } catch {
      available = false;
      return null;
    }
  }
  const savedSettings = load(SETTINGS_KEY);
  let token =
    typeof savedSettings?.token === "string" ? savedSettings.token : "";
  try {
    token = localStorage.getItem(TOKEN_KEY) ?? token;
  } catch {
    available = false;
  }
  const settings = emptySettings(token);
  const settingsForm = {
    text: safeConfig(savedSettings?.text),
    image: safeConfig(savedSettings?.image),
    token,
  };
  const draft = emptyDraft();
  for (const key of Object.keys(draft))
    if (typeof saved?.[key] === "string") draft[key] = saved[key];
  if (!["公开可见", "仅自己可见", "仅互关好友可见"].includes(draft.visibility))
    draft.visibility = "公开可见";
  return {
    settings,
    settingsForm,
    settingsLoading: true,
    settingsReady: false,
    draft,
    sources: Array.isArray(saved?.sources)
      ? saved.sources
          .filter((item) => item && typeof item.id === "string")
          .map((item) => ({
            id: item.id,
            token: typeof item.token === "string" ? item.token : "",
            title: typeof item.title === "string" ? item.title : "未命名笔记",
            author: typeof item.author === "string" ? item.author : "",
            content: typeof item.content === "string" ? item.content : "",
            selected: Boolean(item.selected),
            used: Boolean(item.used),
            error: typeof item.error === "string" ? item.error : "",
          }))
      : [],
    images: Array.isArray(saved?.images)
      ? saved.images
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
      : [],
    uncertain: saved?.uncertain === true,
    lastPublished:
      typeof saved?.lastPublished === "string" ? saved.lastPublished : "",
    previewIndex: 0,
    busy: false,
    operation: "",
    researching: false,
    status: emptyNotice(),
    settingsStatus: emptyNotice(),
    publishStatus: emptyNotice(),
    persistenceAvailable: available,
    saveStatus: available
      ? saved
        ? "已恢复本地草稿"
        : "草稿保存在此浏览器"
      : "仅本次使用 · 未保存到浏览器",
    models: { text: [], image: [] },
    publishValidation: "",
    confirmation: null,
    publishAgreed: false,
    reviewedPublish: false,
    reviewedRevision: null,
    publishRevision: 0,
    clearDialog: false,
  };
}

function validateConfig(config, name, requireModel = true) {
  let url;
  try {
    url = new URL(config.base_url);
  } catch {
    throw new Error(`请在左侧「设置」中填写${name}的完整接口地址。`);
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
  if (!config.api_key.trim() && !config.has_api_key)
    throw new Error(`请在左侧「设置」中填写${name}的 API Key。`);
  if (requireModel && !config.model)
    throw new Error(`请在左侧「设置」中填写${name}的模型名称。`);
}

export function useWorkbench() {
  const [state, setState] = useState(initialState);
  const current = useRef(state);
  const settingsRequest = useRef(null);
  const loadedSettingsToken = useRef(null);
  const legacySettings = useRef(state.settingsForm);
  const settingsDirty = useRef({ text: false, image: false });
  const researchController = useRef(null);
  const objectURLs = useRef(new Map());
  const firstSave = useRef(true);
  const saveTimer = useRef(null);
  const context = useRef({
    accountId: null,
    generation: 0,
    token: state.settings.token,
  });
  const controllers = useRef(new Set());
  const update = useCallback((patch) => {
    const changes =
      typeof patch === "function" ? patch(current.current) : patch;
    current.current = { ...current.current, ...changes };
    setState(current.current);
  }, []);
  const [api] = useState(() =>
    createAPI(
      () => current.current.settings,
      () => current.current.settingsForm,
    ),
  );
  const fetchModelSettings = useCallback(
    async (token, signal, migrate = true) => {
      let data = await api("/settings", { token, signal });
      signal.throwIfAborted();
      serverSettings(data, token);
      const legacy = legacySettings.current;
      const hasLegacy =
        legacy &&
        ["text", "image"].some((kind) =>
          ["base_url", "api_key", "model"].some((key) => legacy[kind][key]),
        );
      if (migrate && !data.initialized && hasLegacy) {
        try {
          data = await api("/settings", {
            method: "PUT",
            token,
            signal,
            body: {
              ...settingsBody(legacy),
              only_if_uninitialized: true,
            },
          });
        } catch (error) {
          if (error.code !== "SETTINGS_ALREADY_INITIALIZED") throw error;
          data = await api("/settings", { token, signal });
        }
        signal.throwIfAborted();
      }
      return data;
    },
    [api],
  );
  const errorMessage = (error) =>
    redact(
      error.message,
      current.current.settings,
      current.current.settingsForm,
    );
  const notice = (message, type = "", target = "status") =>
    update({ [target]: { message, type } });

  const storageFailure = useCallback(
    () =>
      update({
        persistenceAvailable: false,
        saveStatus: "仅本次使用 · 未保存到浏览器",
      }),
    [update],
  );
  const saveDraft = useCallback(
    (scope = context.current) => {
      // 保存目标随操作固定，旧回调不能写入新账号。
      if (scope !== context.current) return false;
      if (!scope.accountId) return true;
      clearTimeout(saveTimer.current);
      const value = current.current;
      try {
        // 普通草稿只镜像最新保护，不能写入独立发布记录。
        const protection = readProtection(scope.accountId, value);
        localStorage.setItem(
          `${DRAFT_KEY}:${scope.accountId}`,
          JSON.stringify({
            ...value.draft,
            sources: value.sources,
            images: value.images.map(({ id, name, path, url }) => ({
              id,
              name,
              path,
              url,
            })),
            uncertain: protection.uncertain,
            lastPublished: protection.lastPublished,
          }),
        );
        update({
          ...protectionPatch(protection, value),
          saveStatus: `已保存 · ${new Date().toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`,
        });
        return true;
      } catch {
        storageFailure();
        return false;
      }
    },
    [storageFailure, update],
  );

  const syncProtection = useCallback(
    (scope = context.current) => {
      if (scope !== context.current || !scope.accountId)
        throw new DOMException("账号上下文已变更", "AbortError");
      const protection = readProtection(scope.accountId, current.current);
      update(protectionPatch(protection, current.current));
      return protection;
    },
    [update],
  );

  const activateAccount = useCallback(
    (accountId, legacyAccountId, { force = false, discard = false } = {}) => {
      if (!force && (current.current.busy || (!discard && !saveDraft())))
        return false;
      clearTimeout(saveTimer.current);
      researchController.current?.abort();
      controllers.current.forEach((controller) => controller.abort());
      controllers.current.clear();
      for (const url of objectURLs.current.values()) URL.revokeObjectURL(url);
      objectURLs.current.clear();
      const scope = {
        accountId,
        generation: context.current.generation + 1,
        token: current.current.settings.token,
      };
      context.current = scope;
      let saved = null;
      let protection = null;
      let available = true;
      if (accountId) {
        try {
          const key = `${DRAFT_KEY}:${accountId}`;
          const marker = `${DRAFT_KEY}:migrated:${accountId}`;
          const raw = localStorage.getItem(key);
          saved = raw ? JSON.parse(raw) : null;
          if (accountId === legacyAccountId && !localStorage.getItem(marker)) {
            const legacy = localStorage.getItem(DRAFT_KEY);
            if (!raw && legacy) {
              const migrated = JSON.parse(legacy);
              localStorage.setItem(key, legacy);
              saved = migrated;
            }
            localStorage.setItem(marker, "1");
          }
          protection = readProtection(accountId, saved);
        } catch {
          available = false;
        }
      }
      const restored = initialState(saved);
      firstSave.current = true;
      update({
        ...restored,
        ...(protection ? protectionPatch(protection, restored) : {}),
        settings: current.current.settings,
        settingsForm: current.current.settingsForm,
        settingsLoading: current.current.settingsLoading,
        settingsReady: current.current.settingsReady,
        settingsStatus: current.current.settingsStatus,
        models: current.current.models,
        accountId,
        context: scope,
        persistenceAvailable: available && current.current.persistenceAvailable,
        saveStatus: accountId ? restored.saveStatus : "请先选择创作账号",
      });
      return true;
    },
    [saveDraft, update],
  );

  const applyModelSettings = useCallback(
    (data, token, preserveEdits = false) => {
      const settings = serverSettings(data, token);
      let settingsForm = structuredClone(settings);
      if (preserveEdits) {
        settingsForm.token = current.current.settingsForm.token;
        for (const kind of ["text", "image"])
          if (settingsDirty.current[kind])
            settingsForm[kind] = {
              ...current.current.settingsForm[kind],
              has_api_key: settings[kind].has_api_key,
            };
      }
      loadedSettingsToken.current = token;
      legacySettings.current = null;
      update({
        settings,
        settingsForm,
        settingsReady: true,
        settingsLoading: false,
      });
      if (!preserveEdits) settingsDirty.current = { text: false, image: false };
      try {
        localStorage.removeItem(SETTINGS_KEY);
        localStorage.setItem(TOKEN_KEY, token);
        return true;
      } catch {
        storageFailure();
        return false;
      }
    },
    [storageFailure, update],
  );

  useEffect(() => {
    const token = state.settings.token;
    if (loadedSettingsToken.current === token && current.current.settingsReady)
      return;
    const controller = new AbortController();
    settingsRequest.current?.abort();
    settingsRequest.current = controller;
    update({ settingsLoading: true, settingsReady: false });
    (async () => {
      try {
        const data = await fetchModelSettings(token, controller.signal);
        if (settingsRequest.current !== controller || controller.signal.aborted)
          return;
        const localSaved = applyModelSettings(data, token, true);
        update({
          settingsStatus: localSaved
            ? emptyNotice()
            : {
                message: "模型配置已从服务端读取，但浏览器本地凭证未能更新。",
                type: "warning",
              },
        });
      } catch (error) {
        if (settingsRequest.current !== controller || controller.signal.aborted)
          return;
        update({
          settingsLoading: false,
          settingsReady: false,
          settingsStatus: {
            message: redact(
              error.message,
              current.current.settings,
              current.current.settingsForm,
            ),
            type: "error",
          },
        });
      } finally {
        if (settingsRequest.current === controller)
          settingsRequest.current = null;
      }
    })();
    return () => {
      controller.abort();
      if (settingsRequest.current === controller)
        settingsRequest.current = null;
    };
  }, [
    api,
    state.settings.token,
    update,
    fetchModelSettings,
    applyModelSettings,
  ]);

  const isCurrent = (scope) => scope === context.current;
  function ensureCurrent(scope) {
    if (!isCurrent(scope))
      throw new DOMException("账号上下文已变更", "AbortError");
  }
  async function accountAPI(scope, path, options = {}) {
    ensureCurrent(scope);
    const data = await api(path, {
      ...options,
      accountId: scope.accountId,
      token: scope.token,
    });
    ensureCurrent(scope);
    options.signal?.throwIfAborted();
    return data;
  }

  useEffect(() => {
    if (firstSave.current) {
      firstSave.current = false;
      return;
    }
    if (!context.current.accountId) return;
    update({ saveStatus: "正在保存…" });
    const scope = context.current;
    saveTimer.current = setTimeout(() => saveDraft(scope), 450);
    return () => clearTimeout(saveTimer.current);
  }, [state.draft, state.sources, state.images, saveDraft, update]);

  useEffect(() => {
    const onStorage = (event) => {
      const id = context.current.accountId;
      if (
        !id ||
        (event.key !== null &&
          event.key !== protectionKey(id) &&
          event.key !== `${DRAFT_KEY}:${id}`)
      )
        return;
      try {
        syncProtection();
      } catch {
        storageFailure();
        update({
          uncertain: true,
          reviewedPublish: false,
          reviewedRevision: null,
        });
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [syncProtection, storageFailure, update]);

  useEffect(() => {
    function beforeUnload(event) {
      if (current.current.busy) {
        event.preventDefault();
        event.returnValue = "";
      }
    }
    const flush = () => saveDraft();
    window.addEventListener("pagehide", flush);
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      window.removeEventListener("pagehide", flush);
      clearTimeout(saveTimer.current);
      controllers.current.forEach((controller) => controller.abort());
      settingsRequest.current?.abort();
      settingsRequest.current = null;
      window.removeEventListener("beforeunload", beforeUnload);
      researchController.current?.abort();
      for (const url of objectURLs.current.values()) URL.revokeObjectURL(url);
    };
  }, [saveDraft]);

  async function run(
    operation,
    label,
    action,
    target = "status",
    shared = false,
  ) {
    if (current.current.busy) return;
    const scope = context.current;
    if (!shared && !scope.accountId) {
      notice("请先到「账号」页选择创作账号。", "warning", target);
      return;
    }
    const controller = new AbortController();
    controllers.current.add(controller);
    update({
      busy: true,
      operation,
      [target]: { message: label, type: "busy" },
    });
    try {
      await action(scope, controller.signal);
    } catch (error) {
      if (!isCurrent(scope)) return;
      notice(
        error.name === "AbortError"
          ? "操作已取消，原有草稿已保留。"
          : errorMessage(error),
        error.name === "AbortError" ? "warning" : "error",
        target,
      );
    } finally {
      controllers.current.delete(controller);
      if (isCurrent(scope)) update({ busy: false, operation: "" });
    }
  }

  function changeDraft(key, value) {
    if (current.current.busy || !context.current.accountId) return;
    update(({ draft }) => ({
      draft: { ...draft, [key]: value },
      publishValidation: "",
    }));
  }

  async function loadImages(items, scope, signal, target = "status") {
    const loaded = await Promise.all(
      items.map(async (image) => {
        let blobURL = "";
        try {
          const blob = await accountAPI(
            scope,
            `/images/${encodeURIComponent(image.id)}`,
            {
              blob: true,
              signal,
            },
          );
          if (!blob.type.startsWith("image/"))
            throw new Error("图片格式无法预览");
          blobURL = URL.createObjectURL(blob);
          const probe = new Image();
          probe.src = blobURL;
          try {
            await probe.decode();
          } catch {
            throw new Error("图片无法解码");
          }
          ensureCurrent(scope);
          signal?.throwIfAborted();
          const previous = objectURLs.current.get(image.id);
          if (previous) URL.revokeObjectURL(previous);
          objectURLs.current.set(image.id, blobURL);
          return { ...image, blobURL, error: "" };
        } catch (error) {
          if (blobURL) URL.revokeObjectURL(blobURL);
          ensureCurrent(scope);
          if (error.name === "AbortError") throw error;
          return {
            ...image,
            error: `图片读取失败，请检查服务访问令牌，或移除后重新上传。${errorMessage(error)}`,
          };
        }
      }),
    );
    ensureCurrent(scope);
    const byID = new Map(loaded.map((image) => [image.id, image]));
    update(({ images }) => ({
      images: images.map((image) => byID.get(image.id) || image),
    }));
    if (loaded.some((image) => image.error))
      notice(
        "部分配图读取失败，请检查设置后重新保存设置，或移除并重新上传。失败配图修复前无法发布。",
        "warning",
        target,
      );
    return loaded;
  }

  useEffect(() => {
    if (current.current.images.length)
      run("restore", "正在恢复草稿配图…", async (scope, signal) => {
        const loaded = await loadImages(current.current.images, scope, signal);
        ensureCurrent(scope);
        if (loaded.every((image) => !image.error))
          notice("已恢复上次保存的草稿与配图，可以继续创作。");
      });
  }, [state.context]);

  function selectSource(id, selected) {
    if (current.current.busy) return;
    if (
      selected &&
      current.current.sources.filter((source) => source.selected).length >= 5
    ) {
      notice("最多选择 5 篇参考笔记，请先取消一篇。", "warning");
      return;
    }
    update(({ sources }) => ({
      sources: sources.map((source) =>
        source.id === id ? { ...source, selected } : source,
      ),
    }));
  }

  async function composeSelected(signal, scope) {
    const sources = current.current.sources.map((source) => ({ ...source }));
    const selected = sources.filter((source) => source.selected);
    if (!selected.length || selected.length > 5)
      throw new Error("请选择 1–5 篇参考笔记。");
    let failures = 0;
    const missing = selected.filter((source) => !source.content);
    if (missing.length) {
      notice(`正在读取 ${missing.length} 篇参考正文…`, "busy");
      const data = await accountAPI(scope, "/feeds/bodies", {
        method: "POST",
        signal,
        body: {
          items: missing.map((source) => ({
            feed_id: source.id,
            xsec_token: source.token,
          })),
        },
      });
      ensureCurrent(scope);
      signal.throwIfAborted();
      if (!Array.isArray(data?.items))
        throw new Error("服务未返回参考正文，请重试。原有草稿已保留。");
      const fetched = new Map(
        data.items
          .filter((item) => item && typeof item.feed_id === "string")
          .map((item) => [item.feed_id, item]),
      );
      for (const source of missing) {
        const item = fetched.get(source.id);
        if (typeof item?.content === "string" && item.content.trim()) {
          source.content = item.content;
          if (typeof item.title === "string" && item.title.trim())
            source.title = item.title;
          if (typeof item.author === "string" && item.author.trim())
            source.author = item.author;
          source.error = "";
        } else {
          source.error = item?.error || "未能读取正文，请更换参考资料。";
          source.selected = false;
          failures++;
        }
      }
      update({ sources: sources.map((item) => ({ ...item })) });
      saveDraft(scope);
    }
    signal.throwIfAborted();
    const complete = selected.filter((source) => source.content);
    if (!complete.length)
      throw new Error(
        "所选笔记均未成功读取正文，未调用写稿模型。请更换参考资料后重试。原有草稿已保留。",
      );
    notice(
      `${failures ? `${failures} 篇读取失败，已在参考列表标明。` : ""}正在基于 ${complete.length} 篇真实资料生成草稿…`,
      "busy",
    );
    const form = current.current.draft;
    const generated = await accountAPI(scope, "/ai/compose", {
      method: "POST",
      signal,
      body: {
        config: current.current.settings.text,
        topic: form.topic.trim(),
        instructions: form.instructions.trim(),
        sources: complete.map((source) => ({
          id: source.id,
          title: source.title,
          content: source.content,
          author: source.author,
          url: sourceURL(source),
        })),
      },
    });
    ensureCurrent(scope);
    signal.throwIfAborted();
    if (
      !generated ||
      typeof generated.title !== "string" ||
      typeof generated.content !== "string" ||
      !generated.title.trim() ||
      !generated.content.trim()
    )
      throw new Error("模型没有返回完整草稿，原有草稿已保留。");
    update(({ draft }) => ({
      draft: {
        ...draft,
        title: generated.title,
        content: generated.content,
        tags: Array.isArray(generated.tags)
          ? generated.tags
              .filter((tag) => typeof tag === "string")
              .map((tag) => `#${tag}`)
              .join(" ")
          : "",
        "image-prompt":
          typeof generated.image_prompt === "string"
            ? generated.image_prompt
            : "",
      },
      sources: sources.map((source) => ({
        ...source,
        used: complete.includes(source),
      })),
      publishValidation: "",
    }));
    saveDraft(scope);
    const over =
      titleLength(generated.title) > 20 ||
      Array.from(generated.content).length > 1000;
    notice(
      `草稿已生成，参考了 ${complete.length} 篇笔记。${failures ? `另有 ${failures} 篇读取失败，已跳过。` : ""}${over ? "标题或正文超出字数限制，请在发布前编辑缩短。" : "请核对事实、修改表达并添加配图。"}`,
      over || failures ? "warning" : "",
    );
  }

  async function research(search, requireLogin) {
    await run(
      search ? "research" : "regenerate",
      search ? "正在搜索参考笔记…" : "正在生成文案…",
      async (scope) => {
        const { settings, draft } = current.current;
        if (!search) validateConfig(settings.text, "文字模型");
        if (!draft.topic.trim()) throw new Error("请先填写创作选题。");
        const keywords = [
          ...new Set(
            (draft.keywords.trim() || draft.topic.trim())
              .split(/[,，\n]+/u)
              .map((keyword) => keyword.trim())
              .filter(Boolean),
          ),
        ];
        if (search && (!keywords.length || keywords.length > 3))
          throw new Error("请填写 1–3 个关键词，用逗号或换行分隔。");
        const controller = new AbortController();
        researchController.current = controller;
        update({ researching: true });
        const signal = controller.signal;
        try {
          await requireLogin(signal, scope);
          ensureCurrent(scope);
          signal.throwIfAborted();
          if (search) {
            const found = new Map();
            for (let index = 0; index < keywords.length; index++) {
              signal.throwIfAborted();
              notice(
                `正在搜索「${keywords[index]}」（${index + 1} / ${keywords.length}）…`,
                "busy",
              );
              const data = await accountAPI(scope, "/feeds/search", {
                method: "POST",
                signal,
                body: { keyword: keywords[index] },
              });
              ensureCurrent(scope);
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
            const sources = [...found.values()];
            let selected = 0;
            for (const source of sources)
              if (source.token && selected < 3) {
                source.selected = true;
                selected++;
              }
            update({ sources });
            saveDraft(scope);
            if (!sources.length)
              throw new Error(
                "没有找到参考笔记，请换一个更具体的关键词。原有草稿已保留。",
              );
            if (!selected)
              throw new Error(
                "搜索结果缺少可读取正文的访问凭证，请换一组关键词。未调用写稿模型。",
              );
            notice(
              `已找到 ${sources.length} 篇参考，选中 ${selected} 篇。可以调整选择，再进入「撰写文案」。`,
            );
          } else {
            await composeSelected(signal, scope);
          }
        } finally {
          if (isCurrent(scope)) {
            researchController.current = null;
            update({ researching: false });
          }
        }
      },
    );
  }

  async function upload(files) {
    if (!files.length) return;
    await run("upload", "正在上传配图…", async (scope, signal) => {
      if (current.current.images.length + files.length > 9)
        throw new Error(
          `最多添加 9 张图片，当前还可添加 ${9 - current.current.images.length} 张。`,
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
      const data = await accountAPI(scope, "/images/upload", {
        method: "POST",
        body: form,
        signal,
      });
      ensureCurrent(scope);
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
      update(({ images }) => ({ images: [...images, ...added] }));
      saveDraft(scope);
      const loaded = await loadImages(added, scope, signal);
      ensureCurrent(scope);
      notice(
        loaded.some((image) => image.error)
          ? "图片已上传，但部分预览读取失败。请检查设置，或移除后重新上传。"
          : `已添加 ${added.length} 张图片。第一张将作为封面。`,
        loaded.some((image) => image.error) ? "warning" : "",
      );
    });
  }

  async function generateImage() {
    await run("generate-image", "正在生成配图…", async (scope, signal) => {
      validateConfig(current.current.settings.image, "生图模型");
      const prompt = current.current.draft["image-prompt"].trim();
      if (!prompt) throw new Error("请先填写画面描述。");
      if (current.current.images.length >= 9)
        throw new Error("配图已达 9 张，请先移除一张。");
      const data = await accountAPI(scope, "/ai/image", {
        method: "POST",
        signal,
        body: { config: current.current.settings.image, prompt },
      });
      ensureCurrent(scope);
      if (!validImage(data?.image))
        throw new Error("生图服务未返回有效图片，请检查模型设置。");
      const image = { ...data.image, error: "", blobURL: "" };
      update(({ images }) => ({ images: [...images, image] }));
      saveDraft(scope);
      const [loaded] = await loadImages([image], scope, signal);
      ensureCurrent(scope);
      notice(
        loaded.error
          ? "配图已生成，但预览读取失败。请检查设置，或移除后重新生成。"
          : "配图已生成并加入笔记。请核对画面细节与文字。",
        loaded.error ? "warning" : "",
      );
    });
  }

  function removeImage(id) {
    if (current.current.busy) return;
    const url = objectURLs.current.get(id);
    if (url) URL.revokeObjectURL(url);
    objectURLs.current.delete(id);
    update(({ images, previewIndex }) => ({
      images: images.filter((image) => image.id !== id),
      previewIndex: Math.max(0, Math.min(previewIndex, images.length - 2)),
      publishValidation: "",
    }));
  }

  function changeSetting(kind, key, value) {
    if (current.current.busy) return;
    if (kind !== "token") settingsDirty.current[kind] = true;
    update(({ settingsForm }) => ({
      settingsForm:
        kind === "token"
          ? { ...settingsForm, token: value }
          : {
              ...settingsForm,
              [kind]: {
                ...settingsForm[kind],
                [key]: value,
                ...(key === "base_url" && value !== settingsForm[kind].base_url
                  ? { api_key: "" }
                  : {}),
              },
            },
    }));
  }

  async function saveSettings() {
    if (current.current.busy) return false;
    const controller = new AbortController();
    settingsRequest.current?.abort();
    settingsRequest.current = controller;
    const dirtyKinds = { ...settingsDirty.current };
    const dirty = dirtyKinds.text || dirtyKinds.image;
    const form = current.current.settingsForm;
    let next = {
      text: trimmedConfig(form.text),
      image: trimmedConfig(form.image),
      token: form.token.trim(),
    };
    const tokenChanged = next.token !== current.current.settings.token;
    update({ busy: true, operation: "save-settings", settingsLoading: true });
    let saved = false;
    try {
      if (tokenChanged && !saveDraft())
        throw new Error("草稿保存失败，服务令牌未切换，请复制重要内容后重试。");
      let data = await fetchModelSettings(
        next.token,
        controller.signal,
        !dirty,
      );
      const stored = serverSettings(data, next.token);
      for (const kind of ["text", "image"]) {
        if (data.initialized && !dirtyKinds[kind]) next[kind] = stored[kind];
        else next[kind].has_api_key = stored[kind].has_api_key;
      }
      for (const kind of ["text", "image"])
        if (["base_url", "api_key", "model"].some((key) => next[kind][key]))
          validateConfig(next[kind], kind === "text" ? "文字模型" : "生图模型");
      if (dirty)
        data = await api("/settings", {
          method: "PUT",
          token: next.token,
          signal: controller.signal,
          body: settingsBody(next),
        });
      controller.signal.throwIfAborted();
      if (settingsRequest.current !== controller) return false;
      const localSaved = applyModelSettings(data, next.token);
      if (tokenChanged) activateAccount(null, null, { force: true });
      notice(
        localSaved
          ? data.initialized
            ? "模型配置已保存到服务端，重建容器后可继续使用。"
            : "访问令牌已保存，模型配置尚未设置。"
          : "服务端模型设置已应用，但浏览器本地凭证未能更新。",
        localSaved ? "" : "warning",
        "settingsStatus",
      );
      saved = true;
    } catch (error) {
      if (settingsRequest.current === controller && !controller.signal.aborted)
        notice(errorMessage(error), "error", "settingsStatus");
    } finally {
      if (settingsRequest.current === controller) {
        settingsRequest.current = null;
        update({ busy: false, operation: "", settingsLoading: false });
      }
    }
    if (saved && current.current.images.length)
      await run(
        "refresh-images",
        "正在读取已保存的配图…",
        async (scope, signal) => {
          const loaded = await loadImages(
            current.current.images,
            scope,
            signal,
            "settingsStatus",
          );
          ensureCurrent(scope);
          if (loaded.every((image) => !image.error))
            notice("设置已应用，所有配图已成功读取。", "", "settingsStatus");
        },
        "settingsStatus",
      );
    return saved;
  }

  async function clearSettings() {
    if (current.current.busy) return false;
    const controller = new AbortController();
    settingsRequest.current?.abort();
    settingsRequest.current = controller;
    update({ busy: true, operation: "clear-settings", settingsLoading: true });
    try {
      const token = current.current.settings.token;
      const data = await api("/settings", {
        method: "DELETE",
        token,
        signal: controller.signal,
      });
      controller.signal.throwIfAborted();
      if (settingsRequest.current !== controller) return false;
      applyModelSettings(data, token);
      update({ models: { text: [], image: [] } });
      notice(
        "已清空服务端模型配置，账号、草稿和服务访问令牌已保留。",
        "",
        "settingsStatus",
      );
      return true;
    } catch (error) {
      if (settingsRequest.current === controller && !controller.signal.aborted)
        notice(errorMessage(error), "error", "settingsStatus");
      return false;
    } finally {
      if (settingsRequest.current === controller) {
        settingsRequest.current = null;
        update({ busy: false, operation: "", settingsLoading: false });
      }
    }
  }

  async function readModels(kind) {
    await run(
      `${kind}-models`,
      "正在读取模型列表…",
      async (scope, signal) => {
        const config = trimmedConfig(current.current.settingsForm[kind]);
        validateConfig(
          config,
          kind === "text" ? "文字模型" : "生图模型",
          false,
        );
        const data = await api("/ai/models", {
          method: "POST",
          signal,
          body: { kind, config },
          token: current.current.settingsForm.token.trim(),
        });
        ensureCurrent(scope);
        const models = Array.isArray(data?.models)
          ? data.models.filter((model) => typeof model === "string")
          : [];
        update((value) => ({ models: { ...value.models, [kind]: models } }));
        notice(
          models.length
            ? `已读取 ${models.length} 个模型。点击模型名称输入框选择，也可以手动填写。`
            : "接口未返回模型列表，请手动填写模型名称。",
          models.length ? "" : "warning",
          "settingsStatus",
        );
      },
      "settingsStatus",
      true,
    );
  }

  function publishBody() {
    const { draft, images } = current.current;
    return {
      title: draft.title.trim(),
      content: draft.content.trim(),
      images: images.map((image) => image.path),
      tags: parseTags(draft.tags),
      visibility: draft.visibility,
    };
  }

  function validatePublish() {
    syncProtection();
    const value = current.current;
    const body = publishBody();
    if (value.uncertain)
      throw new Error(
        "上次发布结果尚未确认。请先核对小红书账号并解除页面顶部的发布锁定。",
      );
    if (!body.title || !body.content) throw new Error("请填写笔记标题和正文。");
    if (titleLength(value.draft.title) > 20)
      throw new Error("标题超过 20 字，请缩短后发布（英文和数字按半字计）。");
    if (Array.from(value.draft.content).length > 1000)
      throw new Error("正文超过 1000 字，请缩短后发布。");
    if (value.images.length < 1 || value.images.length > 9)
      throw new Error("请添加 1–9 张图片。");
    if (value.images.some((image) => !image.blobURL || image.error))
      throw new Error("存在未成功读取的配图，请修复或移除后再发布。");
    if (JSON.stringify(body) === value.lastPublished)
      throw new Error("这份内容已经发布成功，无需重复发布。");
    return body;
  }

  async function preparePublish(requireLogin, selectedAccount) {
    if (current.current.busy) return;
    try {
      if (!context.current.accountId) throw new Error("请先选择创作账号。");
      validatePublish();
    } catch (error) {
      update({ publishValidation: error.message });
      return;
    }
    await run("publish-check", "正在检查账号…", async (scope, signal) => {
      const account = await requireLogin(signal, scope);
      ensureCurrent(scope);
      if (account.account_id !== scope.accountId || !account.user_id)
        throw new Error("未能确认当前账号身份，请前往账号页重新验证登录。");
      const body = validatePublish();
      update({
        confirmation: {
          ...body,
          accountId: scope.accountId,
          generation: scope.generation,
          accountName: selectedAccount?.name || scope.accountId,
          username:
            account.username || selectedAccount?.nickname || "已登录账号",
          userId: account.user_id,
          fingerprint: JSON.stringify(body),
        },
        publishAgreed: false,
        publishStatus: emptyNotice(),
      });
      notice("内容检查通过，请在确认窗口核对后决定是否发布。");
    });
  }

  async function confirmPublish(requireLogin) {
    const confirmation = current.current.confirmation;
    await run(
      "publish-confirm",
      "正在发布，请勿关闭…",
      (scope, signal) =>
        withPublishLock(scope.accountId, async () => {
          ensureCurrent(scope);
          if (!current.current.publishAgreed || !confirmation)
            throw new Error("请先勾选发布确认。");
          if (
            confirmation.accountId !== scope.accountId ||
            confirmation.generation !== scope.generation
          )
            throw new Error("账号已切换，请重新检查并确认发布。");
          const account = await requireLogin(signal, scope);
          ensureCurrent(scope);
          if (
            account.account_id !== confirmation.accountId ||
            account.user_id !== confirmation.userId
          )
            throw new Error("登录身份已变化，请重新检查并确认发布。");
          // 持有跨标签页锁后重新读取保护，不能相信确认窗口的旧内存。
          const body = validatePublish();
          if (JSON.stringify(body) !== confirmation.fingerprint)
            throw new Error("草稿已变化，请返回编辑并重新确认发布。");
          const pending = writeProtection(
            scope.accountId,
            readProtection(scope.accountId, current.current),
            { uncertain: true },
          );
          update(protectionPatch(pending, current.current));
          if (!saveDraft(scope))
            throw new Error("发布保护未能保存，请恢复本地存储后重试。");
          try {
            const data = await accountAPI(scope, "/publish", {
              method: "POST",
              body,
              signal,
            });
            ensureCurrent(scope);
            if (data?.status !== "发布完成")
              throw new Error("服务未返回明确的发布完成状态。");
            const completed = writeProtection(scope.accountId, pending, {
              uncertain: false,
              lastPublished: JSON.stringify(body),
            });
            update({
              ...protectionPatch(completed, current.current),
              confirmation: null,
            });
            saveDraft(scope);
            notice(
              "笔记发布完成！草稿仍保存在此浏览器，你可以到小红书账号查看。不会再次自动发布。",
            );
          } catch (error) {
            ensureCurrent(scope);
            syncProtection(scope);
            saveDraft(scope);
            update({ confirmation: null });
            notice(
              `发布未能得到完成确认：${errorMessage(error)} 请求可能已生效，请先在小红书账号核对结果，避免重复发布。已锁定再次发布。`,
              "error",
            );
          }
        }),
      "publishStatus",
    );
  }

  async function unlockPublish() {
    if (current.current.busy || !current.current.reviewedPublish) return;
    const reviewedRevision = current.current.reviewedRevision;
    await run("unlock-publish", "正在核对发布保护…", (scope) =>
      withPublishLock(scope.accountId, () => {
        ensureCurrent(scope);
        const latest = syncProtection(scope);
        if (latest.revision !== reviewedRevision)
          throw new Error(
            "其他标签页已更新发布记录，请重新核对结果并勾选确认后再解除锁定。",
          );
        const unlocked = writeProtection(scope.accountId, latest, {
          uncertain: false,
        });
        update({
          ...protectionPatch(unlocked, current.current),
          reviewedPublish: false,
          reviewedRevision: null,
        });
        saveDraft(scope);
        notice(
          "已解除发布锁定。如果刚才的笔记已发布成功，请勿再次发布相同内容。",
          "warning",
        );
      }),
    );
  }

  function clearDraft() {
    if (current.current.busy) return;
    for (const url of objectURLs.current.values()) URL.revokeObjectURL(url);
    objectURLs.current.clear();
    // 清空内容时保留发布结果不明标记和已发布内容指纹。
    update({
      draft: emptyDraft(),
      sources: [],
      images: [],
      previewIndex: 0,
      clearDialog: false,
      publishValidation: "",
    });
    saveDraft();
    notice("草稿已清空，可以开始新的创作。");
  }

  return {
    ...state,
    api,
    context: state.context || context.current,
    activateAccount,
    flushDraft: saveDraft,
    changeDraft,
    selectSource,
    research,
    upload,
    generateImage,
    removeImage,
    changeSetting,
    saveSettings,
    clearSettings,
    readModels,
    preparePublish,
    confirmPublish,
    clearDraft,
    cancelResearch: () => researchController.current?.abort(),
    save: () => {
      const saved = saveDraft();
      notice(
        saved
          ? "草稿已保存在此浏览器。下次打开可继续编辑。"
          : "草稿未能保存到浏览器，请复制重要内容后再离开。",
        saved ? "" : "warning",
      );
    },
    showClearDialog: (open) => {
      if (!current.current.busy) update({ clearDialog: open });
    },
    closePublish: () => {
      if (!current.current.busy) update({ confirmation: null });
    },
    closeWorkspaceDialogs: () => {
      if (!current.current.busy)
        update({
          confirmation: null,
          clearDialog: false,
          publishAgreed: false,
        });
    },
    setPublishAgreed: (value) => update({ publishAgreed: value }),
    setReviewedPublish: (value) =>
      update({
        reviewedPublish: value,
        reviewedRevision: value ? current.current.publishRevision : null,
      }),
    unlockPublish,
    movePreview: (direction) =>
      update(({ images, previewIndex }) => ({
        previewIndex: images.length
          ? (previewIndex + direction + images.length) % images.length
          : 0,
      })),
  };
}
