import { useEffect, useRef, useState } from "react";
import { Dialog } from "./components.jsx";
import { ToastNotice } from "./notifications.jsx";
import "./prompts.css";

const groups = [
  {
    id: "compose",
    kicker: "工作台写稿",
    title: "手动创作",
    description: "用于「寻找灵感 → 撰写文案」:根据选题、要求和 1–5 篇参考正文生成草稿。",
    items: [
      {
        key: "compose_system",
        name: "系统提示词",
        hint: "定义角色、创作规则、篇幅和输出 JSON 结构。",
      },
      {
        key: "compose_user",
        name: "用户提示词",
        hint: "把选题、补充要求和参考资料以 JSON 传给模型。",
      },
    ],
  },
  {
    id: "schedule",
    kicker: "定时创作",
    title: "定时任务",
    description: "用于定时任务:围绕关键词自主选题,并参考同账号近期文章避开重复内容。",
    items: [
      {
        key: "schedule_system",
        name: "系统提示词",
        hint: "定义自主选题、避重、创作规则、篇幅和输出 JSON 结构。",
      },
      {
        key: "schedule_user",
        name: "用户提示词",
        hint: "把关键词、创作要求和近期文章摘要以 JSON 传给模型。",
      },
    ],
  },
];

const allItems = groups.flatMap((group) =>
  group.items.map((item) => ({ ...item, group })),
);

const labels = Object.fromEntries(allItems.map((item) => [item.key, item.name]));

const formatTime = (value) => {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? ""
    : date.toLocaleString("zh-CN", {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      });
};

