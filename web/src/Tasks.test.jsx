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
      task({
        status: "succeeded",
        message: "发布完成",
        finished_at: "2026-11-14T08:02:00Z",
        result: { status: "发布完成", title: "早餐记录", content: "第一段\n\n第二段", images: 2 },
      }),
      task({ id: "b".repeat(32), account_id: "B", account_name: "视频账号", title: "短片", kind: "publish_video", status: "failed", error: "视频文件不存在" }),
      task({ id: "c".repeat(32), title: "待核对笔记", status: "uncertain", message: "服务中断" }),
    ]));
    render(<Tasks api={api} token="" active />);
    await act(async () => {});
    expect(screen.getByText("执行中")).toBeVisible();
    expect(screen.getByText("创作账号A · 图文")).toBeVisible();
    await act(async () => vi.advanceTimersByTimeAsync(3000));
    expect(screen.getByText("已完成")).toBeVisible();
    expect(screen.getByText("已完成").closest("article")?.querySelector(":scope > p")).toBeNull();
    expect(screen.getByText("视频账号 · 视频")).toBeVisible();
    expect(screen.getByText("失败原因：视频文件不存在")).toBeVisible();
    expect(screen.getByText("结果未知")).toBeVisible();
    expect(screen.getByText(/请先在小红书账号核对实际发布结果/)).toBeVisible();
    const summary = screen.getByText("查看发布结果");
    fireEvent.click(summary);
    const details = summary.closest("details");
    expect(details).toHaveTextContent("发布状态");
    expect(details).toHaveTextContent("2 张");
    expect(details).toHaveTextContent("第一段 第二段");
    expect(details).not.toHaveTextContent(/"status":/);
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

  it("展开图文发布结果时按账号读取配图并在收起时释放BlobURL", async () => {
    const createURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:task-image");
    const revokeURL = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    try {
      const api = vi.fn(async (path) => {
        if (path.startsWith("/images/")) return new Blob(["img"], { type: "image/png" });
        return list([
          task({
            status: "succeeded",
            account_id: "acc-img",
            image_ids: ["0123456789abcdef0123456789abcdef.png"],
            result: { status: "发布完成", title: "早餐记录", images: 1 },
          }),
        ]);
      });
      render(<Tasks api={api} token="secret-token" active />);
      await screen.findByRole("heading", { name: "早餐记录" });
      expect(screen.getByText("账号编码：acc-img")).toBeVisible();
      expect(api.mock.calls.some(([path]) => path.startsWith("/images/"))).toBe(false);

      const summary = screen.getByText("查看发布结果");
      fireEvent.click(summary);
      const img = await screen.findByRole("img", { name: "任务配图 1" });
      expect(img).toHaveAttribute("src", "blob:task-image");
      expect(summary.closest("details")).not.toHaveTextContent("账号编号");
      expect(api.mock.calls.find(([path]) => path.startsWith("/images/"))).toEqual([
        "/images/0123456789abcdef0123456789abcdef.png",
        expect.objectContaining({ accountId: "acc-img", token: "secret-token", blob: true }),
      ]);

      fireEvent.click(screen.getByRole("button", { name: "查看大图：任务配图 1" }));
      const dialog = screen.getByRole("dialog", { name: "任务配图 1" });
      expect(dialog).toHaveAttribute("open");
      const fullImg = dialog.querySelector("img");
      expect(fullImg).toHaveAttribute("src", "blob:task-image");
      fireEvent.click(fullImg);
      expect(dialog).toHaveAttribute("open");
      fireEvent.click(dialog);
      expect(dialog).not.toHaveAttribute("open");

      fireEvent.click(summary);
      await waitFor(() => expect(revokeURL).toHaveBeenCalledWith("blob:task-image"));
    } finally {
      createURL.mockRestore();
      revokeURL.mockRestore();
    }
  });
  it.each([false, true])("发布结果打开官方笔记并反馈复制结果，失败=%s", async (fails) => {
    const url = "https://www.xiaohongshu.com/explore/abc123?xsec_token=token";
    const writeText = vi.fn(() => fails ? Promise.reject(new Error("denied")) : Promise.resolve());
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    try {
      const api = vi.fn().mockResolvedValue(list([task({ status: "succeeded", result: { note_id: "abc123", note_url: url } })]));
      render(<Tasks api={api} token="token" active />);
      fireEvent.click(await screen.findByText("查看发布结果"));
      expect(screen.getByRole("link", { name: "打开笔记" })).toHaveAttribute("href", url);
      expect(screen.getByRole("link", { name: "打开笔记" })).toHaveAttribute("target", "_blank");
      await waitFor(() => expect(screen.getByRole("button", { name: "复制链接" })).toBeEnabled());
      fireEvent.click(screen.getByRole("button", { name: "复制链接" }));
      expect(writeText).toHaveBeenCalledWith(url);
      expect(await screen.findByRole(fails ? "alert" : "status")).toBeVisible();
      expect(screen.getByText("已完成")).toBeVisible();
    } finally { vi.unstubAllGlobals(); }
  });

  it.each([
    {},
    { note_url: "", note_link_error: "发布页未返回笔记地址" },
    { note_url: "javascript:alert(1)" },
    { note_url: "data:text/html,hello" },
    { note_url: "https://www.xiaohongshu.com.evil.test/explore/abc123" },
    { note_url: "http://www.xiaohongshu.com/explore/abc123" },
    { note_url: "https://www.xiaohongshu.com/user/profile/abc123" },
    { note_url: "https://www.xiaohongshu.com/explore/abc123", note_id: "other" },
  ])("历史、缺失或不安全链接不提供操作：%j", async (fields) => {
    const api = vi.fn().mockResolvedValue(list([task({ status: "succeeded", result: { content: "历史正文", ...fields } })]));
    render(<Tasks api={api} token="token" active />);
    fireEvent.click(await screen.findByText("查看发布结果"));
    expect(screen.getByText("历史正文")).toBeVisible();
    expect(screen.getByText("已完成")).toBeVisible();
    expect(screen.queryByRole("link", { name: "打开笔记" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "复制链接" })).not.toBeInTheDocument();
    if (fields.note_link_error) expect(screen.getByText(new RegExp(fields.note_link_error))).toBeVisible();
  });

  it("离开任务页后忽略迟到的复制结果", async () => {
    const copying = deferred();
    const writeText = vi.fn(() => copying.promise);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    try {
      const api = vi.fn().mockResolvedValue(list([task({ status: "succeeded", result: { note_url: "https://www.xiaohongshu.com/explore/abc123" } })]));
      const view = render(<Tasks api={api} token="token" active />);
      fireEvent.click(await screen.findByText("查看发布结果"));
      await waitFor(() => expect(screen.getByRole("button", { name: "复制链接" })).toBeEnabled());
      fireEvent.click(screen.getByRole("button", { name: "复制链接" }));
      expect(writeText).toHaveBeenCalledTimes(1);
      view.rerender(<Tasks api={api} token="token" active={false} />);
      await act(async () => copying.resolve());
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    } finally { vi.unstubAllGlobals(); }
  });
});
