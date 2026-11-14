import { act, fireEvent, render as renderUI, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Tasks from "./Tasks.jsx";
import { NotificationHost } from "./notifications.jsx";

function TaskNotifications({ children }) {
  return <>{children}<NotificationHost /></>;
}

const render = (ui) => renderUI(ui, { wrapper: TaskNotifications });

const task = (changes = {}) => ({
  id: "a".repeat(32), account_id: "A", account_name: "创作账号A", title: "早餐记录",
  kind: "publish", status: "running", message: "正在提交笔记",
  created_at: "2026-11-14T08:00:00Z", started_at: "2026-11-14T08:01:00Z",
  updated_at: "2026-11-14T08:01:00Z", finished_at: null, ...changes,
});
const list = (tasks = [], total = tasks.length, page = 1) => ({ tasks, total, page, page_size: 20 });
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
};

describe("全账号任务页", () => {
  it("无需选择账号即可加载空态，刷新读取真实记录", async () => {
    const api = vi.fn().mockResolvedValueOnce(list()).mockResolvedValue(list([task()]));
    render(<Tasks api={api} token="token-A" active />);
    expect(screen.getByRole("button", { name: "刷新任务" })).toBeDisabled();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(await screen.findByText("还没有发布任务")).toBeVisible();
    expect(api.mock.calls[0]).toEqual([
      "/tasks?page=1&page_size=20", expect.objectContaining({ token: "token-A", signal: expect.any(AbortSignal) }),
    ]);
    expect(api.mock.calls[0][1]).not.toHaveProperty("accountId");
    fireEvent.click(screen.getByRole("button", { name: "刷新任务" }));
    expect(await screen.findByRole("article", { name: "发布任务：早餐记录" })).toHaveTextContent("执行中");
    expect(screen.queryByText("还没有发布任务")).not.toBeInTheDocument();
  });

  it("错误显示真实原因且可刷新恢复，不把读取失败显示为空态", async () => {
    const api = vi.fn().mockRejectedValueOnce(new Error("服务暂时不可用")).mockResolvedValue(list());
    render(<Tasks api={api} token="" active />);
    expect(await screen.findByRole("alert")).toHaveTextContent("服务暂时不可用");
    expect(screen.queryByText("还没有发布任务")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "刷新任务" }));
    expect(await screen.findByText("还没有发布任务")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("轮询显示状态、账号、种类和结果，失败与未知结果有明确反馈", async () => {
    vi.useFakeTimers();
    const api = vi.fn().mockResolvedValueOnce(list([task()])).mockResolvedValue(list([
      task({ status: "succeeded", message: "发布完成", finished_at: "2026-11-14T08:02:00Z", result: { status: "发布完成" } }),
      task({ id: "b".repeat(32), account_id: "B", account_name: "视频账号", title: "短片", kind: "publish_video", status: "failed", error: "视频文件不存在" }),
      task({ id: "c".repeat(32), title: "待核对笔记", status: "uncertain", message: "服务中断" }),
    ]));
    render(<Tasks api={api} token="" active />);
    await act(async () => {});
    expect(screen.getByText("执行中")).toBeVisible();
    expect(screen.getByText("创作账号A · 图文")).toBeVisible();
    await act(async () => vi.advanceTimersByTimeAsync(3000));
    expect(screen.getByText("已完成")).toBeVisible();
    expect(screen.getByText("视频账号 · 视频")).toBeVisible();
    expect(screen.getByText("失败原因：视频文件不存在")).toBeVisible();
    expect(screen.getByText("结果未知")).toBeVisible();
    expect(screen.getByText(/请先在小红书账号核对实际发布结果/)).toBeVisible();
    fireEvent.click(screen.getByText("查看发布结果"));
    expect(screen.getByText(/"status": "发布完成"/)).toBeVisible();
    expect(screen.queryByRole("link", { name: /小红书/ })).not.toBeInTheDocument();
  });

  it("分页读取下一页，切换令牌时回到第一页并忽略旧令牌响应", async () => {
    const old = deferred();
    const api = vi.fn((path, options) => {
      if (options.token === "new-token") return Promise.resolve(list([task({ title: "新服务任务" })]));
      if (path.includes("page=2")) return old.promise;
      return Promise.resolve(list([task()], 21));
    });
    const view = render(<Tasks api={api} token="old-token" active />);
    await screen.findByText("早餐记录");
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.mock.calls.some(([path]) => path.includes("page=2"))).toBe(true));
    const oldSignal = api.mock.calls.find(([path]) => path.includes("page=2"))[1].signal;
    view.rerender(<Tasks api={api} token="new-token" active />);
    expect(await screen.findByText("新服务任务")).toBeVisible();
    expect(oldSignal.aborted).toBe(true);
    await act(async () => old.resolve(list([task({ title: "旧令牌迟到任务" })], 21, 2)));
    expect(screen.queryByText("旧令牌迟到任务")).not.toBeInTheDocument();
    expect(screen.getByText("第 1 / 1 页 · 共 1 项")).toBeVisible();
    expect(api.mock.calls.at(-1)[0]).toBe("/tasks?page=1&page_size=20");
  });

  it("隐藏页面取消请求与轮询，恢复时重新读取并丢弃被取消的响应", async () => {
    vi.useFakeTimers();
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    const late = deferred();
    const api = vi.fn().mockReturnValueOnce(late.promise).mockResolvedValue(list([task({ title: "恢复后的任务" })]));
    render(<Tasks api={api} token="" active />);
    const signal = api.mock.calls[0][1].signal;
    act(() => {
      hidden.mockReturnValue(true);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(signal.aborted).toBe(true);
    await act(async () => vi.advanceTimersByTimeAsync(9000));
    expect(api).toHaveBeenCalledTimes(1);
    await act(async () => {
      hidden.mockReturnValue(false);
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(screen.getByText("恢复后的任务")).toBeVisible();
    await act(async () => late.resolve(list([task({ title: "取消请求的旧任务" })])));
    expect(screen.queryByText("取消请求的旧任务")).not.toBeInTheDocument();
    hidden.mockRestore();
  });
});
