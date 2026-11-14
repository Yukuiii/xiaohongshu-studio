import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useWorkbench } from "./useWorkbench.js";
import { samePublishedContent } from "./publishProtection.js";

const identity = {
  account_id: "A",
  user_id: "user-A",
  username: "作者A",
  is_logged_in: true,
};
const draft = {
  title: "同一笔记",
  content: "正文",
  images: [{ id: "pic", name: "配图", path: "/old/web/pic.png" }],
};
const ok = (data) => ({
  ok: true,
  status: 200,
  json: async () => ({ success: true, data }),
});
const conflict = (code) => ({
  ok: false,
  status: 409,
  json: async () => ({ success: false, code, error: "版本冲突" }),
});

let drafts;
let protections;
let tasks;
const task = {
  id: "a".repeat(32), account_id: "A", account_name: "账号A", title: draft.title,
  kind: "publish", status: "queued", message: "等待执行",
};

function server() {
  return vi.fn(async (path, options = {}) => {
    const accountId = options.headers?.["X-XHS-Account-ID"];
    if (path === "/api/v1/settings")
      return ok({ initialized: true, text: {}, image: {} });
    if (path === "/api/v1/draft") {
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        const current = drafts.get(accountId)?.revision ?? 0;
        if (body.base_revision !== current)
          return conflict("DRAFT_REVISION_CONFLICT");
        drafts.set(accountId, {
          payload: body.draft,
          revision: current + 1,
        });
        return ok({ revision: current + 1 });
      }
      const record = drafts.get(accountId);
      return ok(
        record
          ? { draft: record.payload, revision: record.revision }
          : { draft: null, revision: 0 },
      );
    }
    if (path === "/api/v1/publish-protection") {
      if (options.method === "PUT") {
        const body = JSON.parse(options.body);
        const current = protections.get(accountId)?.revision ?? 0;
        if (body.base_revision !== current)
          return conflict("PROTECTION_REVISION_CONFLICT");
        const record = {
          revision: current + 1,
          uncertain: body.uncertain === true,
          last_published:
            typeof body.last_published === "string" ? body.last_published : "",
        };
        protections.set(accountId, record);
        return ok({ revision: record.revision });
      }
      const record = protections.get(accountId);
      return ok(
        record
          ? {
              revision: record.revision,
              uncertain: record.uncertain,
              last_published: record.last_published,
              active_task_id: record.active_task_id || "",
            }
          : { revision: 0, uncertain: false, last_published: "" },
      );
    }
    if (path.startsWith("/api/v1/tasks/")) return ok({ task: tasks.get(task.id) });
    if (path === "/api/v1/publish") {
      tasks.set(task.id, task);
      protections.set(accountId, { revision: 1, uncertain: true, last_published: "", active_task_id: task.id });
      return { ...ok({ task }), status: 202 };
    }
    if (path.includes("/images/"))
      return {
        ok: true,
        blob: async () => new Blob(["pic"], { type: "image/png" }),
      };
    return ok({});
  });
}

beforeEach(() => {
  drafts = new Map([["A", { payload: draft, revision: 1 }]]);
  protections = new Map();
  tasks = new Map();
  vi.stubGlobal("fetch", server());
});

async function pair() {
  const first = renderHook(useWorkbench);
  const second = renderHook(useWorkbench);
  act(() => {
    first.result.current.activateAccount("A");
    second.result.current.activateAccount("A");
  });
  await waitFor(() => {
    expect(first.result.current.draftLoading).toBe(false);
    expect(second.result.current.draftLoading).toBe(false);
    expect(first.result.current.busy).toBe(false);
    expect(second.result.current.busy).toBe(false);
  });
  return [first.result, second.result];
}

async function prepare(result) {
  await act(async () => {
    await result.current.preparePublish(async () => identity);
  });
  act(() => result.current.setPublishAgreed(true));
}

