import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Select from "./Select.jsx";

function ChoiceForm({ disabled = false, initial = "daily", onChange = () => {} }) {
  const [value, setValue] = useState(initial);
  return <form aria-label="计划表单"><fieldset disabled={disabled}>
    <label htmlFor="cadence">执行频率</label>
    <Select id="cadence" name="cadence" value={value} required onChange={(event) => {
      setValue(event.target.value);
      onChange(event.target.value);
    }}>
      <option value="">请选择频率</option>
      <option value="daily">每天</option>
      <option value="unavailable" disabled>暂停使用的选项</option>
      <option value="weekly">每周</option>
      <option value="monthly">每月</option>
    </Select>
    <label htmlFor="topic">选题</label><input id="topic" />
  </fieldset></form>;
}

function activeOption(control) {
  return document.getElementById(control.getAttribute("aria-activedescendant"));
}

describe("统一下拉选择", () => {
  it("键盘跳过禁用项，Escape取消，Enter提交真正的表单值", () => {
    const changed = vi.fn();
    render(<ChoiceForm onChange={changed} />);
    const control = screen.getByRole("combobox", { name: "执行频率" });
    control.focus();
    fireEvent.keyDown(control, { key: "ArrowDown" });
    fireEvent.keyDown(control, { key: "ArrowDown" });
    expect(activeOption(control)).toHaveTextContent("每周");
    expect(changed).not.toHaveBeenCalled();
    fireEvent.keyDown(control, { key: "Escape" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(control).toHaveFocus();
    expect(control).toHaveTextContent("每天");
    fireEvent.keyDown(control, { key: "End" });
    expect(activeOption(control)).toHaveTextContent("每月");
    fireEvent.keyDown(control, { key: "Enter" });
    expect(changed).toHaveBeenCalledExactlyOnceWith("monthly");
    expect(control).toHaveTextContent("每月");
    expect(new FormData(screen.getByRole("form", { name: "计划表单" })).get("cadence")).toBe("monthly");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("指针选中项、点击外部和Tab关闭不会额外改变值", () => {
    const changed = vi.fn();
    render(<ChoiceForm onChange={changed} />);
    const control = screen.getByRole("combobox", { name: "执行频率" });
    fireEvent.click(control);
    fireEvent.click(screen.getByRole("option", { name: "暂停使用的选项" }));
    expect(changed).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("option", { name: "每周" }));
    expect(changed).toHaveBeenCalledExactlyOnceWith("weekly");
    fireEvent.click(control);
    fireEvent.pointerDown(screen.getByLabelText("选题"));
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    fireEvent.click(control);
    fireEvent.keyDown(control, { key: "Tab" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("父fieldset禁用时不能选择，并保留required校验", () => {
    const changed = vi.fn();
    const { rerender } = render(<ChoiceForm initial="" onChange={changed} />);
    const form = screen.getByRole("form", { name: "计划表单" });
    const control = screen.getByRole("combobox", { name: "执行频率" });
    fireEvent.invalid(document.querySelector('select[name="cadence"]'));
    expect(control).toHaveFocus();
    expect(control).toHaveAttribute("aria-invalid", "true");
    fireEvent.click(screen.getByRole("option", { name: "每天" }));
    expect(form.checkValidity()).toBe(true);
    fireEvent.click(control);
    rerender(<ChoiceForm disabled onChange={changed} />);
    expect(control).toBeDisabled();
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    fireEvent.click(control);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(changed).toHaveBeenCalledExactlyOnceWith("daily");
  });
});
