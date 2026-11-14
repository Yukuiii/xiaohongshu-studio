import { useEffect, useMemo, useRef, useState } from "react";
import { ToastNotice } from "./notifications.jsx";
import { useSchedules } from "./useSchedules.js";
import "./schedules.css";

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const statuses = { running: "执行中", publishing: "发布中", succeeded: "发布成功", failed: "执行失败", uncertain: "发布结果未知", skipped: "已跳过" };
const stages = { searching: "搜索参考", reading: "读取参考正文", composing: "撰写笔记", imaging: "生成配图", publishing: "发布笔记", finished: "已结束" };
const publicationLabel = (type) => type === "long_text" ? "长文" : "图文";
const canRetry = (schedule) => schedule.retry_allowed === true && Boolean(schedule.last_run_id) &&
  ["failed", "skipped"].includes(schedule.last_run_status) && !schedule.active_run_id;
const time = (value) => {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString("zh-CN", { hour12: false });
};
const defaultTimezone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai";

function cadence(cron = "0 9 * * *") {
  const fields = cron.trim().split(/\s+/);
  const [minute, hour, day, month, weekday] = fields;
  const result = { mode: "custom", time: "09:00", weekday: "1", day: "1", cron };
  if (fields.length !== 5 || !/^\d+$/.test(minute) || +minute > 59 || !/^\d+$/.test(hour) || +hour > 23 || month !== "*") return result;
  result.time = `${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
  if (day === "*" && weekday === "*") result.mode = "daily";
  else if (day === "*" && /^[0-7]$/.test(weekday)) {
    result.mode = "weekly";
    result.weekday = String(+weekday % 7);
  } else if (weekday === "*" && /^\d+$/.test(day) && +day >= 1 && +day <= 31) {
    result.mode = "monthly";
    result.day = String(+day);
  }
  return result;
}
function cronFor(form) {
  if (form.mode === "custom") return form.cron.trim();
  const [hour, minute] = form.time.split(":").map(Number);
  return `${minute} ${hour} ${form.mode === "monthly" ? form.day : "*"} * ${form.mode === "weekly" ? form.weekday : "*"}`;
}
function ruleLabel(cron) {
  const rule = cadence(cron);
  if (rule.mode === "daily") return `每天 ${rule.time}`;
  if (rule.mode === "weekly") return `每${weekdays[+rule.weekday]} ${rule.time}`;
  if (rule.mode === "monthly") return `每月 ${rule.day} 日 ${rule.time}`;
  return `Cron · ${cron}`;
}
function inputFor(schedule) {
  const { name, account_id, keyword, topic, instructions, cron, timezone, reference_count, enabled, publication_type = "image" } = schedule;
  return { name, account_id, keyword, topic, instructions, cron, timezone, reference_count, enabled, publication_type };
}

function ScheduleForm({ schedule, accounts, busy, onSave, onCancel }) {
  const [form, setForm] = useState(() => ({
    name: "", account_id: accounts[0]?.id || "", keyword: "", topic: "", instructions: "",
    timezone: defaultTimezone(), reference_count: 20, enabled: true, publication_type: "image",
    ...schedule, ...cadence(schedule?.cron),
  }));
  const heading = useRef(null);
  useEffect(() => heading.current?.focus(), []);
  const change = (key, value) => setForm((previous) => ({ ...previous, [key]: value }));
  const account = accounts.find((item) => item.id === form.account_id);
  const unbound = !account?.user_id;
  return (
    <section className="schedule-editor" aria-labelledby="schedule-editor-title">
      <h2 id="schedule-editor-title" tabIndex={-1} ref={heading}>{schedule ? "编辑定时任务" : "新增定时任务"}</h2>
      <form onSubmit={(event) => {
        event.preventDefault();
        onSave({ ...inputFor(form), name: form.name.trim(), keyword: form.keyword.trim(), topic: form.topic.trim(),
          timezone: form.timezone.trim(), cron: cronFor(form), reference_count: Number(form.reference_count) });
      }}>
        <fieldset disabled={Boolean(busy)} className="schedule-fields">
          <div className="schedule-form-grid">
            <div><label htmlFor="schedule-name">任务名称</label><input id="schedule-name" required value={form.name} onChange={(event) => change("name", event.target.value)} /></div>
            <div><label htmlFor="schedule-account">发布账号</label><select id="schedule-account" required value={form.account_id} onChange={(event) => change("account_id", event.target.value)}>
              <option value="">请选择账号</option>
              {schedule && !accounts.some((item) => item.id === form.account_id) && <option value={form.account_id}>{schedule.account_name || form.account_id}（不可用）</option>}
              {accounts.map((item) => <option key={item.id} value={item.id}>{item.name}{item.user_id ? "" : "（尚未登录绑定）"}</option>)}
            </select></div>
            <div><label htmlFor="schedule-keyword">搜索关键词</label><input id="schedule-keyword" required value={form.keyword} onChange={(event) => change("keyword", event.target.value)} /></div>
            <div><label htmlFor="schedule-topic">创作选题</label><input id="schedule-topic" required value={form.topic} onChange={(event) => change("topic", event.target.value)} /></div>
          </div>
          <label htmlFor="schedule-publication-type">发布类型</label>
          <select id="schedule-publication-type" value={form.publication_type} onChange={(event) => change("publication_type", event.target.value)}>
            <option value="image">图文 · 生成 1 张配图</option><option value="long_text">长文 · 原生文字排版</option>
          </select>
          <p className="helper">{form.publication_type === "long_text" ? "使用平台原生长文文字排版卡片，不调用生图模型。" : "使用设置中已保存的生图模型生成 1 张图片，再发布图文。缺少模型配置时仍可保存，请在执行前补全设置。"}</p>
          <label htmlFor="schedule-instructions">写作要求</label><textarea id="schedule-instructions" rows={4} placeholder="例如：面向新手，语气自然，给出具体步骤" value={form.instructions} onChange={(event) => change("instructions", event.target.value)} />
          <div className="schedule-form-grid">
            <div><label htmlFor="schedule-reference-count">参考数量</label><input id="schedule-reference-count" type="number" min="1" max="20" step="1" required value={form.reference_count} onChange={(event) => change("reference_count", event.target.value)} /></div>
            <div><label htmlFor="schedule-timezone">时区</label><input id="schedule-timezone" required placeholder="Asia/Shanghai" value={form.timezone} onChange={(event) => change("timezone", event.target.value)} /></div>
            <div><label htmlFor="schedule-mode">执行频率</label><select id="schedule-mode" value={form.mode} onChange={(event) => change("mode", event.target.value)}>
              <option value="daily">每天</option><option value="weekly">每周</option><option value="monthly">每月</option><option value="custom">自定义 Cron</option>
            </select></div>
            {form.mode !== "custom" && <div><label htmlFor="schedule-time">执行时间</label><input id="schedule-time" type="time" required value={form.time} onChange={(event) => change("time", event.target.value)} /></div>}
            {form.mode === "weekly" && <div><label htmlFor="schedule-weekday">星期</label><select id="schedule-weekday" value={form.weekday} onChange={(event) => change("weekday", event.target.value)}>{weekdays.map((day, index) => <option value={index} key={day}>{day}</option>)}</select></div>}
            {form.mode === "monthly" && <div><label htmlFor="schedule-day">每月日期</label><input id="schedule-day" type="number" min="1" max="31" step="1" required value={form.day} onChange={(event) => change("day", event.target.value)} /></div>}
          </div>
          {form.mode === "custom" && <><label htmlFor="schedule-cron">Cron 表达式</label><input id="schedule-cron" required value={form.cron} onChange={(event) => change("cron", event.target.value)} /><p className="helper">五个字段：分 时 日 月 星期。例如 0 9 * * 1-5 表示工作日 09:00。</p></>}
          {form.mode === "monthly" && <p className="helper">当月没有该日期时跳过该月。</p>}
          <p className="helper">按所选时区执行。服务离线时错过的时间不会补发；失败或结果未知时不会自动重试。</p>
          <label className="schedule-checkbox"><input type="checkbox" checked={form.enabled} onChange={(event) => change("enabled", event.target.checked)} />启用定时任务</label>
          {unbound && <p className="schedule-warning">此账号尚未登录绑定，可先关闭启用并保存，再到<a href="#accounts">账号页</a>完成登录。</p>}
          <p className="schedule-consent">启用后将按计划自动搜索参考、创作并发布，无需逐次审核。使用<a href="#settings">设置中已保存的模型</a>；{form.publication_type === "long_text" ? "长文使用原生文字排版，不调用生图模型。" : "图文自动生成 1 张配图。"}</p>
          <div className="button-group"><button type="submit" className="button primary" disabled={!account || (form.enabled && unbound)}>{busy === "save" ? "保存中…" : form.enabled ? "保存并自动发布" : "保存为暂停"}</button><button type="button" className="button" onClick={onCancel}>取消编辑</button></div>
        </fieldset>
      </form>
    </section>
  );
}

function RunImage({ id, api, token, accountId, alt }) {
  const [preview, setPreview] = useState({ url: "", error: "" });
  const latest = useRef(null);
  latest.current = { id, token, accountId };
  useEffect(() => {
    let disposed = false;
    let controller;
    let objectURL = "";
    const suspend = () => {
      controller?.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
      objectURL = "";
    };
    const load = async () => {
      suspend();
      if (disposed || document.hidden) return;
      const request = new AbortController();
      controller = request;
      const valid = () => !disposed && !request.signal.aborted && controller === request &&
        latest.current.id === id && latest.current.token === token && latest.current.accountId === accountId;
      setPreview({ url: "", error: "" });
      try {
        const blob = await api(`/images/${encodeURIComponent(id)}`, { token, accountId, blob: true, signal: request.signal });
        if (!valid()) return;
        if (!blob.type.startsWith("image/")) throw new Error("图片格式无法预览");
        objectURL = URL.createObjectURL(blob);
        setPreview({ url: objectURL, error: "" });
      } catch (error) {
        if (valid()) setPreview({ url: "", error: `图片读取失败：${error.message}` });
      }
    };
    const visibility = () => document.hidden ? suspend() : load();
    load();
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("pagehide", suspend);
    window.addEventListener("pageshow", load);
    return () => {
      disposed = true;
      suspend();
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("pagehide", suspend);
      window.removeEventListener("pageshow", load);
    };
  }, [api, id, token, accountId]);
  return <figure className="schedule-image">
    {preview.error ? <p className="schedule-error">{preview.error}</p> : preview.url
      ? <img src={preview.url} alt={alt} onError={() => setPreview({ url: "", error: "图片无法解码，请刷新重试。" })} />
      : <p className="muted">正在读取生成图片…</p>}
    <figcaption className="schedule-identifiers muted">图片编号：{id}</figcaption>
  </figure>;
}

function RunRecord({ run, api, token, active }) {
  return (
    <article className="schedule-run" aria-label={`执行记录：${run.title || time(run.scheduled_at)}`}>
      <div className="schedule-heading"><h3>{run.title || "尚未生成标题"}</h3><span className={`schedule-status ${run.status}`}>{statuses[run.status] || run.status}</span></div>
      <p className="muted">{run.account_name || run.account_id} · {publicationLabel(run.snapshot?.publication_type)} · {stages[run.stage] || run.stage}</p>
      <dl className="schedule-meta">
        <div><dt>计划时间</dt><dd>{time(run.scheduled_at)}</dd></div>
        <div><dt>开始时间</dt><dd>{time(run.started_at)}</dd></div>
        <div><dt>结束时间</dt><dd>{time(run.finished_at)}</dd></div>
      </dl>
      {run.content && <div className="schedule-prose" aria-label="笔记正文">{run.content}</div>}
      {run.tags?.length > 0 && <p className="schedule-tags" aria-label="笔记话题">{run.tags.map((tag) => `#${tag.replace(/^#/, "")}`).join(" ")}</p>}
      {run.image_prompt && <details className="schedule-reference"><summary>生图提示词</summary><p className="schedule-prose">{run.image_prompt}</p></details>}
      {run.images?.length > 0 && <div className="schedule-images" aria-label="本次生成图片">{active && run.images.map((id, index) => <RunImage key={id} id={id} api={api} token={token} accountId={run.account_id} alt={`本次生成图片 ${index + 1}`} />)}</div>}
      {run.retry_of_id && <p className="schedule-identifiers muted">重试来源：{run.retry_of_id}</p>}
      {run.error && <p className="schedule-error">执行说明：{run.error}</p>}
      {run.status === "uncertain" && <p className="schedule-warning">发布结果尚未确定，请在小红书账号核对实际结果。此条记录不会自动重试。</p>}
      {run.status === "succeeded" && <p>本次笔记已发布。</p>}
      {run.result != null && <pre className="schedule-prose">{typeof run.result === "string" ? run.result : JSON.stringify(run.result, null, 2)}</pre>}
      <details className="schedule-reference"><summary>完整参考（{run.sources?.length || 0} 篇）</summary>
        {run.sources?.length ? run.sources.map((source, index) => <article key={index} className="schedule-source">
          <h4>{source.title || `参考 ${index + 1}`}</h4>
          <div className="schedule-prose">{typeof source === "string" ? source : source.content || "暂无正文"}</div>
          {source.author && <p className="muted">作者：{source.author}</p>}
          {source.url && <p className="schedule-prose">来源：{source.url}</p>}
          {source.feed_id && <p className="schedule-identifiers muted">参考编号：{source.feed_id}</p>}
          {source.error && <p className="schedule-error">{source.error}</p>}
        </article>) : <p className="muted">暂无参考记录。</p>}
      </details>
      {run.snapshot && <details className="schedule-reference"><summary>本次执行配置</summary><dl className="schedule-meta">
        <div><dt>任务名称</dt><dd>{run.snapshot.name}</dd></div><div><dt>关键词</dt><dd>{run.snapshot.keyword}</dd></div><div><dt>选题</dt><dd>{run.snapshot.topic}</dd></div>
        <div><dt>参考数量</dt><dd>{run.snapshot.reference_count}</dd></div><div><dt>时间规则</dt><dd>{run.snapshot.cron} · {run.snapshot.timezone}</dd></div>
        <div><dt>发布类型</dt><dd>{publicationLabel(run.snapshot.publication_type)}</dd></div>
      </dl><p className="schedule-prose">{run.snapshot.instructions || "未填写额外写作要求"}</p></details>}
      <p className="schedule-identifiers muted">执行编号：{run.id}{run.publish_task_id && <><br />发布任务编号：{run.publish_task_id} · <a href="#tasks">查看发布任务</a></>}</p>
    </article>
  );
}

