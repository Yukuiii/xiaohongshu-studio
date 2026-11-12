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

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path) =>
      path.includes("/images/")
        ? {
            ok: true,
            blob: async () => new Blob(["pic"], { type: "image/png" }),
          }
        : ok({ status: "发布完成" }),
    ),
  );
});

async function open(result, id, legacy) {
  act(() => {
    expect(result.current.activateAccount(id, legacy)).toBe(true);
  });
  await waitFor(() => expect(result.current.busy).toBe(false));
}

async function ready() {
  localStorage.setItem(key("A"), JSON.stringify(draft("账号A")));
  const hook = renderHook(useWorkbench);
  await open(hook.result, "A");
  return hook;
}

describe("账号草稿", () => {
  it("切换时同步保存尚未到 450ms 的修改，并恢复各账号独立内容及发布锁", () => {
    vi.useFakeTimers();
    localStorage.setItem(
      key("A"),
      JSON.stringify({
        title: "A原稿",
        uncertain: true,
        lastPublished: "A记录",
      }),
    );
    localStorage.setItem(
      key("B"),
      JSON.stringify({ title: "B原稿", lastPublished: "B记录" }),
    );
    const { result } = renderHook(useWorkbench);
    act(() => {
      result.current.activateAccount("A");
    });
    act(() => {
      result.current.changeDraft("title", "A未落盘");
    });
    act(() => {
      vi.advanceTimersByTime(449);
    });
    expect(JSON.parse(localStorage.getItem(key("A"))).title).toBe("A原稿");
    act(() => {
      result.current.activateAccount("B");
    });
    expect(JSON.parse(localStorage.getItem(key("A")))).toMatchObject({
      title: "A未落盘",
      uncertain: true,
      lastPublished: "A记录",
    });
    expect(result.current.draft.title).toBe("B原稿");
    expect(result.current.uncertain).toBe(false);
    expect(result.current.lastPublished).toBe("B记录");
    act(() => {
      result.current.changeDraft("title", "B新稿");
      result.current.activateAccount("A");
    });
    expect(result.current.draft.title).toBe("A未落盘");
    expect(result.current.uncertain).toBe(true);
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(JSON.parse(localStorage.getItem(key("B"))).title).toBe("B新稿");
  });

  it("flush 失败保留原账号和未保存内容", () => {
    const { result } = renderHook(useWorkbench);
    act(() => {
      result.current.activateAccount("A");
      result.current.changeDraft("title", "重要内容");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    act(() => {
      expect(result.current.activateAccount("B")).toBe(false);
    });
    expect(result.current.context.accountId).toBe("A");
    expect(result.current.draft.title).toBe("重要内容");
  });

  it("旧草稿只迁至服务端 legacy 账号一次，保留源、路径和防重记录", async () => {
    const legacy = JSON.stringify(
      draft("旧草稿", { uncertain: true, lastPublished: "旧发布记录" }),
    );
    localStorage.setItem(legacyKey, legacy);
    const { result } = renderHook(useWorkbench);
    await open(result, "B", "A");
    expect(result.current.draft.title).toBe("");
    await open(result, "A", "A");
    expect(result.current.draft.title).toBe("旧草稿");
    expect(result.current.images[0].path).toBe(image.path);
    expect(result.current.uncertain).toBe(true);
    expect(result.current.lastPublished).toBe("旧发布记录");
    expect(localStorage.getItem(legacyKey)).toBe(legacy);
    act(() => {
      result.current.activateAccount("B");
    });
    localStorage.removeItem(key("A"));
    await open(result, "A", "A");
    expect(result.current.draft.title).toBe("");
  });

  it("迁移不覆盖目标账号已有草稿", async () => {
    localStorage.setItem(legacyKey, JSON.stringify({ title: "旧稿" }));
    localStorage.setItem(key("A"), JSON.stringify({ title: "已有稿" }));
    const { result } = renderHook(useWorkbench);
    await open(result, "A", "A");
    expect(result.current.draft.title).toBe("已有稿");
  });

  it("切换重新加载配图，释放 Blob，旧图片响应及旧保存不能污染新账号", async () => {
    const late = deferred();
    localStorage.setItem(key("A"), JSON.stringify(draft("A草稿")));
    localStorage.setItem(key("B"), JSON.stringify({ title: "B草稿" }));
    fetch.mockImplementationOnce(() => late.promise);
    const { result } = renderHook(useWorkbench);
    act(() => {
      result.current.activateAccount("A");
    });
    const old = result.current.context;
    expect(result.current.busy).toBe(true);
    act(() => {
      expect(result.current.activateAccount("B")).toBe(false);
    });
    act(() => {
      result.current.activateAccount("B", null, { force: true });
    });
    await act(async () => {
      late.resolve({
        ok: true,
        blob: async () => new Blob(["pic"], { type: "image/png" }),
      });
    });
    expect(result.current.draft.title).toBe("B草稿");
    expect(result.current.images).toEqual([]);
    expect(result.current.busy).toBe(false);
    act(() => {
      expect(result.current.flushDraft(old)).toBe(false);
    });
    expect(JSON.parse(localStorage.getItem(key("B"))).title).toBe("B草稿");
    await open(result, "A");
    expect(result.current.images[0].blobURL).toBe("blob:preview");
    await open(result, "B");
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview");
    expect(
      fetch.mock.calls.every(
        ([, options]) => options.headers["X-XHS-Account-ID"] === "A",
      ),
    ).toBe(true);
  });

  it("迁移标记写入失败仍恢复目标草稿", async () => {
    localStorage.setItem(key("A"), JSON.stringify({ title: "不能丢失" }));
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    const { result } = renderHook(useWorkbench);
    await open(result, "A", "A");
    expect(result.current.draft.title).toBe("不能丢失");
    expect(result.current.persistenceAvailable).toBe(false);
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

  it("服务令牌变化立即失效账号上下文，公共模型配置继续保存", async () => {
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
    expect(JSON.parse(localStorage.getItem(key("A"))).title).toBe("A草稿");
    expect(
      JSON.parse(localStorage.getItem("hongshu-workbench.settings.v1")).token,
    ).toBe("new-token");
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
    fetch.mockImplementationOnce(async (path, options) => {
      expect(path).toBe("/api/v1/publish");
      expect(options.headers["X-XHS-Account-ID"]).toBe("A");
      expect(JSON.parse(localStorage.getItem(key("A"))).uncertain).toBe(true);
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
    fetch.mockImplementationOnce(() => late.promise);
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
    expect(JSON.parse(localStorage.getItem(key("A"))).uncertain).toBe(true);
    expect(localStorage.getItem(key("B"))).toBeNull();
  });

  it("本地发布保护无法保存时不发送发布请求", async () => {
    const { result } = await ready();
    const login = vi.fn().mockResolvedValue(identity);
    await act(async () => {
      await result.current.preparePublish(login);
    });
    act(() => {
      result.current.setPublishAgreed(true);
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    await act(async () => {
      await result.current.confirmPublish(login);
    });
    expect(result.current.publishStatus.message).toContain("发布保护未能保存");
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
