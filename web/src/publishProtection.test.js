import { act, fireEvent, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useWorkbench } from "./useWorkbench.js";

const draftKey = "hongshu-workbench.draft.v1:A";
const protectionKey = "hongshu-workbench.publish.v1:A";
const initial = {
  title: "同一笔记",
  content: "正文",
  images: [{ id: "legacy", name: "配图", path: "/old/web/legacy.png" }],
  lastPublished: "原样保留的旧指纹",
};
const identity = {
  account_id: "A",
  user_id: "user-A",
  username: "作者A",
  is_logged_in: true,
};
const ok = (data) => ({
  ok: true,
  status: 200,
  json: async () => ({ success: true, data }),
});
const protection = () => JSON.parse(localStorage.getItem(protectionKey));
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
    vi.fn(async () => ({
      ok: true,
      blob: async () => new Blob(["pic"], { type: "image/png" }),
    })),
  );
});

async function pair(extra = {}) {
  localStorage.setItem(draftKey, JSON.stringify({ ...initial, ...extra }));
  const first = renderHook(useWorkbench);
  const second = renderHook(useWorkbench);
  act(() => {
    first.result.current.activateAccount("A");
    second.result.current.activateAccount("A");
  });
  await waitFor(() => {
    expect(first.result.current.busy).toBe(false);
    expect(second.result.current.busy).toBe(false);
  });
  return [first.result, second.result];
}

async function prepare(...results) {
  for (const result of results) {
    await act(async () => {
      await result.current.preparePublish(async () => identity);
    });
    act(() => {
      result.current.setPublishAgreed(true);
    });
  }
}

function storageEvent() {
  fireEvent(
    window,
    new StorageEvent("storage", {
      key: protectionKey,
      newValue: localStorage.getItem(protectionKey),
    }),
  );
}

describe("同账号跨标签页发布保护", () => {
  it.each(["unknown", "success"])(
    "%s 结果不能被另一旧页面的编辑、切换或 pagehide 保存覆盖",
    async (outcome) => {
      const [first, second] = await pair();
      await prepare(first, second);
      fetch.mockImplementationOnce(async () => {
        if (outcome === "unknown") throw new Error("offline");
        return ok({ status: "发布完成" });
      });
      await act(async () => {
        await first.current.confirmPublish(async () => identity);
      });
      const published = protection();
      expect(published.uncertain).toBe(outcome === "unknown");
      expect(published.lastPublished).toBe(
        outcome === "unknown"
          ? initial.lastPublished
          : JSON.stringify({
              title: initial.title,
              content: initial.content,
              images: [initial.images[0].path],
              tags: [],
              visibility: "公开可见",
            }),
      );
      act(() => {
        second.current.changeDraft("instructions", "旧页面继续编辑");
        second.current.flushDraft();
      });
      expect(protection()).toEqual(published);
      expect(JSON.parse(localStorage.getItem(draftKey))).toMatchObject({
        uncertain: published.uncertain,
        lastPublished: published.lastPublished,
        instructions: "旧页面继续编辑",
      });
      fireEvent(window, new Event("pagehide"));
      expect(protection()).toEqual(published);
      await act(async () => {
        await second.current.confirmPublish(async () => identity);
      });
      expect(second.current.publishStatus.message).toContain(
        outcome === "unknown" ? "上次发布结果尚未确认" : "已经发布成功",
      );
      expect(
        fetch.mock.calls.filter(([path]) => path.endsWith("/publish")),
      ).toHaveLength(1);
      act(() => {
        second.current.activateAccount("B");
      });
      expect(protection()).toEqual(published);
      expect(first.current.images[0].path).toBe(initial.images[0].path);
    },
  );

  it("同时发布时第二个页面立即被拒绝，其他页面也不能在发布中解除锁定", async () => {
    const [first, second] = await pair();
    await prepare(first, second);
    const late = deferred();
    fetch.mockImplementationOnce(() => late.promise);
    let publishing;
    act(() => {
      publishing = first.current.confirmPublish(async () => identity);
    });
    await waitFor(() => expect(first.current.uncertain).toBe(true));
    await act(async () => {
      await second.current.confirmPublish(async () => identity);
    });
    expect(second.current.publishStatus.message).toContain(
      "另一个标签页正在处理此账号的发布",
    );
    expect(
      fetch.mock.calls.filter(([path]) => path.endsWith("/publish")),
    ).toHaveLength(1);
    storageEvent();
    act(() => {
      second.current.setReviewedPublish(true);
    });
    await act(async () => {
      await second.current.unlockPublish();
    });
    expect(second.current.status.message).toContain(
      "另一个标签页正在处理此账号的发布",
    );
    expect(protection().uncertain).toBe(true);
    await act(async () => {
      late.resolve(ok({ status: "发布完成" }));
      await publishing;
    });
  });

  it("旧复核勾选不能解除更新的未知结果，重新核对后才协调解除且保留旧指纹", async () => {
    const [first, second] = await pair({ uncertain: true });
    act(() => {
      first.current.setReviewedPublish(true);
      second.current.setReviewedPublish(true);
    });
    await act(async () => {
      await first.current.unlockPublish();
    });
    expect(protection()).toMatchObject({
      uncertain: false,
      lastPublished: initial.lastPublished,
    });
    await prepare(first);
    fetch.mockRejectedValueOnce(new Error("offline"));
    await act(async () => {
      await first.current.confirmPublish(async () => identity);
    });
    const newer = protection();
    expect(newer.uncertain).toBe(true);
    await act(async () => {
      await second.current.unlockPublish();
    });
    expect(second.current.status.message).toContain("重新核对结果并勾选确认");
    expect(protection()).toEqual(newer);
    expect(second.current.reviewedPublish).toBe(false);
    act(() => {
      second.current.setReviewedPublish(true);
    });
    await act(async () => {
      await second.current.unlockPublish();
    });
    expect(protection()).toMatchObject({
      uncertain: false,
      lastPublished: initial.lastPublished,
    });
    act(() => {
      first.current.flushDraft();
    });
    expect(protection().uncertain).toBe(false);
    expect(first.current.uncertain).toBe(false);
  });

  it("storage 事件只更新保护和复核状态，保留当前选择与未保存的编辑内容", async () => {
    const [first, second] = await pair();
    act(() => {
      second.current.changeDraft("title", "本页未保存的修改");
    });
    await prepare(first);
    fetch.mockRejectedValueOnce(new Error("offline"));
    await act(async () => {
      await first.current.confirmPublish(async () => identity);
    });
    storageEvent();
    expect(second.current.uncertain).toBe(true);
    expect(second.current.draft.title).toBe("本页未保存的修改");
    expect(second.current.context.accountId).toBe("A");
    act(() => {
      second.current.setReviewedPublish(true);
      first.current.setReviewedPublish(true);
    });
    await act(async () => {
      await first.current.unlockPublish();
    });
    storageEvent();
    expect(second.current.uncertain).toBe(false);
    expect(second.current.reviewedPublish).toBe(false);
    expect(second.current.draft.title).toBe("本页未保存的修改");
    expect(second.current.context.accountId).toBe("A");
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
    expect(fetch.mock.calls.some(([path]) => path.endsWith("/publish"))).toBe(
      false,
    );
  });
});