function SchedulePage({ api, token, active, accounts }) {
  const schedules = useSchedules({ api, token, active });
  const [editor, setEditor] = useState(null);
  const [deleting, setDeleting] = useState("");
  const selected = schedules.schedules.find((item) => item.id === schedules.selectedId);
  const errorNotice = useMemo(() => {
    const error = schedules.error || (schedules.selectedId && schedules.runsError);
    return error ? { message: `读取定时任务失败：${error}`, type: "error" } : null;
  }, [schedules.error, schedules.selectedId, schedules.runsError]);
  const save = async (input) => {
    const id = editor?.id;
    const saved = await schedules.mutate("save", id ? `/schedules/${encodeURIComponent(id)}` : "/schedules", { method: id ? "PUT" : "POST", body: input }, input.enabled ? "定时任务已保存，将按计划自动发布。" : "定时任务已保存为暂停。");
    if (saved) setEditor(null);
  };
  const remove = async (id) => {
    if (await schedules.mutate("delete", `/schedules/${encodeURIComponent(id)}`, { method: "DELETE" }, "定时任务已删除，历史执行记录仍保留。")) {
      setDeleting("");
      if (editor?.id === id) setEditor(null);
    }
  };
  const retry = async (schedule) => {
    if (!canRetry(schedule)) return;
    if (await schedules.mutate(`retry-${schedule.id}`, `/schedules/${encodeURIComponent(schedule.id)}/retry`, { method: "POST", body: { run_id: schedule.last_run_id } }, "重试已接收，将按当前任务配置自动创作并发布。原计划启停状态和下次时间不变。")) schedules.select(schedule.id);
  };
  return (
    <section className="card schedules-page" aria-busy={schedules.loading}>
      <div className="section-heading"><div><p className="eyebrow">让创作按时发生</p><h1 id="schedules-title" tabIndex={-1}>定时任务</h1></div>
        <div className="button-group"><button className="button" type="button" disabled={schedules.loading || Boolean(schedules.busy)} onClick={schedules.refresh}>刷新</button><button className="button primary" type="button" disabled={Boolean(schedules.busy)} onClick={() => { setEditor({}); setDeleting(""); }}>新增定时任务</button></div>
      </div>
      <p className="muted">按计划自动创作并发布，离开页面后由服务继续执行。暂停只停止后续计划，已开始的执行会继续。</p>
      <ToastNotice active={active} notice={errorNotice} /><ToastNotice active={active} notice={schedules.notice} />
      {!accounts.length && <p className="schedule-warning">请先到<a href="#accounts">账号页添加并登录账号</a>，并在<a href="#settings">设置页保存文字模型</a>。</p>}
      {editor && <ScheduleForm key={editor.id || "new"} schedule={editor.id ? editor : null} accounts={accounts} busy={schedules.busy} onSave={save} onCancel={() => setEditor(null)} />}
      {!schedules.loading && !schedules.error && !schedules.schedules.length && <div className="schedules-empty"><h2>还没有定时任务</h2><p className="muted">配置关键词、选题和时间，默认读取 20 篇完整参考后自动创作发布。</p><p><a href="#accounts">管理发布账号</a> · <a href="#settings">配置文字模型</a></p></div>}
      <div className="schedule-list">
        {schedules.schedules.map((schedule) => <article key={schedule.id} className={`schedule-item${schedules.selectedId === schedule.id ? " selected" : ""}`} aria-label={`定时任务：${schedule.name}`}>
          <div className="schedule-heading"><h2>{schedule.name}</h2><span className={`schedule-status ${schedule.enabled ? "enabled" : "paused"}`}>{schedule.enabled ? "已启用" : "已暂停"}</span></div>
          <p className="muted">{schedule.account_name || schedule.account_id} · {publicationLabel(schedule.publication_type)} · {ruleLabel(schedule.cron)} · {schedule.timezone}</p>
          <dl className="schedule-meta"><div><dt>下次执行</dt><dd>{schedule.enabled ? time(schedule.next_run_at) : "已暂停"}</dd></div><div><dt>上次执行</dt><dd>{time(schedule.last_run_at)}</dd></div></dl>
          {schedule.configuration_error && <p className="schedule-error">配置问题：{schedule.configuration_error} <a href="#settings">前往设置</a></p>}
          {schedule.last_run_status && <p className="muted">最近执行：{statuses[schedule.last_run_status] || schedule.last_run_status}</p>}
          {schedule.last_run_error && <p className="schedule-error">最近执行说明：{schedule.last_run_error}</p>}
          <div className="button-group">
            <button className="button" type="button" disabled={Boolean(schedules.busy)} aria-expanded={schedules.selectedId === schedule.id} aria-controls="schedule-detail" onClick={() => schedules.select(schedules.selectedId === schedule.id ? "" : schedule.id)}>详情与执行记录</button>
            <button className="button" type="button" disabled={Boolean(schedules.busy)} onClick={() => { setEditor(schedule); setDeleting(""); }}>编辑</button>
            <button className="button" type="button" disabled={Boolean(schedules.busy)} onClick={() => schedules.mutate(`toggle-${schedule.id}`, `/schedules/${encodeURIComponent(schedule.id)}`, { method: "PUT", body: { ...inputFor(schedule), enabled: !schedule.enabled } }, schedule.enabled ? "已暂停后续执行，已开始的任务会继续。" : "定时任务已启用，将按计划自动发布。")}>{schedule.enabled ? "暂停" : "启用"}</button>
            {canRetry(schedule) && <button className="button" type="button" disabled={Boolean(schedules.busy)} onClick={() => retry(schedule)}>{schedules.busy === `retry-${schedule.id}` ? "提交重试中…" : "重试最近执行"}</button>}
            <button className="button danger" type="button" disabled={Boolean(schedules.busy)} onClick={() => setDeleting(schedule.id)}>删除</button>
          </div>
          {canRetry(schedule) && <p className="helper">重试将使用当前配置重新创作并自动发布，保留原执行记录，不改变计划启停状态。</p>}
          {deleting === schedule.id && <div className="schedule-delete" role="group" aria-label={`确认删除 ${schedule.name}`}><p>删除「{schedule.name}」并停止后续计划？执行记录会保留；正在执行时无法删除。</p><div className="button-group"><button className="button danger" type="button" disabled={Boolean(schedules.busy)} onClick={() => remove(schedule.id)}>确认删除</button><button className="button" type="button" disabled={Boolean(schedules.busy)} onClick={() => setDeleting("")}>取消删除</button></div></div>}
        </article>)}
      </div>
      {schedules.selectedId && <section id="schedule-detail" className="schedule-detail" aria-labelledby="schedule-detail-title">
        <div className="schedule-heading"><h2 id="schedule-detail-title">{selected?.name || "已删除的定时任务"} · 执行记录</h2><button className="button" type="button" disabled={Boolean(schedules.busy)} onClick={() => schedules.select("")}>关闭详情</button></div>
        {selected && <div className="schedule-config"><dl className="schedule-meta"><div><dt>搜索关键词</dt><dd>{selected.keyword}</dd></div><div><dt>创作选题</dt><dd>{selected.topic}</dd></div><div><dt>参考数量</dt><dd>{selected.reference_count}</dd></div><div><dt>Cron / 时区</dt><dd>{selected.cron} · {selected.timezone}</dd></div></dl><h3>写作要求</h3><p className="schedule-prose">{selected.instructions || "未填写额外写作要求"}</p></div>}
        <p className="helper">以下时间按浏览器本地时区显示，计划按任务设置的时区执行。</p>
        {schedules.runsLoading && <p className="muted">正在读取执行记录…</p>}
        {!schedules.runsLoading && !schedules.runsError && !schedules.total && <p className="muted">暂无执行记录，启用后将按计划执行。</p>}
        {schedules.runs.map((run) => <RunRecord key={run.id} run={run} api={api} token={token} active={active} />)}
        {schedules.total > 0 && <nav className="schedule-pagination" aria-label="执行记录分页"><button className="button" type="button" disabled={schedules.runsLoading || Boolean(schedules.busy) || schedules.page <= 1} onClick={() => schedules.setPage(schedules.page - 1)}>上一页</button><span>第 {schedules.page} / {schedules.pages} 页 · 共 {schedules.total} 次</span><button className="button" type="button" disabled={schedules.runsLoading || Boolean(schedules.busy) || schedules.page >= schedules.pages} onClick={() => schedules.setPage(schedules.page + 1)}>下一页</button></nav>}
      </section>}
    </section>
  );
}

export default function Schedules(props) {
  return <SchedulePage key={props.token} {...props} />;
}
