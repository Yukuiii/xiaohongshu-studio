import { act, fireEvent, render as renderUI, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Schedules from "./Schedules.jsx";
import { NotificationHost } from "./notifications.jsx";

const accounts = [{ id: "A", name: "账号A", user_id: "user-A" }, { id: "B", name: "账号B", user_id: "user-B" }];
const job = (changes = {}) => ({
  id: "s1", name: "早餐计划", account_id: "A", account_name: "账号A", keyword: "早餐",
  topic: "简单早餐", instructions: "给出做法", cron: "30 8 * * 1", timezone: "Asia/Shanghai",
  reference_count: 20, enabled: true, publication_type: "image", next_run_at: "2026-11-16T00:30:00Z", ...changes,
});
const run = (changes = {}) => ({
  id: "r1", schedule_id: "s1", account_id: "A", account_name: "账号A", status: "succeeded", stage: "finished",
  title: "本次早餐", content: "完整的正文", tags: ["早餐"], sources: [], scheduled_at: "2026-11-14T00:30:00Z", ...changes,
});
const runs = (items = [], total = items.length) => ({ runs: items, total, page: 1, page_size: 20 });
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
};
function Notifications({ children }) { return <>{children}<NotificationHost /></>; }
const render = (ui) => renderUI(ui, { wrapper: Notifications });
const change = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });


