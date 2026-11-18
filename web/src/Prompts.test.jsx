import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Prompts from "./Prompts.jsx";

const initial = [
  {
    key: "compose_system",
    content: "默认手动系统 {{publication_type}} {{title_limit}} {{content_limit}}",
    default_content: "默认手动系统 {{publication_type}} {{title_limit}} {{content_limit}}",
    customized: false,
    variables: ["publication_type", "title_limit", "content_limit"],
  },
  {
    key: "compose_user",
    content: "默认手动用户 {{topic}} {{instructions}} {{sources}}",
    default_content: "默认手动用户 {{topic}} {{instructions}} {{sources}}",
    customized: false,
    variables: ["topic", "instructions", "sources"],
  },
  {
    key: "schedule_system",
    content: "默认定时系统 {{publication_type}} {{title_limit}} {{content_limit}}",
    default_content: "默认定时系统 {{publication_type}} {{title_limit}} {{content_limit}}",
    customized: false,
    variables: ["publication_type", "title_limit", "content_limit"],
  },
  {
    key: "schedule_user",
    content: "默认定时用户 {{keyword}} {{instructions}} {{recent_notes}}",
    default_content: "默认定时用户 {{keyword}} {{instructions}} {{recent_notes}}",
    customized: false,
    variables: ["keyword", "instructions", "recent_notes"],
  },
];

const saveContent = "自定义系统 {{publication_type}} / {{title_limit}} / {{content_limit}}";

describe("提示词编辑页", () => {
  it("加载模板并保存单个提示词", async () => {
    const api = vi.fn(async (path, options = {}) => {
      if (path === "/prompts") return { prompts: initial };
      if (path === "/prompts/compose_system" && options.method === "PUT")
        return {
          prompt: {
            ...initial[0],
            content: options.body.content,
            customized: true,
            updated_at: "2026-11-19T12:00:00Z",
          },
        };
      throw new Error(`unexpected request: ${path}`);
    });
    render(<Prompts api={api} token="token" active />);
    const editor = await screen.findByDisplayValue(initial[0].content);
    fireEvent.change(editor, { target: { value: saveContent } });
    const card = screen.getByRole("article", { name: "手动创作系统提示词" });
    expect(within(card).getByText("有未保存修改")).toBeVisible();
    fireEvent.click(within(card).getByRole("button", { name: "保存" }));
    await waitFor(() =>
      expect(api).toHaveBeenCalledWith(
        "/prompts/compose_system",
        expect.objectContaining({
          method: "PUT",
          token: "token",
          body: { content: saveContent },
        }),
      ),
    );
    await waitFor(() => expect(within(card).getByText("已自定义")).toBeVisible());
    expect(within(card).getByText("模板内容")).toBeVisible();
  });

  it("恢复默认先确认,确认后使用服务端默认内容", async () => {
    const customized = initial.map((item) =>
      item.key === "compose_system"
        ? {
            ...item,
            content: saveContent,
            customized: true,
            updated_at: "2026-11-19T12:00:00Z",
          }
        : item,
    );
    const api = vi.fn(async (path, options = {}) => {
      if (path === "/prompts") return { prompts: customized };
      if (path === "/prompts/compose_system" && options.method === "DELETE")
        return { prompt: initial[0] };
      throw new Error(`unexpected request: ${path}`);
    });
    render(<Prompts api={api} token="token" active />);
    const card = await screen.findByRole("article", {
      name: "手动创作系统提示词",
    });
    fireEvent.click(within(card).getByRole("button", { name: "恢复默认" }));
    const dialog = await screen.findByRole("dialog", {
      name: "恢复默认提示词？",
    });
    expect(within(dialog).getByText(/不可撤销/)).toBeVisible();
    expect(api).not.toHaveBeenCalledWith(
      "/prompts/compose_system",
      expect.objectContaining({ method: "DELETE" }),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "确认恢复" }));
    await waitFor(() =>
      expect(api).toHaveBeenCalledWith(
        "/prompts/compose_system",
        expect.objectContaining({ method: "DELETE", token: "token" }),
      ),
    );
    await waitFor(() =>
      expect(within(card).getByText("内置默认")).toBeVisible(),
    );
    expect(within(card).getByDisplayValue(initial[0].content)).toBeVisible();
  });

  it("读取失败后保留错误并可重试", async () => {
    let calls = 0;
    const api = vi.fn(async (path) => {
      if (path !== "/prompts") throw new Error(`unexpected request: ${path}`);
      calls += 1;
      if (calls === 1) throw new Error("网络不可用");
      return { prompts: initial };
    });
    render(<Prompts api={api} token="token" active />);
    expect(await screen.findByText(/读取提示词失败/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByDisplayValue(initial[0].content)).toBeVisible();
  });
});