import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useWorkbench } from "./useWorkbench.js";

const key = (id) => `hongshu-workbench.draft.v1:${id}`;
const legacyKey = "hongshu-workbench.draft.v1";
const image = { id: "pic", name: "封面", path: "/old/web/pic.png" };
const draft = (title, extra = {}) => ({
  title,
  content: "正文",
  images: [image],
  ...extra,
});
const ok = (data) => ({
  ok: true,
  status: 200,
  json: async () => ({ success: true, data }),
});
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
let serverDrafts;
let serverProtections;
const conflict = (code) => ({
  ok: false,
  status: 409,
  json: async () => ({ success: false, code, error: "版本冲突" }),
});
function seedDraft(accountId, payload, revision = 1) {
  serverDrafts.set(accountId, { payload, revision });
}
function seedProtection(accountId, value) {
  serverProtections.set(accountId, {
    revision: 1,
    uncertain: false,
    last_published: "",
    ...value,
  });
}

function modelStore(initial = {}) {
  let initialized = initial.initialized ?? true;
  let stored = {
    text: { base_url: "", api_key: "", model: "", ...initial.text },
    image: { base_url: "", api_key: "", model: "", ...initial.image },
  };
  const publicSettings = () => ({
    initialized,
    ...Object.fromEntries(
      ["text", "image"].map((kind) => [
        kind,
        {
          base_url: stored[kind].base_url,
          model: stored[kind].model,
          has_api_key: Boolean(stored[kind].api_key),
        },
      ]),
    ),
  });
  return vi.fn(async (path, options) => {
    if (path === "/api/v1/settings") {
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        if (body.only_if_uninitialized && initialized)
          return {
            ok: false,
            status: 409,
            json: async () => ({
              success: false,
              code: "SETTINGS_ALREADY_INITIALIZED",
              error: "配置已初始化",
            }),
          };
        for (const kind of ["text", "image"])
          stored[kind] = {
            ...body[kind],
            api_key: body[kind].api_key ?? stored[kind].api_key,
          };
        initialized = true;
      } else if (options.method === "DELETE") {
        stored = {
          text: { base_url: "", api_key: "", model: "" },
          image: { base_url: "", api_key: "", model: "" },
        };
        initialized = true;
      }
      return ok(publicSettings());
    }
    if (path === "/api/v1/ai/models") return ok({ models: ["stored-model"] });
    const accountId = options?.headers?.["X-XHS-Account-ID"];
    if (path === "/api/v1/draft") {
      if (options.method === "DELETE") {
        serverDrafts.delete(accountId);
        return ok({ revision: 0 });
      }
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        const current = serverDrafts.get(accountId)?.revision ?? 0;
        if (body.base_revision !== current)
          return conflict("DRAFT_REVISION_CONFLICT");
        serverDrafts.set(accountId, {
          payload: body.draft,
          revision: current + 1,
        });
        return ok({ revision: current + 1 });
      }
      const record = serverDrafts.get(accountId);
      return ok(
        record
          ? { draft: record.payload, revision: record.revision }
          : { draft: null, revision: 0 },
      );
    }
    if (path === "/api/v1/publish-protection") {
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        const current = serverProtections.get(accountId)?.revision ?? 0;
        if (body.base_revision !== current)
          return conflict("PROTECTION_REVISION_CONFLICT");
        const record = {
          revision: current + 1,
          uncertain: body.uncertain === true,
          last_published:
            typeof body.last_published === "string" ? body.last_published : "",
        };
        serverProtections.set(accountId, record);
        return ok({ revision: record.revision });
      }
      const record = serverProtections.get(accountId);
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
    return path.includes("/images/")
      ? { ok: true, blob: async () => new Blob(["pic"], { type: "image/png" }) }
      : ok({ status: "发布完成" });
  });
}

beforeEach(() => {
  serverDrafts = new Map();
  serverProtections = new Map();
  vi.stubGlobal("fetch", modelStore());
});

async function open(result, id, legacy) {
  act(() => {
    expect(result.current.activateAccount(id, legacy)).toBe(true);
  });
  await waitFor(() => expect(result.current.busy).toBe(false));
  await waitFor(() => expect(result.current.draftLoading).toBe(false));
}

async function ready() {
  seedDraft("A", draft("账号A"));
  const hook = renderHook(useWorkbench);
  await open(hook.result, "A");
  return hook;
}

