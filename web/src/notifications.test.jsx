import { useState } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Dialog } from "./components.jsx";
import { NotificationHost, ToastNotice } from "./notifications.jsx";

function NotificationFixture({ notice }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>打开确认窗口</button>
      <ToastNotice notice={notice} />
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        id="notification-dialog"
        title="确认操作"
      >
        <p>请核对本次操作。</p>
      </Dialog>
      <NotificationHost />
    </>
  );
}

describe("操作吐司", () => {
  it("同一操作的后续失败替换仍显示的成功结果", async () => {
    const { rerender } = render(
      <NotificationFixture notice={{ message: "第一次保存成功", type: "" }} />,
    );
    expect(await screen.findByRole("status")).toHaveTextContent("第一次保存成功");

    rerender(
      <NotificationFixture
        notice={{ message: "第二次保存失败", type: "error" }}
      />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("第二次保存失败");
    expect(screen.queryByText("第一次保存成功")).not.toBeInTheDocument();
  });

  it("检查进度会更新为警告结果", async () => {
    const { rerender } = render(
      <NotificationFixture
        notice={{ message: "正在检查登录状态…", type: "busy" }}
      />,
    );
    expect(await screen.findByRole("status")).toHaveTextContent("正在检查登录状态…");
    expect(screen.queryByRole("button", { name: "关闭提示" })).not.toBeInTheDocument();

    rerender(
      <NotificationFixture
        notice={{ message: "当前账号尚未登录", type: "warning" }}
      />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("当前账号尚未登录");
    expect(screen.queryByText("正在检查登录状态…")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "关闭提示" })).not.toBeInTheDocument();
  });

  it("确认窗口打开和关闭时保留可见的错误反馈", async () => {
    const notice = { message: "操作失败，请重试", type: "error" };
    render(<NotificationFixture notice={notice} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(notice.message);

    fireEvent.click(screen.getByRole("button", { name: "打开确认窗口" }));
    const dialog = screen.getByRole("dialog", { name: "确认操作" });
    await waitFor(() =>
      expect(within(dialog).getByRole("alert")).toHaveTextContent(notice.message),
    );

    fireEvent.click(
      within(dialog).getByRole("button", { name: "关闭窗口" }),
    );
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(screen.getByRole("alert")).toBeVisible();
    });
  });
});