export default function Prompts({ api, token, active = true }) {
  const [templates, setTemplates] = useState({});
  const [drafts, setDrafts] = useState({});
  const [state, setState] = useState({ loading: true, error: "", token: "" });
  const [reload, setReload] = useState(0);
  const [saving, setSaving] = useState("");
  const [resetKey, setResetKey] = useState("");
  const [activeKey, setActiveKey] = useState(allItems[0].key);
  const [notice, setNotice] = useState({ message: "", type: "" });
  const loadedToken = useRef(null);
  const latestToken = useRef(token);
  latestToken.current = token;

  useEffect(() => {
    if (!active || loadedToken.current === token) return;
    const controller = new AbortController();
    setState({ loading: true, error: "", token });
    api("/prompts", { token, signal: controller.signal })
      .then((data) => {
        if (controller.signal.aborted) return;
        const list = Array.isArray(data?.prompts) ? data.prompts : null;
        if (!list) throw new Error("提示词列表响应无效,请检查服务版本后重试。");
        const nextTemplates = {};
        const nextDrafts = {};
        for (const item of list) {
          if (
            typeof item?.key !== "string" ||
            typeof item?.content !== "string" ||
            typeof item?.default_content !== "string" ||
            !Array.isArray(item.variables)
          )
            throw new Error("提示词数据不完整,请刷新后重试。");
          nextTemplates[item.key] = item;
          nextDrafts[item.key] = item.content;
        }
        for (const item of allItems)
          if (!nextTemplates[item.key])
            throw new Error("提示词数据不完整,请刷新后重试。");
        loadedToken.current = token;
        setTemplates(nextTemplates);
        setDrafts(nextDrafts);
        setState({ loading: false, error: "", token });
      })
      .catch((error) => {
        if (error.name === "AbortError" || controller.signal.aborted) return;
        setState({ loading: false, error: error.message, token });
      });
    return () => controller.abort();
  }, [active, api, reload, token]);

  const current = allItems.find((item) => item.key === activeKey) || allItems[0];
  const group = current.group;
  const template = templates[current.key];
  const value = drafts[current.key] ?? "";
  const changed = Boolean(template && value !== template.content);
  const busy = saving === current.key;

  const isDirty = (key) => {
    const item = templates[key];
    return Boolean(item && drafts[key] !== undefined && drafts[key] !== item.content);
  };

  async function save(item) {
    if (saving) return;
    const scope = token;
    const content = drafts[item.key] ?? "";
    setSaving(item.key);
    setNotice({ message: `正在保存${labels[item.key]}…`, type: "busy" });
    try {
      const data = await api(`/prompts/${encodeURIComponent(item.key)}`, {
        method: "PUT",
        token: scope,
        body: { content },
      });
      if (latestToken.current !== scope) return;
      const saved = data?.prompt;
      if (
        typeof saved?.key !== "string" ||
        saved.key !== item.key ||
        typeof saved.content !== "string"
      )
        throw new Error("服务未返回保存后的提示词,请刷新后核对。");
      setTemplates((previous) => ({ ...previous, [item.key]: saved }));
      setDrafts((previous) => ({ ...previous, [item.key]: saved.content }));
      setNotice({ message: `${labels[item.key]}已保存,下一次创作生效。`, type: "" });
    } catch (error) {
      if (latestToken.current === scope)
        setNotice({ message: error.message, type: "error" });
    } finally {
      if (latestToken.current === scope) setSaving("");
    }
  }

  async function reset() {
    if (!resetKey || saving) return;
    const scope = token;
    const key = resetKey;
    setSaving(key);
    setNotice({ message: "正在恢复默认提示词…", type: "busy" });
    try {
      const data = await api(`/prompts/${encodeURIComponent(key)}`, {
        method: "DELETE",
        token: scope,
      });
      if (latestToken.current !== scope) return;
      const restored = data?.prompt;
      if (typeof restored?.content !== "string" || restored.key !== key)
        throw new Error("服务未返回默认提示词,请刷新后核对。");
      setTemplates((previous) => ({ ...previous, [key]: restored }));
      setDrafts((previous) => ({ ...previous, [key]: restored.content }));
      setResetKey("");
      setNotice({ message: `${labels[key]}已恢复内置默认。`, type: "" });
    } catch (error) {
      if (latestToken.current === scope)
        setNotice({ message: error.message, type: "error" });
    } finally {
      if (latestToken.current === scope) setSaving("");
    }
  }

  const ready = state.token === token && !state.loading && !state.error;
  const resetLabel = resetKey ? labels[resetKey] : "提示词";
  const updated = formatTime(template?.updated_at);

  return (
    <div className="prompts-page">
      <header className="intro">
        <div>
          <p className="eyebrow">让模型按你的方法写作</p>
          <h1 id="prompts-title" tabIndex={-1}>
            提示词
          </h1>
          <p className="muted">
            手动创作和定时创作使用以下模板;生图提示词保持内置,不在工作台开放修改。
          </p>
        </div>
      </header>

      <section className="card prompts-note">
        <strong>保留必需变量</strong>
        <p className="muted">
          每段模板必须包含卡片中列出的全部变量,变量会在发送前替换为本次创作的数据。
          模板按原样发送给模型,保存后立即用于下一次创作。
        </p>
      </section>

      {!ready ? (
        <section className="card prompts-state" aria-live="polite">
          {state.error && state.token === token && !state.loading ? (
            <>
              <p className="prompts-error">读取提示词失败:{state.error}</p>
              <button
                type="button"
                className="button"
                onClick={() => setReload((value) => value + 1)}
              >
                重试
              </button>
            </>
          ) : (
            <p className="muted">正在读取提示词…</p>
          )}
        </section>
      ) : (
        <section className="prompt-workbench" aria-label="提示词编辑">
          <div className="prompt-workflow-tabs" role="tablist" aria-label="创作类型">
            {groups.map((item) => {
              const dirty = item.items.some((entry) => isDirty(entry.key));
              return (
                <button
                  type="button"
                  role="tab"
                  key={item.id}
                  className="prompt-workflow-tab"
                  aria-selected={item.id === group.id}
                  onClick={() => setActiveKey(item.items[0].key)}
                >
                  <span className="prompt-workflow-kicker">{item.kicker}</span>
                  <span className="prompt-workflow-title">{item.title}</span>
                  {dirty && <span className="prompt-dirty-dot" aria-label="有未保存修改" />}
                </button>
              );
            })}
          </div>
          <p className="prompt-group-description muted">{group.description}</p>

          <article className="card prompt-panel" aria-label={`${group.title}${current.name}`}>
            <div className="prompt-panel-heading">
              <div>
                <span className="section-kicker">{group.kicker}</span>
                <h2>{current.name}</h2>
                <p className="muted">{current.hint}</p>
              </div>
              <span
                className={`prompt-badge${template.customized ? " custom" : ""}`}
              >
                {template.customized ? "已自定义" : "内置默认"}
              </span>
            </div>

            <div className="prompt-panel-toolbar">
              <div
                className="prompt-template-tabs"
                role="tablist"
                aria-label={`${group.title}模板`}
              >
                {group.items.map((item) => (
                  <button
                    type="button"
                    role="tab"
                    key={item.key}
                    aria-selected={item.key === current.key}
                    onClick={() => setActiveKey(item.key)}
                  >
                    {item.name}
                    {isDirty(item.key) && (
                      <span className="prompt-dirty-dot" aria-label="有未保存修改" />
                    )}
                  </button>
                ))}
              </div>
              <div className="prompt-variables" aria-label="必需变量">
                {template.variables.map((name) => (
                  <code key={name}>{`{{${name}}}`}</code>
                ))}
              </div>
            </div>

            <label className="prompt-editor-label" htmlFor={`prompt-${current.key}`}>
              模板内容
            </label>
            <textarea
              id={`prompt-${current.key}`}
              className="prompt-editor"
              value={value}
              rows={22}
              spellCheck={false}
              disabled={busy}
              onChange={(event) =>
                setDrafts((previous) => ({
                  ...previous,
                  [current.key]: event.target.value,
                }))
              }
            />
            <div className="prompt-panel-footer">
              <span className="prompt-state">
                {changed ? "有未保存修改" : updated ? `更新于 ${updated}` : "未修改"}
              </span>
              <div className="button-group">
                <button
                  type="button"
                  className="button quiet small"
                  disabled={Boolean(saving) || !template.customized}
                  onClick={() => setResetKey(current.key)}
                >
                  恢复默认
                </button>
                <button
                  type="button"
                  className="button primary small"
                  disabled={Boolean(saving) || !changed}
                  onClick={() => save(current)}
                >
                  {busy ? "正在保存…" : "保存"}
                </button>
              </div>
            </div>
          </article>
        </section>
      )}

      <ToastNotice active={active} notice={notice} />
      <Dialog
        open={active && Boolean(resetKey)}
        onClose={() => setResetKey("")}
        busy={Boolean(saving)}
        id="reset-prompt"
        title="恢复默认提示词？"
      >
        <p className="muted">
          将删除「{resetLabel}」的自定义内容,恢复为程序内置模板。此操作不可撤销。
        </p>
        <div className="dialog-actions">
          <button
            type="button"
            className="button"
            disabled={Boolean(saving)}
            onClick={() => setResetKey("")}
          >
            取消
          </button>
          <button
            type="button"
            className="button primary"
            disabled={Boolean(saving)}
            onClick={reset}
          >
            确认恢复
          </button>
        </div>
      </Dialog>
    </div>
  );
}