describe("搜索与写稿步骤", () => {
  async function researchFixture({ configured = true } = {}) {
    seedDraft("A", {
      topic: "早餐灵感",
      keywords: "快手早餐,健康早餐",
      title: "保留的标题",
      content: "保留的正文",
      tags: "#原有话题",
      "image-prompt": "原有画面",
      sources: [
        {
          id: "old",
          token: "old-token",
          title: "原有参考",
          selected: true,
          content: "已有参考正文",
        },
      ],
    });
    const original = modelStore({
      text: configured
        ? {
            base_url: "https://model.example/v1",
            model: "text-model",
            api_key: "secret",
          }
        : {},
    });
    vi.stubGlobal(
      "fetch",
      vi.fn((path, options) => {
        if (path === "/api/v1/feeds/search")
          return Promise.resolve(
            ok({
              feeds: [1, 2, 3].map((n) => ({
                id: `ref-${n}`,
                xsecToken: `token-${n}`,
                noteCard: {
                  displayTitle: `参考${n}`,
                  user: { nickname: "作者" },
                },
              })),
            }),
          );
        if (path === "/api/v1/feeds/bodies") {
          const body = JSON.parse(options.body);
          return Promise.resolve(
            ok({
              items: body.items.map((item) => ({
                feed_id: item.feed_id,
                title: `标题 ${item.feed_id}`,
                content: `真实正文 ${item.feed_id}`,
                author: "参考作者",
              })),
            }),
          );
        }
        if (path === "/api/v1/ai/compose")
          return Promise.resolve(
            ok({
              title: "生成的标题",
              content: "生成的正文",
              tags: ["早餐"],
              image_prompt: "早餐画面",
            }),
          );
        return original(path, options);
      }),
    );
    const hook = renderHook(useWorkbench);
    await open(hook.result, "A");
    await waitFor(() => expect(hook.result.current.settingsReady).toBe(true));
    return { ...hook, fetch };
  }

  it("没有模型配置也可只搜索，去重并选择参考，不读取正文、不生成或覆盖旧稿", async () => {
    const { result, fetch } = await researchFixture({ configured: false });
    const previous = { ...result.current.draft };
    await act(async () => result.current.research(true));
    expect(
      fetch.mock.calls.filter(([path]) => path === "/api/v1/feeds/search"),
    ).toHaveLength(2);
    expect(
      fetch.mock.calls.some(
        ([path]) =>
          path === "/api/v1/feeds/bodies" || path === "/api/v1/ai/compose",
      ),
    ).toBe(false);
    expect(result.current.sources).toHaveLength(3);
    expect(result.current.sources.every((source) => source.selected)).toBe(
      true,
    );
    expect(result.current.draft).toEqual(previous);
    await waitFor(() =>
      expect(serverDrafts.get("A")?.payload.sources).toHaveLength(3),
    );
    expect(result.current.status.message).toContain("撰写文案");
  });

  it("单独写稿只批量读取已选参考，并使用真实正文生成", async () => {
    const { result, fetch } = await researchFixture();
    await act(async () => result.current.research(true));
    act(() => {
      result.current.selectSource("ref-2", false);
      result.current.selectSource("ref-3", false);
    });
    await act(async () => result.current.research(false));
    const bodies = fetch.mock.calls.filter(
      ([path]) => path === "/api/v1/feeds/bodies",
    );
    expect(bodies).toHaveLength(1);
    expect(JSON.parse(bodies[0][1].body).items).toEqual([
      { feed_id: "ref-1", xsec_token: "token-1" },
    ]);
    const compose = fetch.mock.calls.find(
      ([path]) => path === "/api/v1/ai/compose",
    );
    expect(JSON.parse(compose[1].body).sources).toEqual([
      expect.objectContaining({
        id: "ref-1",
        title: "标题 ref-1",
        content: "真实正文 ref-1",
        author: "参考作者",
      }),
    ]);
    expect(result.current.draft).toMatchObject({
      title: "生成的标题",
      content: "生成的正文",
      tags: "#早餐",
      "image-prompt": "早餐画面",
    });
  });

  it("已有正文的参考直接复用，不重复发起读取请求", async () => {
    const { result, fetch } = await researchFixture();
    await act(async () => result.current.research(false));
    expect(
      fetch.mock.calls.some(([path]) => path === "/api/v1/feeds/bodies"),
    ).toBe(false);
    const compose = fetch.mock.calls.find(
      ([path]) => path === "/api/v1/ai/compose",
    );
    expect(JSON.parse(compose[1].body).sources).toEqual([
      expect.objectContaining({ id: "old", content: "已有参考正文" }),
    ]);
  });

  it("部分参考读取失败时跳过该篇并继续写稿", async () => {
    const { result, fetch } = await researchFixture();
    await act(async () => result.current.research(true));
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) => {
      if (path !== "/api/v1/feeds/bodies") return original(path, options);
      const body = JSON.parse(options.body);
      return Promise.resolve(
        ok({
          items: body.items.map((item) =>
            item.feed_id === "ref-2"
              ? { feed_id: item.feed_id, error: "该笔记没有可用正文" }
              : {
                  feed_id: item.feed_id,
                  title: `标题 ${item.feed_id}`,
                  content: `真实正文 ${item.feed_id}`,
                  author: "参考作者",
                },
          ),
        }),
      );
    });
    await act(async () => result.current.research(false));
    const compose = fetch.mock.calls.find(
      ([path]) => path === "/api/v1/ai/compose",
    );
    const sent = JSON.parse(compose[1].body).sources.map((item) => item.id);
    expect(sent).toEqual(["ref-1", "ref-3"]);
    const failed = result.current.sources.find((item) => item.id === "ref-2");
    expect(failed).toMatchObject({
      selected: false,
      error: "该笔记没有可用正文",
    });
    expect(result.current.status.message).toContain("1 篇读取失败");
  });

  it("所选正文全部读取失败时保留旧稿，不调用写稿模型", async () => {
    const { result, fetch } = await researchFixture();
    await act(async () => result.current.research(true));
    const previous = { ...result.current.draft };
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) => {
      if (path !== "/api/v1/feeds/bodies") return original(path, options);
      const body = JSON.parse(options.body);
      return Promise.resolve(
        ok({
          items: body.items.map((item) => ({
            feed_id: item.feed_id,
            error: "该笔记没有可用正文",
          })),
        }),
      );
    });
    await act(async () => result.current.research(false));
    expect(
      fetch.mock.calls.some(([path]) => path === "/api/v1/ai/compose"),
    ).toBe(false);
    expect(result.current.draft).toEqual(previous);
    expect(result.current.status.message).toContain("原有草稿已保留");
  });

  it("写稿失败保留已有文案和图片提示词", async () => {
    const { result, fetch } = await researchFixture();
    const previous = { ...result.current.draft };
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) =>
      path === "/api/v1/ai/compose"
        ? Promise.resolve(ok({ title: "", content: "" }))
        : original(path, options),
    );
    await act(async () => result.current.research(false));
    expect(result.current.draft).toEqual(previous);
    expect(result.current.status.message).toContain("模型没有返回完整草稿");
  });

  it("取消搜索后忽略迟到响应，不覆盖原参考或继续写稿", async () => {
    const { result, fetch } = await researchFixture();
    const previous = { ...result.current.draft };
    const sources = result.current.sources;
    const late = deferred();
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) =>
      path === "/api/v1/feeds/search" ? late.promise : original(path, options),
    );
    let task;
    act(() => {
      task = result.current.research(true);
    });
    await waitFor(() =>
      expect(
        fetch.mock.calls.some(([path]) => path === "/api/v1/feeds/search"),
      ).toBe(true),
    );
    await act(async () => {
      result.current.cancelResearch();
      late.resolve(ok({ feeds: [{ id: "late", xsecToken: "late-token" }] }));
      await task;
    });
    expect(result.current.draft).toEqual(previous);
    expect(result.current.sources).toEqual(sources);
    expect(result.current.busy).toBe(false);
    expect(result.current.researching).toBe(false);
    expect(result.current.status.message).toContain("操作已取消");
    expect(
      fetch.mock.calls.some(([path]) => path === "/api/v1/ai/compose"),
    ).toBe(false);
  });
});

