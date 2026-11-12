import { useCallback, useEffect, useRef, useState } from "react";
import { createAPI, redact } from "./api.js";

const SETTINGS_KEY = "hongshu-workbench.settings.v1";
const DRAFT_KEY = "hongshu-workbench.draft.v1";
const emptySettings = () => ({
  text: { base_url: "", api_key: "", model: "" },
  image: { base_url: "", api_key: "", model: "" },
  token: "",
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
  return Object.fromEntries(
    ["base_url", "api_key", "model"].map((key) => [
      key,
      typeof value?.[key] === "string" ? value[key] : "",
    ]),
  );
}

function initialState() {
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
  const saved = load(DRAFT_KEY);
  const settings =
    savedSettings && typeof savedSettings === "object"
      ? {
          text: safeConfig(savedSettings.text),
          image: safeConfig(savedSettings.image),
          token:
            typeof savedSettings.token === "string" ? savedSettings.token : "",
        }
      : emptySettings();
  const draft = emptyDraft();
  for (const key of Object.keys(draft))
    if (typeof saved?.[key] === "string") draft[key] = saved[key];
  if (!["公开可见", "仅自己可见", "仅互关好友可见"].includes(draft.visibility))
    draft.visibility = "公开可见";
  return {
    settings,
    settingsForm: structuredClone(settings),
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
  if (!config.api_key.trim())
    throw new Error(`请在左侧「设置」中填写${name}的 API Key。`);
  if (requireModel && !config.model)
    throw new Error(`请在左侧「设置」中填写${name}的模型名称。`);
}

export function useWorkbench() {
  const [state, setState] = useState(initialState);
  const current = useRef(state);
  const researchController = useRef(null);
  const objectURLs = useRef(new Map());
  const firstSave = useRef(true);
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
  const saveDraft = useCallback(() => {
    const value = current.current;
    try {
      localStorage.setItem(
        DRAFT_KEY,
        JSON.stringify({
          ...value.draft,
          sources: value.sources,
          images: value.images.map(({ id, name, path, url }) => ({
            id,
            name,
            path,
            url,
          })),
          uncertain: value.uncertain,
          lastPublished: value.lastPublished,
        }),
      );
      update({
        saveStatus: `已保存 · ${new Date().toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`,
      });
      return true;
    } catch {
      storageFailure();
      return false;
    }
  }, [storageFailure, update]);

  useEffect(() => {
    if (firstSave.current) {
      firstSave.current = false;
      return;
    }
    update({ saveStatus: "正在保存…" });
    const timer = setTimeout(saveDraft, 450);
    return () => clearTimeout(timer);
  }, [
    state.draft,
    state.sources,
    state.images,
    state.uncertain,
    state.lastPublished,
    saveDraft,
    update,
  ]);

  useEffect(() => {
    function beforeUnload(event) {
      if (current.current.busy) {
        event.preventDefault();
        event.returnValue = "";
      }
    }
    window.addEventListener("pagehide", saveDraft);
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      window.removeEventListener("pagehide", saveDraft);
      window.removeEventListener("beforeunload", beforeUnload);
      researchController.current?.abort();
      for (const url of objectURLs.current.values()) URL.revokeObjectURL(url);
    };
  }, [saveDraft]);

  async function run(operation, label, action, target = "status") {
    if (current.current.busy) return;
    update({
      busy: true,
      operation,
      [target]: { message: label, type: "busy" },
    });
    try {
      await action();
    } catch (error) {
      notice(
        error.name === "AbortError"
          ? "已取消研究，原有草稿已保留。"
          : errorMessage(error),
        error.name === "AbortError" ? "warning" : "error",
        target,
      );
    } finally {
      update({ busy: false, operation: "" });
    }
  }

  function changeDraft(key, value) {
    if (current.current.busy) return;
    update(({ draft }) => ({
      draft: { ...draft, [key]: value },
      publishValidation: "",
    }));
  }

  async function loadImages(items, target = "status") {
    const loaded = await Promise.all(
      items.map(async (image) => {
        let blobURL = "";
        try {
          const blob = await api(`/images/${encodeURIComponent(image.id)}`, {
            blob: true,
          });
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
          const previous = objectURLs.current.get(image.id);
          if (previous) URL.revokeObjectURL(previous);
          objectURLs.current.set(image.id, blobURL);
          return { ...image, blobURL, error: "" };
        } catch (error) {
          if (blobURL) URL.revokeObjectURL(blobURL);
          return {
            ...image,
            error: `图片读取失败，请检查服务访问令牌，或移除后重新上传。${errorMessage(error)}`,
          };
        }
      }),
    );
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
      run("restore", "正在恢复草稿配图…", async () => {
        const loaded = await loadImages(current.current.images);
        if (loaded.every((image) => !image.error))
          notice("已恢复上次保存的草稿与配图，可以继续创作。");
      });
  }, []);

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

  async function composeSelected(signal) {
    const sources = current.current.sources.map((source) => ({ ...source }));
    const selected = sources.filter((source) => source.selected);
    if (!selected.length || selected.length > 5)
      throw new Error("请选择 1–5 篇参考笔记。");
    const complete = [];
    let failures = 0;
    for (const source of selected) {
      signal.throwIfAborted();
      if (!source.content) {
        notice(
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
          source.error = errorMessage(error);
          source.selected = false;
          failures++;
        }
      }
      if (source.content) complete.push(source);
      update({ sources: sources.map((item) => ({ ...item })) });
      saveDraft();
    }
    signal.throwIfAborted();
    if (!complete.length)
      throw new Error(
        "所选笔记均未成功读取正文，未调用写稿模型。请更换参考资料后重试。原有草稿已保留。",
      );
    notice(
      `${failures ? `${failures} 篇读取失败，已在参考列表标明。` : ""}正在基于 ${complete.length} 篇真实资料生成草稿…`,
      "busy",
    );
    const form = current.current.draft;
    const generated = await api("/ai/compose", {
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
    saveDraft();
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
      search ? "正在搜索与写稿…" : "正在重新生成…",
      async () => {
        const { settings, draft } = current.current;
        validateConfig(settings.text, "文字模型");
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
          await requireLogin(signal);
          signal.throwIfAborted();
          if (search) {
            const found = new Map();
            for (let index = 0; index < keywords.length; index++) {
              signal.throwIfAborted();
              notice(
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
            const sources = [...found.values()];
            let selected = 0;
            for (const source of sources)
              if (source.token && selected < 3) {
                source.selected = true;
                selected++;
              }
            update({ sources });
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
          researchController.current = null;
          update({ researching: false });
        }
      },
    );
  }

  async function upload(files) {
    if (!files.length) return;
    await run("upload", "正在上传配图…", async () => {
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
      update(({ images }) => ({ images: [...images, ...added] }));
      saveDraft();
      const loaded = await loadImages(added);
      notice(
        loaded.some((image) => image.error)
          ? "图片已上传，但部分预览读取失败。请检查设置，或移除后重新上传。"
          : `已添加 ${added.length} 张图片。第一张将作为封面。`,
        loaded.some((image) => image.error) ? "warning" : "",
      );
    });
  }

  async function generateImage() {
    await run("generate-image", "正在生成配图…", async () => {
      validateConfig(current.current.settings.image, "生图模型");
      const prompt = current.current.draft["image-prompt"].trim();
      if (!prompt) throw new Error("请先填写画面描述。");
      if (current.current.images.length >= 9)
        throw new Error("配图已达 9 张，请先移除一张。");
      const data = await api("/ai/image", {
        method: "POST",
        body: { config: current.current.settings.image, prompt },
      });
      if (!validImage(data?.image))
        throw new Error("生图服务未返回有效图片，请检查模型设置。");
      const image = { ...data.image, error: "", blobURL: "" };
      update(({ images }) => ({ images: [...images, image] }));
      saveDraft();
      const [loaded] = await loadImages([image]);
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
    update(({ settingsForm }) => ({
      settingsForm:
        kind === "token"
          ? { ...settingsForm, token: value }
          : {
              ...settingsForm,
              [kind]: { ...settingsForm[kind], [key]: value },
            },
    }));
  }

  async function saveSettings() {
    if (current.current.busy) return;
    try {
      const form = current.current.settingsForm;
      const next = {
        text: Object.fromEntries(
          Object.entries(form.text).map(([key, value]) => [key, value.trim()]),
        ),
        image: Object.fromEntries(
          Object.entries(form.image).map(([key, value]) => [key, value.trim()]),
        ),
        token: form.token.trim(),
      };
      for (const kind of ["text", "image"])
        if (Object.values(next[kind]).some(Boolean))
          validateConfig(next[kind], kind === "text" ? "文字模型" : "生图模型");
      update({ settings: next, settingsForm: structuredClone(next) });
      let saved = true;
      try {
        localStorage.setItem(SETTINGS_KEY, JSON.stringify(next));
      } catch {
        saved = false;
        storageFailure();
      }
      notice(
        saved
          ? "设置已保存，可前往账号页连接小红书，或返回工作台继续创作。"
          : "设置已应用于本次使用，但浏览器未能保存。",
        saved ? "" : "warning",
        "settingsStatus",
      );
      if (current.current.images.length)
        await run(
          "refresh-images",
          "正在读取已保存的配图…",
          async () => {
            const loaded = await loadImages(
              current.current.images,
              "settingsStatus",
            );
            if (loaded.every((image) => !image.error))
              notice("设置已应用，所有配图已成功读取。", "", "settingsStatus");
          },
          "settingsStatus",
        );
    } catch (error) {
      notice(errorMessage(error), "error", "settingsStatus");
    }
  }

  function clearSettings() {
    if (current.current.busy) return;
    const settings = emptySettings();
    update({
      settings,
      settingsForm: structuredClone(settings),
      models: { text: [], image: [] },
    });
    let removed = true;
    try {
      localStorage.removeItem(SETTINGS_KEY);
    } catch {
      removed = false;
      storageFailure();
    }
    notice(
      removed
        ? "已清除当前配置与浏览器保存的凭证；草稿已保留。"
        : "本次配置已清空，但无法删除浏览器中保存的凭证。请在浏览器设置中清除此站点的数据。",
      removed ? "" : "warning",
      "settingsStatus",
    );
  }

  async function readModels(kind) {
    await run(
      `${kind}-models`,
      "正在读取模型列表…",
      async () => {
        const config = Object.fromEntries(
          Object.entries(current.current.settingsForm[kind]).map(
            ([key, value]) => [key, value.trim()],
          ),
        );
        validateConfig(
          config,
          kind === "text" ? "文字模型" : "生图模型",
          false,
        );
        const data = await api("/ai/models", {
          method: "POST",
          body: { config },
          token: current.current.settingsForm.token.trim(),
        });
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

  async function preparePublish(requireLogin) {
    if (current.current.busy) return;
    try {
      validatePublish();
    } catch (error) {
      update({ publishValidation: error.message });
      return;
    }
    await run("publish-check", "正在检查账号…", async () => {
      const account = await requireLogin();
      const body = validatePublish();
      update({
        confirmation: { ...body, username: account.username || "已登录账号" },
        publishAgreed: false,
        publishStatus: emptyNotice(),
      });
      notice("内容检查通过，请在确认窗口核对后决定是否发布。");
    });
  }

  async function confirmPublish() {
    await run(
      "publish-confirm",
      "正在发布，请勿关闭…",
      async () => {
        if (!current.current.publishAgreed || !current.current.confirmation)
          throw new Error("请先勾选发布确认。");
        const body = validatePublish();
        // 先保存结果不明标记，断网或刷新不能绕过防重复发布保护。
        update({ uncertain: true, reviewedPublish: false });
        saveDraft();
        try {
          const data = await api("/publish", { method: "POST", body });
          if (data?.status !== "发布完成")
            throw new Error("服务未返回明确的发布完成状态。");
          update({
            uncertain: false,
            lastPublished: JSON.stringify(body),
            confirmation: null,
          });
          saveDraft();
          notice(
            "笔记发布完成！草稿仍保存在此浏览器，你可以到小红书账号查看。不会再次自动发布。",
          );
        } catch (error) {
          saveDraft();
          update({ confirmation: null });
          notice(
            `发布未能得到完成确认：${errorMessage(error)} 请求可能已生效，请先在小红书账号核对结果，避免重复发布。已锁定再次发布。`,
            "error",
          );
        }
      },
      "publishStatus",
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
    setReviewedPublish: (value) => update({ reviewedPublish: value }),
    unlockPublish: () => {
      if (!current.current.busy && current.current.reviewedPublish) {
        update({ uncertain: false, reviewedPublish: false });
        saveDraft();
        notice(
          "已解除发布锁定。如果刚才的笔记已发布成功，请勿再次发布相同内容。",
          "warning",
        );
      }
    },
    movePreview: (direction) =>
      update(({ images, previewIndex }) => ({
        previewIndex: images.length
          ? (previewIndex + direction + images.length) % images.length
          : 0,
      })),
  };
}