describe("定时任务用户操作", () => {
  it("配置每周计划直接保存自动发布，并默认读取20篇参考", async () => {
    const api = vi.fn(async (path, options) => options.method === "POST" ? { schedule: job(options.body) } : { schedules: [] });
    render(<Schedules api={api} token="token" active accounts={accounts} />);
    await screen.findByText("还没有定时任务");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "新增定时任务" }));
    change("任务名称", "周五早餐");
    change("发布账号", "B");
    change("搜索关键词", "早餐食谱");
    change("创作选题", "快手早餐");
    change("执行频率", "weekly");
    change("星期", "5");
    change("执行时间", "07:45");
    change("时区", "Asia/Tokyo");
    fireEvent.click(screen.getByRole("button", { name: "保存并自动发布" }));
    await waitFor(() => expect(api.mock.calls.find(([, options]) => options.method === "POST")?.[1].body).toEqual({
      name: "周五早餐", account_id: "B", keyword: "早餐食谱", topic: "快手早餐", instructions: "",
      cron: "45 7 * * 5", timezone: "Asia/Tokyo", reference_count: 20, enabled: true, publication_type: "image",
    }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(api.mock.calls.every(([path]) => path === "/schedules")).toBe(true);
  });

  it.each([
    ["5 6 * * *", "daily"],
    ["30 8 * * 1", "weekly"],
    ["0 9 31 * *", "monthly"],
    ["*/15 8-18 * * 1-5", "custom"],
  ])("编辑 %s 时识别频率且保持执行规则", async (cron, mode) => {
    const schedule = job({ cron });
    const api = vi.fn(async (path, options) => options.method === "PUT" ? { schedule: { ...schedule, ...options.body } } : { schedules: [schedule] });
    render(<Schedules api={api} token="token" active accounts={accounts} />);
    fireEvent.click(await screen.findByRole("button", { name: "编辑", exact: true }));
    expect(screen.getByLabelText("执行频率")).toHaveValue(mode);
    change("任务名称", "修改名称");
    fireEvent.click(screen.getByRole("button", { name: "保存并自动发布" }));
    await waitFor(() => expect(api.mock.calls.find(([, options]) => options.method === "PUT")?.[1].body.cron).toBe(cron));
  });

  it("未绑定账号只能保存暂停计划，绑定后无需当前登录状态即可启用", async () => {
    const api = vi.fn(async (path, options) => options.method === "POST" ? { schedule: job(options.body) } : { schedules: [] });
    render(<Schedules api={api} token="token" active accounts={[{ id: "A", name: "未绑定账号" }]} />);
    await screen.findByText("还没有定时任务");
    fireEvent.click(screen.getByRole("button", { name: "新增定时任务" }));
    change("任务名称", "暂停计划"); change("搜索关键词", "早餐"); change("创作选题", "早餐");
    expect(screen.getByRole("button", { name: "保存并自动发布" })).toBeDisabled();
    fireEvent.click(screen.getByLabelText("启用定时任务"));
    fireEvent.click(screen.getByRole("button", { name: "保存为暂停" }));
    await waitFor(() => expect(api.mock.calls.find(([, options]) => options.method === "POST")?.[1].body.enabled).toBe(false));
  });

  it("切换执行记录和分页时丢弃旧响应，保留完整正文与参考", async () => {
    const old = deferred();
    const fullText = "参考第一段\n参考第二段";
    const api = vi.fn(async (path) => {
      if (path === "/schedules") return { schedules: [job(), job({ id: "s2", name: "晚餐计划" })] };
      if (path.includes("s1/runs")) return old.promise;
      if (path.includes("page=2")) return runs([run({ id: "r2", title: "第二页记录" })], 21);
      return runs([run({ title: "晚餐记录", sources: [{ feed_id: "f1", title: "完整参考", content: fullText, author: "作者" }] })], 21);
    });
    render(<Schedules api={api} token="token" active accounts={accounts} />);
    const first = await screen.findByRole("article", { name: "定时任务：早餐计划" });
    fireEvent.click(within(first).getByRole("button", { name: "详情与执行记录" }));
    await waitFor(() => expect(api.mock.calls.some(([path]) => path.includes("s1/runs"))).toBe(true));
    fireEvent.click(within(screen.getByRole("article", { name: "定时任务：晚餐计划" })).getByRole("button", { name: "详情与执行记录" }));
    await screen.findByRole("article", { name: "执行记录：晚餐记录" });
    fireEvent.click(screen.getByText("完整参考（1 篇）"));
    expect(screen.getByText("参考第一段 参考第二段")).toHaveTextContent("参考第一段 参考第二段");
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await screen.findByRole("article", { name: "执行记录：第二页记录" });
    await act(async () => old.resolve(runs([run({ title: "不应出现的旧记录" })])));
    expect(screen.queryByText("不应出现的旧记录")).not.toBeInTheDocument();
    expect(screen.getByText("第 2 / 2 页 · 共 21 次")).toBeVisible();
  });

  it("切换令牌会清空表单及选择，旧保存响应不能影响新页面", async () => {
    const saving = deferred();
    const api = vi.fn(async (path, options) => {
      if (options.method === "PUT") return saving.promise;
      return { schedules: [job({ name: options.token === "old" ? "旧令牌任务" : "新令牌任务" })] };
    });
    const view = render(<Schedules api={api} token="old" active accounts={accounts} />);
    fireEvent.click(await screen.findByRole("button", { name: "编辑", exact: true }));
    fireEvent.click(screen.getByRole("button", { name: "保存并自动发布" }));
    const request = api.mock.calls.find(([, options]) => options.method === "PUT")[1];
    view.rerender(<Schedules api={api} token="new" active accounts={accounts} />);
    await screen.findByRole("article", { name: "定时任务：新令牌任务" });
    expect(request.signal.aborted).toBe(true);
    await act(async () => saving.resolve({ schedule: job() }));
    expect(screen.queryByRole("heading", { name: "编辑定时任务" })).not.toBeInTheDocument();
    expect(screen.queryByText("定时任务已保存，将按计划自动发布。")).not.toBeInTheDocument();
    expect(screen.queryByRole("article", { name: "定时任务：旧令牌任务" })).not.toBeInTheDocument();
  });

  it("离开页面停止轮询，返回重新读取并忽略已取消的旧请求", async () => {
    const old = deferred();
    const api = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValue({ schedules: [job({ name: "重新读取的任务" })] });
    const view = render(<Schedules api={api} token="token" active accounts={accounts} />);
    const signal = api.mock.calls[0][1].signal;
    view.rerender(<Schedules api={api} token="token" active={false} accounts={accounts} />);
    expect(signal.aborted).toBe(true);
    vi.useFakeTimers();
    await act(async () => vi.advanceTimersByTimeAsync(15000));
    expect(api).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
    view.rerender(<Schedules api={api} token="token" active accounts={accounts} />);
    await screen.findByRole("article", { name: "定时任务：重新读取的任务" });
    await act(async () => old.resolve({ schedules: [job({ name: "旧请求任务" })] }));
    expect(screen.queryByRole("article", { name: "定时任务：旧请求任务" })).not.toBeInTheDocument();
  });
  it("长文选择保存后编辑与暂停均保留发布类型", async () => {
    let schedule = job();
    const api = vi.fn(async (path, options) => {
      if (options.method === "PUT") {
        schedule = { ...schedule, ...options.body };
        return { schedule };
      }
      return { schedules: [schedule] };
    });
    render(<Schedules api={api} token="token" active accounts={accounts} />);
    fireEvent.click(await screen.findByRole("button", { name: "编辑", exact: true }));
    expect(screen.getByLabelText("发布类型")).toHaveValue("image");
    change("发布类型", "long_text");
    fireEvent.click(screen.getByRole("button", { name: "保存并自动发布" }));
    await waitFor(() => expect(screen.queryByLabelText("发布类型")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "编辑", exact: true }));
    expect(screen.getByLabelText("发布类型")).toHaveValue("long_text");
    fireEvent.click(screen.getByRole("button", { name: "取消编辑" }));
    fireEvent.click(screen.getByRole("button", { name: "暂停", exact: true }));
    await waitFor(() => expect(api.mock.calls.filter(([, options]) => options.method === "PUT").at(-1)[1].body).toMatchObject({ publication_type: "long_text", enabled: false }));
  });

  it.each(["failed", "skipped"])("展示配置问题并重试 %s 执行后刷新详情", async (status) => {
    let schedule = job({ enabled: false, last_run_id: "failed-run", last_run_status: status, last_run_error: "生图失败", configuration_error: "请配置生图模型", retry_allowed: true });
    const api = vi.fn(async (path) => {
      if (path.endsWith("/retry")) {
        schedule = { ...schedule, active_run_id: "new-run", last_run_id: "new-run", last_run_status: "running", retry_allowed: false };
        return { run: run({ id: "new-run", status: "running", retry_of_id: "failed-run" }) };
      }
      if (path.includes("/runs?")) return runs([run({ id: "new-run", title: "重试笔记", status: "running", stage: "imaging", retry_of_id: "failed-run" })]);
      return { schedules: [schedule] };
    });
    render(<Schedules api={api} token="token" active accounts={accounts} />);
    expect(await screen.findByText(/配置问题：请配置生图模型/)).toBeVisible();
    expect(screen.getByText("最近执行说明：生图失败")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "重试最近执行" }));
    const record = await screen.findByRole("article", { name: "执行记录：重试笔记" });
    expect(record).toHaveTextContent("生成配图");
    expect(record).toHaveTextContent("重试来源：failed-run");
    expect(api.mock.calls.find(([path]) => path.endsWith("/retry"))).toEqual(["/schedules/s1/retry", expect.objectContaining({ method: "POST", token: "token", body: { run_id: "failed-run" } })]);
    expect(screen.queryByRole("button", { name: "重试最近执行" })).not.toBeInTheDocument();
    expect(within(screen.getByRole("article", { name: "定时任务：早餐计划" })).getByRole("button", { name: "启用", exact: true })).toBeEnabled();
  });

  it.each([
    { last_run_status: "failed" },
    { last_run_status: "skipped", retry_allowed: false },
    { last_run_status: "uncertain", retry_allowed: true },
    { last_run_status: "succeeded", retry_allowed: true },
    { last_run_status: "failed", retry_allowed: true, active_run_id: "active" },
  ])("服务未允许或存在执行风险时不提供重试：%j", async (state) => {
    const api = vi.fn().mockResolvedValue({ schedules: [job({ last_run_id: "last", ...state })] });
    render(<Schedules api={api} token="token" active accounts={accounts} />);
    await screen.findByRole("article", { name: "定时任务：早餐计划" });
    expect(screen.queryByRole("button", { name: "重试最近执行" })).not.toBeInTheDocument();
  });

  it("历史图片按执行账号读取blob，关闭详情释放且不接受迟到图片", async () => {
    const late = deferred();
    const api = vi.fn(async (path) => {
      if (path === "/schedules") return { schedules: [job()] };
      if (path === "/images/image-1") return new Blob(["image"], { type: "image/png" });
      if (path === "/images/image-2") return late.promise;
      return runs([run({ account_id: "B", images: ["image-1", "image-2"] })]);
    });
    render(<Schedules api={api} token="private-token" active accounts={accounts} />);
    fireEvent.click(await screen.findByRole("button", { name: "详情与执行记录" }));
    const image = await screen.findByRole("img", { name: "本次生成图片 1" });
    expect(image).toHaveAttribute("src", "blob:preview");
    expect(api.mock.calls.find(([path]) => path === "/images/image-1")?.[1]).toMatchObject({ accountId: "B", token: "private-token", blob: true });
    const pending = api.mock.calls.find(([path]) => path === "/images/image-2")[1];
    fireEvent.click(screen.getByRole("button", { name: "关闭详情" }));
    expect(pending.signal.aborted).toBe(true);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview");
    await act(async () => late.resolve(new Blob(["late"], { type: "image/png" })));
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });
});