describe("服务端模型配置", () => {
  const legacyKey = "hongshu-workbench.settings.v1";
  const tokenKey = "hongshu-workbench.token.v1";
  const configured = {
    base_url: "https://example.com/v1",
    api_key: "server-secret",
    model: "saved-model",
  };

  it("读取服务端配置时密钥输入框留空，并按模型类型读取列表", async () => {
    vi.stubGlobal("fetch", modelStore({ text: configured, image: configured }));
    const { result } = renderHook(useWorkbench);
    await waitFor(() => expect(result.current.settingsReady).toBe(true));
    expect(result.current.settings.text).toMatchObject({
      base_url: configured.base_url,
      model: configured.model,
      api_key: "",
      has_api_key: true,
    });
    expect(result.current.settingsForm.text.api_key).toBe("");
    await act(async () => {
      await result.current.readModels("image");
    });
    expect(result.current.models.image).toEqual(["stored-model"]);
    const request = fetch.mock.calls.find(
      ([path]) => path === "/api/v1/ai/models",
    );
    expect(JSON.parse(request[1].body)).toMatchObject({
      kind: "image",
      config: { api_key: "" },
    });
    expect(JSON.stringify(localStorage)).not.toContain("server-secret");
  });

  it("初始化期间更改接口地址会清除旧浏览器密钥，读取模型不能转发旧密钥", async () => {
    const late = deferred();
    const original = modelStore();
    let pending = true;
    vi.stubGlobal(
      "fetch",
      vi.fn((path, options) => {
        if (
          path === "/api/v1/settings" &&
          options.method === "GET" &&
          pending
        ) {
          pending = false;
          return late.promise;
        }
        return original(path, options);
      }),
    );
    localStorage.setItem(legacyKey, JSON.stringify({ text: configured }));
    const { result } = renderHook(useWorkbench);
    act(() => {
      result.current.changeSetting(
        "text",
        "base_url",
        "https://other.example/v1",
      );
    });
    expect(result.current.settingsForm.text.api_key).toBe("");
    await act(async () => {
      await result.current.readModels("text");
    });
    expect(
      fetch.mock.calls.some(([path]) => path === "/api/v1/ai/models"),
    ).toBe(false);
    await act(async () => {
      late.resolve(ok({ initialized: true, text: {}, image: {} }));
    });
    expect(result.current.settingsReady).toBe(true);
    expect(result.current.settingsForm.text.api_key).toBe("");
    await act(async () => {
      await result.current.readModels("text");
    });
    expect(
      fetch.mock.calls.some(([path]) => path === "/api/v1/ai/models"),
    ).toBe(false);
    expect(JSON.stringify(fetch.mock.calls)).not.toContain(configured.api_key);
  });

  it("初始化时只编辑文字模型，读取和保存均保留未编辑的生图配置", async () => {
    const late = deferred();
    const original = modelStore({ image: configured });
    let pending = true;
    vi.stubGlobal(
      "fetch",
      vi.fn((path, options) => {
        if (
          path === "/api/v1/settings" &&
          options.method === "GET" &&
          pending
        ) {
          pending = false;
          return late.promise;
        }
        return original(path, options);
      }),
    );
    const { result } = renderHook(useWorkbench);
    act(() => {
      result.current.changeSetting(
        "text",
        "base_url",
        "https://text.example/v1",
      );
      result.current.changeSetting("text", "api_key", "new-text-key");
      result.current.changeSetting("text", "model", "new-text-model");
    });
    await act(async () => {
      late.resolve(
        ok({
          initialized: true,
          text: {},
          image: {
            base_url: configured.base_url,
            model: configured.model,
            has_api_key: true,
          },
        }),
      );
    });
    expect(result.current.settingsForm.image.base_url).toBe(
      configured.base_url,
    );
    expect(result.current.settingsForm.image.model).toBe(configured.model);
    await act(async () => {
      expect(await result.current.saveSettings()).toBe(true);
    });
    const saved = fetch.mock.calls.find(
      ([, options]) => options.method === "PUT",
    );
    expect(JSON.parse(saved[1].body).image).toEqual({
      base_url: configured.base_url,
      model: configured.model,
    });
    expect(result.current.settings.image.has_api_key).toBe(true);
    expect(result.current.settings.image.model).toBe(configured.model);
  });

  it("旧浏览器配置只迁入未初始化的服务端，新浏览器可从服务端重新读取", async () => {
    const api = modelStore({ initialized: false });
    vi.stubGlobal("fetch", api);
    localStorage.setItem(
      legacyKey,
      JSON.stringify({
        text: configured,
        image: { base_url: "", api_key: "", model: "" },
        token: "workbench-token",
      }),
    );
    const first = renderHook(useWorkbench);
    await waitFor(() => expect(first.result.current.settingsReady).toBe(true));
    const saves = api.mock.calls.filter(
      ([path, options]) =>
        path === "/api/v1/settings" && options.method === "PUT",
    );
    expect(saves).toHaveLength(1);
    expect(JSON.parse(saves[0][1].body)).toMatchObject({
      only_if_uninitialized: true,
      text: configured,
    });
    expect(localStorage.getItem(legacyKey)).toBeNull();
    expect(localStorage.getItem(tokenKey)).toBe("workbench-token");
    expect(first.result.current.settings.text.api_key).toBe("");
    first.unmount();
    localStorage.clear();
    const second = renderHook(useWorkbench);
    await waitFor(() => expect(second.result.current.settingsReady).toBe(true));
    expect(second.result.current.settings.text.model).toBe(configured.model);
    expect(second.result.current.settings.text.has_api_key).toBe(true);
    expect(
      api.mock.calls.filter(([, options]) => options.method === "PUT"),
    ).toHaveLength(1);
  });

  it("服务端已有配置时忽略旧浏览器的模型值，清理旧密钥缓存", async () => {
    vi.stubGlobal("fetch", modelStore({ text: configured }));
    localStorage.setItem(
      legacyKey,
      JSON.stringify({
        text: { ...configured, api_key: "old-browser-key", model: "old-model" },
        token: "service-token",
      }),
    );
    const { result } = renderHook(useWorkbench);
    await waitFor(() => expect(result.current.settingsReady).toBe(true));
    expect(result.current.settings.text.model).toBe("saved-model");
    expect(
      fetch.mock.calls.some(([, options]) => options.method === "PUT"),
    ).toBe(false);
    expect(localStorage.getItem(legacyKey)).toBeNull();
    expect(localStorage.getItem(tokenKey)).toBe("service-token");
  });

  it("迁移失败保留旧密钥与表单，重试保存成功后才删除旧缓存", async () => {
    const original = modelStore({ initialized: false });
    let fail = true;
    vi.stubGlobal(
      "fetch",
      vi.fn((path, options) => {
        if (path === "/api/v1/settings" && options.method === "PUT" && fail)
          return Promise.resolve({
            ok: false,
            status: 500,
            json: async () => ({ success: false, error: "配置文件暂时不可写" }),
          });
        return original(path, options);
      }),
    );
    const legacy = JSON.stringify({ text: configured });
    localStorage.setItem(legacyKey, legacy);
    const { result } = renderHook(useWorkbench);
    await waitFor(() => expect(result.current.settingsLoading).toBe(false));
    expect(result.current.settingsReady).toBe(false);
    expect(result.current.settingsForm.text.api_key).toBe("server-secret");
    expect(localStorage.getItem(legacyKey)).toBe(legacy);
    fail = false;
    await act(async () => {
      expect(await result.current.saveSettings()).toBe(true);
    });
    expect(result.current.settingsReady).toBe(true);
    expect(result.current.settings.text.has_api_key).toBe(true);
    expect(localStorage.getItem(legacyKey)).toBeNull();
  });

  it("修改模型时保留服务端密钥，替换密钥后再次清空密码输入框", async () => {
    vi.stubGlobal("fetch", modelStore({ text: configured }));
    const { result } = renderHook(useWorkbench);
    await waitFor(() => expect(result.current.settingsReady).toBe(true));
    act(() => {
      result.current.changeSetting("text", "model", "new-model");
    });
    await act(async () => {
      expect(await result.current.saveSettings()).toBe(true);
    });
    let writes = fetch.mock.calls.filter(
      ([, options]) => options.method === "PUT",
    );
    expect(JSON.parse(writes[0][1].body).text).not.toHaveProperty("api_key");
    act(() => {
      result.current.changeSetting("text", "api_key", " replacement-secret ");
    });
    await act(async () => {
      expect(await result.current.saveSettings()).toBe(true);
    });
    writes = fetch.mock.calls.filter(([, options]) => options.method === "PUT");
    expect(JSON.parse(writes[1][1].body).text.api_key).toBe(
      "replacement-secret",
    );
    expect(result.current.settingsForm.text.api_key).toBe("");
    expect(result.current.settings.text.has_api_key).toBe(true);
    expect(JSON.stringify(localStorage)).not.toContain("replacement-secret");
  });

  it("清空服务器模型时保留账号、草稿和令牌，旧浏览器配置不能复活", async () => {
    const api = modelStore({ text: configured });
    vi.stubGlobal("fetch", api);
    localStorage.setItem(tokenKey, "service-token");
    const first = renderHook(useWorkbench);
    await waitFor(() => expect(first.result.current.settingsReady).toBe(true));
    await open(first.result, "A");
    act(() => {
      first.result.current.changeDraft("title", "保留草稿");
    });
    await act(async () => {
      expect(await first.result.current.clearSettings()).toBe(true);
    });
    expect(first.result.current.context.accountId).toBe("A");
    expect(first.result.current.draft.title).toBe("保留草稿");
    expect(localStorage.getItem(tokenKey)).toBe("service-token");
    first.unmount();
    localStorage.setItem(legacyKey, JSON.stringify({ text: configured }));
    const second = renderHook(useWorkbench);
    await waitFor(() => expect(second.result.current.settingsReady).toBe(true));
    expect(second.result.current.settings.text).toMatchObject({
      base_url: "",
      api_key: "",
      model: "",
      has_api_key: false,
    });
    expect(api.mock.calls.some(([, options]) => options.method === "PUT")).toBe(
      false,
    );
  });

  it("补充工作台令牌后先读取服务器配置，不用空表单覆盖已有模型", async () => {
    const original = modelStore({ text: configured });
    localStorage.setItem(tokenKey, "wrong-token");
    vi.stubGlobal(
      "fetch",
      vi.fn((path, options) => {
        if (
          path === "/api/v1/settings" &&
          options.headers.Authorization !== "Bearer correct-token"
        )
          return Promise.resolve({
            ok: false,
            status: 401,
            json: async () => ({ success: false }),
          });
        return original(path, options);
      }),
    );
    const { result } = renderHook(useWorkbench);
    await waitFor(() => expect(result.current.settingsLoading).toBe(false));
    act(() => {
      result.current.changeSetting("token", null, "correct-token");
    });
    await act(async () => {
      expect(await result.current.saveSettings()).toBe(true);
    });
    expect(result.current.settings.text.model).toBe(configured.model);
    expect(result.current.settings.text.has_api_key).toBe(true);
    expect(localStorage.getItem(tokenKey)).toBe("correct-token");
    expect(
      fetch.mock.calls.some(([, options]) => options.method === "PUT"),
    ).toBe(false);
  });

  it("只保存访问令牌不初始化空模型配置，仍允许旧浏览器随后迁移", async () => {
    const api = modelStore({ initialized: false });
    vi.stubGlobal("fetch", api);
    const first = renderHook(useWorkbench);
    await waitFor(() => expect(first.result.current.settingsReady).toBe(true));
    act(() => {
      first.result.current.changeSetting("token", null, "service-token");
    });
    await act(async () => {
      expect(await first.result.current.saveSettings()).toBe(true);
    });
    expect(api.mock.calls.some(([, options]) => options.method === "PUT")).toBe(
      false,
    );
    expect(localStorage.getItem(tokenKey)).toBe("service-token");
    first.unmount();
    localStorage.setItem(legacyKey, JSON.stringify({ text: configured }));
    const second = renderHook(useWorkbench);
    await waitFor(() => expect(second.result.current.settingsReady).toBe(true));
    expect(second.result.current.settings.text.model).toBe(configured.model);
    expect(
      api.mock.calls.filter(([, options]) => options.method === "PUT"),
    ).toHaveLength(1);
  });

  it("旧令牌的迟到配置不能覆盖新令牌读取的模型", async () => {
    const late = deferred();
    const original = modelStore({ text: configured });
    localStorage.setItem(tokenKey, "old-token");
    vi.stubGlobal(
      "fetch",
      vi.fn((path, options) =>
        path === "/api/v1/settings" &&
        options.headers.Authorization === "Bearer old-token"
          ? late.promise
          : original(path, options),
      ),
    );
    const { result } = renderHook(useWorkbench);
    act(() => {
      result.current.changeSetting("token", null, "new-token");
    });
    await act(async () => {
      expect(await result.current.saveSettings()).toBe(true);
    });
    await act(async () => {
      late.resolve(
        ok({
          initialized: true,
          text: { ...configured, model: "stale-model" },
          image: {},
        }),
      );
    });
    expect(result.current.settings.token).toBe("new-token");
    expect(result.current.settings.text.model).toBe(configured.model);
    expect(localStorage.getItem(tokenKey)).toBe("new-token");
  });

  it("迁移并发冲突后读取已有配置，不覆盖另一浏览器的设置", async () => {
    const original = modelStore({ text: configured });
    let firstRead = true;
    vi.stubGlobal(
      "fetch",
      vi.fn((path, options) => {
        if (
          path === "/api/v1/settings" &&
          options.method === "GET" &&
          firstRead
        ) {
          firstRead = false;
          return Promise.resolve(
            ok({ initialized: false, text: {}, image: {} }),
          );
        }
        return original(path, options);
      }),
    );
    localStorage.setItem(
      legacyKey,
      JSON.stringify({ text: { ...configured, model: "losing-model" } }),
    );
    const { result } = renderHook(useWorkbench);
    await waitFor(() => expect(result.current.settingsReady).toBe(true));
    expect(result.current.settings.text.model).toBe(configured.model);
    expect(localStorage.getItem(legacyKey)).toBeNull();
  });
});