describe("服务端发布保护", () => {
  it("结果不明时锁定记录，第二个浏览器无法再次发布", async () => {
    const [first, second] = await pair();
    await prepare(first);
    const original = fetch.getMockImplementation();
    fetch.mockImplementation(async (path, options) => {
      const response = await original(path, options);
      if (path === "/api/v1/publish") {
        tasks.set(task.id, { ...task, status: "uncertain" });
        protections.set("A", { revision: 2, uncertain: true, last_published: "", active_task_id: "" });
        throw new Error("offline");
      }
      return response;
    });
    await act(async () => {
      await first.current.confirmPublish(async () => identity);
    });
    expect(protections.get("A")).toMatchObject({
      uncertain: true,
      last_published: "",
    });
    expect(first.current.submissionUnknown).toBe(true);

    await prepare(second);
    expect(second.current.status.message).toContain("尚未确认");
    expect(
      fetch.mock.calls.filter(([path]) => path === "/api/v1/publish"),
    ).toHaveLength(1);
  });

  it("成功后保存防重指纹，另一个浏览器检查时阻止重复内容", async () => {
    const [first, second] = await pair();
    await prepare(first);
    await act(async () => {
      await first.current.confirmPublish(async () => identity);
    });
    tasks.set(task.id, { ...task, status: "succeeded" });
    protections.set("A", {
      revision: 2, uncertain: false, active_task_id: "",
      last_published: JSON.stringify({ title: draft.title, content: draft.content, images: [draft.images[0].path], tags: [], visibility: "公开可见" }),
    });
    await act(async () => window.dispatchEvent(new Event("publish-tasks-changed")));
    await waitFor(() => expect(first.current.lastPublished).toContain(draft.title));
    const stored = protections.get("A");
    expect(stored.uncertain).toBe(false);
    expect(stored.last_published).toContain(draft.title);
    expect(first.current.lastPublished).toContain(draft.title);

    await act(async () => {
      await second.current.preparePublish(async () => identity);
    });
    expect(second.current.status.message).toContain("已经发布成功");
    expect(
      fetch.mock.calls.filter(([path]) => path === "/api/v1/publish"),
    ).toHaveLength(1);
  });

  it("同时发布时第二个页面被 Web Locks 拒绝", async () => {
    const [first, second] = await pair();
    await prepare(first);
    await prepare(second);
    let resolvePublish;
    const publishing = new Promise((resolve) => {
      resolvePublish = resolve;
    });
    const original = fetch.getMockImplementation();
    fetch.mockImplementation((path, options) =>
      path === "/api/v1/publish" ? publishing : original(path, options),
    );
    let pending;
    act(() => {
      pending = first.current.confirmPublish(async () => identity);
    });
    await waitFor(() => expect(fetch.mock.calls.some(([path]) => path === "/api/v1/publish")).toBe(true));
    await act(async () => {
      await second.current.confirmPublish(async () => identity);
    });
    expect(second.current.publishStatus.message).toContain(
      "另一个标签页正在处理此账号的发布",
    );
    await act(async () => {
      tasks.set(task.id, task);
      protections.set("A", { revision: 1, uncertain: true, last_published: "", active_task_id: task.id });
      resolvePublish({ ...ok({ task }), status: 202 });
      await pending;
    });
  });

  it("过期复核不能解除更新的发布记录", async () => {
    protections.set("A", {
      revision: 1,
      uncertain: true,
      last_published: "旧指纹",
    });
    const [first, second] = await pair();
    act(() => {
      first.current.setReviewedPublish(true);
      second.current.setReviewedPublish(true);
    });
    await act(async () => {
      await first.current.unlockPublish();
    });
    expect(protections.get("A").uncertain).toBe(false);
    expect(protections.get("A").revision).toBe(2);
    await act(async () => {
      await second.current.unlockPublish();
    });
    expect(second.current.status.message).toContain("重新核对结果并勾选确认");
    expect(protections.get("A").revision).toBe(2);
  });

  it("无法使用跨标签页锁时停止发布", async () => {
    const [first] = await pair();
    await prepare(first);
    Object.defineProperty(navigator, "locks", {
      configurable: true,
      value: undefined,
    });
    await act(async () => {
      await first.current.confirmPublish(async () => identity);
    });
    expect(first.current.publishStatus.message).toContain(
      "无法协调多个标签页的发布",
    );
    expect(fetch.mock.calls.some(([path]) => path === "/api/v1/publish")).toBe(
      false,
    );
  });
});

describe("历史发布内容比较", () => {
  const body = { title: "分享 <生活>", content: "正文", images: ["one.png", "two.png"], tags: [], visibility: "公开可见" };
  it("忽略对象键顺序和 JSON 转义，并兼容旧的空默认值", () => {
    expect(samePublishedContent(body, '{"images":["one.png","two.png"],"content":"正文","title":"分享 \\u003c生活\\u003e","tags":null,"visibility":""}')).toBe(true);
  });
  it("图片和话题数组顺序变化仍是不同的发布内容", () => {
    expect(samePublishedContent(body, JSON.stringify({ ...body, images: ["two.png", "one.png"] }))).toBe(false);
    expect(samePublishedContent({ ...body, tags: ["早晨", "生活"] }, JSON.stringify({ ...body, tags: ["生活", "早晨"] }))).toBe(false);
  });
});