describe("账号草稿", () => {
  it("切换账号时保存当前草稿并加载各自的服务端内容", async () => {
    seedDraft("A", draft("A原稿"));
    seedProtection("A", { uncertain: true, last_published: "A记录" });
    seedDraft("B", { title: "B原稿" });
    const { result } = renderHook(useWorkbench);
    await open(result, "A");
    expect(result.current.draft.title).toBe("A原稿");
    expect(result.current.uncertain).toBe(true);
    expect(result.current.lastPublished).toBe("A记录");

    act(() => result.current.changeDraft("title", "A新稿"));
    act(() => result.current.activateAccount("B"));
    await waitFor(() => expect(result.current.draft.title).toBe("B原稿"));
    await waitFor(() =>
      expect(serverDrafts.get("A")?.payload.title).toBe("A新稿"),
    );
    expect(result.current.uncertain).toBe(false);

    await open(result, "A");
    expect(result.current.draft.title).toBe("A新稿");
    expect(result.current.uncertain).toBe(true);
    expect(result.current.lastPublished).toBe("A记录");
  });

  it("草稿版本冲突时停止自动保存并提示", async () => {
    seedDraft("A", { title: "服务端草稿" });
    const { result } = renderHook(useWorkbench);
    await open(result, "A");
    serverDrafts.set("A", {
      payload: { title: "其他窗口的草稿" },
      revision: 9,
    });
    act(() => result.current.changeDraft("title", "本地修改"));
    await waitFor(() => expect(result.current.draftConflict).toBe(true));
    expect(result.current.saveStatus).toContain("其他窗口");
    expect(result.current.draft.title).toBe("本地修改");
  });

  it("服务令牌变化立即失效账号上下文，保存的草稿仍在服务端", async () => {
    seedDraft("A", { title: "A原稿" });
    const { result } = renderHook(useWorkbench);
    await open(result, "A");
    act(() => {
      result.current.changeDraft("title", "A草稿");
      result.current.changeSetting("token", null, "new-token");
    });
    const previous = result.current.context;
    await act(async () => {
      await result.current.saveSettings();
    });
    expect(result.current.context.accountId).toBeNull();
    expect(result.current.context.generation).toBeGreaterThan(
      previous.generation,
    );
    expect(result.current.settings.token).toBe("new-token");
    await waitFor(() =>
      expect(serverDrafts.get("A")?.payload.title).toBe("A草稿"),
    );
    expect(localStorage.getItem("hongshu-workbench.token.v1")).toBe(
      "new-token",
    );
  });

  it("无账号选择时仍可读取公共模型列表", async () => {
    const { result } = renderHook(useWorkbench);
    act(() => {
      result.current.changeSetting(
        "text",
        "base_url",
        "https://example.com/v1",
      );
      result.current.changeSetting("text", "api_key", "key");
    });
    fetch.mockResolvedValueOnce(ok({ models: ["model-a"] }));
    await act(async () => {
      await result.current.readModels("text");
    });
    expect(result.current.models.text).toEqual(["model-a"]);
    expect(fetch.mock.calls.at(-1)[1].headers).not.toHaveProperty(
      "X-XHS-Account-ID",
    );
  });
});

describe("发布账号保护", () => {
  const identity = {
    account_id: "A",
    user_id: "user-A",
    username: "作者A",
    is_logged_in: true,
  };

  it("确认固定账号与身份，切换后撤销确认", async () => {
    const { result } = await ready();
    const login = vi.fn().mockResolvedValue(identity);
    await act(async () => {
      await result.current.preparePublish(login, { name: "备注A" });
    });
    expect(result.current.confirmation).toMatchObject({
      accountId: "A",
      userId: "user-A",
      accountName: "备注A",
      username: "作者A",
    });
    await open(result, "B");
    expect(result.current.confirmation).toBeNull();
    await act(async () => {
      await result.current.confirmPublish(login);
    });
    expect(fetch.mock.calls.some(([path]) => path.endsWith("/publish"))).toBe(
      false,
    );
  });

  it("发送前重新核对身份，身份变化时阻止发布", async () => {
    const { result } = await ready();
    const login = vi
      .fn()
      .mockResolvedValueOnce(identity)
      .mockResolvedValueOnce({ ...identity, user_id: "user-B" });
    await act(async () => {
      await result.current.preparePublish(login);
    });
    act(() => {
      result.current.setPublishAgreed(true);
    });
    await act(async () => {
      await result.current.confirmPublish(login);
    });
    expect(result.current.publishStatus.message).toContain("登录身份已变化");
    expect(fetch.mock.calls.some(([path]) => path.endsWith("/publish"))).toBe(
      false,
    );
  });

  it("先保存 A 的未知结果保护再发送，失败只锁定 A，B 不受影响", async () => {
    const { result } = await ready();
    const login = vi.fn().mockResolvedValue(identity);
    await act(async () => {
      await result.current.preparePublish(login);
    });
    act(() => {
      result.current.setPublishAgreed(true);
    });
    const original = fetch.getMockImplementation();
    fetch.mockImplementation(async (path, options) => {
      if (path !== "/api/v1/publish") return original(path, options);
      expect(options.headers["X-XHS-Account-ID"]).toBe("A");
      expect(serverProtections.get("A")?.uncertain).toBe(true);
      throw new Error("network");
    });
    await act(async () => {
      await result.current.confirmPublish(login);
    });
    expect(result.current.uncertain).toBe(true);
    await open(result, "B");
    expect(result.current.uncertain).toBe(false);
    await open(result, "A");
    expect(result.current.uncertain).toBe(true);
    await act(async () => {
      await result.current.preparePublish(login);
    });
    expect(result.current.publishValidation).toContain("上次发布结果尚未确认");
  });

  it("A 的迟到发布响应在上下文失效后不能改写 B 的草稿或发布记录", async () => {
    const { result } = await ready();
    const login = vi.fn().mockResolvedValue(identity);
    await act(async () => {
      await result.current.preparePublish(login);
    });
    act(() => {
      result.current.setPublishAgreed(true);
    });
    const late = deferred();
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) =>
      path === "/api/v1/publish" ? late.promise : original(path, options),
    );
    let publishing;
    act(() => {
      publishing = result.current.confirmPublish(login);
    });
    await waitFor(() => expect(result.current.uncertain).toBe(true));
    act(() => {
      result.current.activateAccount("B", null, { force: true });
    });
    await act(async () => {
      late.resolve(ok({ status: "发布完成" }));
      await publishing;
    });
    expect(result.current.context.accountId).toBe("B");
    expect(result.current.lastPublished).toBe("");
    expect(result.current.uncertain).toBe(false);
    expect(result.current.confirmation).toBeNull();
    expect(serverProtections.get("A")?.uncertain).toBe(true);
    expect(serverProtections.get("B")?.uncertain ?? false).toBe(false);
  });

  it("服务端发布保护保存失败时不发送发布请求", async () => {
    const { result } = await ready();
    const login = vi.fn().mockResolvedValue(identity);
    await act(async () => {
      await result.current.preparePublish(login);
    });
    act(() => {
      result.current.setPublishAgreed(true);
    });
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) =>
      path === "/api/v1/publish-protection" && options.method === "PUT"
        ? Promise.reject(new Error("network"))
        : original(path, options),
    );
    await act(async () => {
      await result.current.confirmPublish(login);
    });
    expect(result.current.publishStatus.message).toContain("无法连接服务");
    expect(fetch.mock.calls.some(([path]) => path.endsWith("/publish"))).toBe(
      false,
    );
  });

  it("成功发布只保存 A 的防重指纹，重新检查时阻止重复内容", async () => {
    const { result } = await ready();
    const login = vi.fn().mockResolvedValue(identity);
    await act(async () => {
      await result.current.preparePublish(login);
    });
    act(() => {
      result.current.setPublishAgreed(true);
    });
    await act(async () => {
      await result.current.confirmPublish(login);
    });
    expect(result.current.uncertain).toBe(false);
    expect(result.current.lastPublished).toContain("账号A");
    await act(async () => {
      await result.current.preparePublish(login);
    });
    expect(result.current.publishValidation).toContain("已经发布成功");
    await open(result, "B");
    expect(result.current.lastPublished).toBe("");
  });
});
