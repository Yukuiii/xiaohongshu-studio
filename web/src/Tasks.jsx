import { useMemo, useState } from "react";
import { PublishedNoteLink, PublishResult } from "./components.jsx";
import { ToastNotice } from "./notifications.jsx";
import { taskIsActive, useTasks } from "./useTasks.js";
import "./tasks.css";

const statuses = {
  queued: "排队中", running: "执行中", succeeded: "已完成", failed: "失败", uncertain: "结果未知",
};
const time = (value, compact = false) => {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
    ...(!compact && { second: "2-digit" }), hour12: false,
  });
};

function duration(task) {
  if (!task.started_at || !task.finished_at) return "";
  const elapsed = new Date(task.finished_at) - new Date(task.started_at);
  if (!Number.isFinite(elapsed) || elapsed < 0) return "";
  const seconds = Math.round(elapsed / 1000);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (hours) return `${hours}小时${minutes ? `${minutes}分` : ""}`;
  if (minutes) return `${minutes}分${seconds % 60 ? `${seconds % 60}秒` : ""}`;
  return `${seconds}秒`;
}

function TaskStatusIcon({ status }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {status === "running" ? (
        <><path d="M20 7v5h-5" /><path d="M20 12a8 8 0 1 1-2.3-5.7L20 9" /></>
      ) : (
        <>
          <circle cx="12" cy="12" r="8.5" />
          {status === "succeeded" ? <path d="m8 12 2.5 2.5L16 9" />
            : status === "failed" ? <path d="m9 9 6 6m0-6-6 6" />
              : status === "queued" ? <path d="M12 7v5l3 2" />
                : <path d="M12 7v6m0 3v.1" />}
        </>
      )}
    </svg>
  );
}

function TaskCard({ task, api, token, active }) {
  const [open, setOpen] = useState(false);
  const elapsed = duration(task);
  const timestamp = task.finished_at || task.started_at || task.created_at;
  const timeLabel = task.finished_at
    ? task.status === "succeeded" ? "完成于" : "结束于"
    : task.started_at ? "开始于" : "提交于";
  const detailsId = `task-details-${task.id}`;
  const hasResult = task.result != null || task.image_ids?.length > 0;

  return (
    <article className="card task-item" data-status={task.status} aria-label={`发布任务：${task.title || "未命名笔记"}`}>
      <div className="task-heading">
        <span className="task-symbol"><TaskStatusIcon status={task.status} /></span>
        <div className="task-heading-copy">
          <h2>{task.title || "未命名笔记"}</h2>
          <div className="task-meta">
            <span className="task-kind">{task.kind === "publish_video" ? "视频" : "图文"}</span>
            <span className="task-account">{task.account_name || "未命名账号"}</span>
          </div>
        </div>
        <span className="task-status">{statuses[task.status] || "未知状态"}</span>
      </div>
      <div className="task-timing">
        <span>{timeLabel} <time dateTime={timestamp || undefined}>{time(timestamp, true)}</time></span>
        {elapsed && <span className="task-duration">用时 <strong>{elapsed}</strong></span>}
      </div>
      {task.message && task.status !== "succeeded" && <p className="task-message">{task.message}</p>}
      {task.error && <p className="tasks-error">失败原因：{task.error}</p>}
      {task.status === "uncertain" && <p className="task-warning">请先在小红书账号核对实际发布结果，避免重复发布。当前账号可在工作台核对后解除锁定。</p>}
      {taskIsActive(task) && <p className="task-progress-hint">任务进行中，状态将自动更新；此账号暂不能再次发布或手动解除锁定。</p>}
      <div className="task-footer">
        <button type="button" className="task-toggle" aria-expanded={open} aria-controls={detailsId} onClick={() => setOpen((value) => !value)}>
          {open ? "收起详情" : "任务详情"}
          <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m6 8 4 4 4-4" /></svg>
        </button>
        <div className="task-link-actions"><PublishedNoteLink result={task.result} active={active} /></div>
      </div>
      <div className="task-details" id={detailsId} hidden={!open}>
        {open && (
          <>
            {hasResult && (
              <section className="task-detail-section" aria-label="发布结果">
                <h3>发布结果</h3>
                <PublishResult result={task.result} images={task.image_ids} api={api} token={token} accountId={task.account_id} active={active} />
              </section>
            )}
            <section className="task-detail-section" aria-label="任务信息">
              <h3>任务信息</h3>
              <dl className="task-times">
                <div><dt>创建时间</dt><dd>{time(task.created_at)}</dd></div>
                <div><dt>开始时间</dt><dd>{time(task.started_at)}</dd></div>
                <div><dt>结束时间</dt><dd>{time(task.finished_at)}</dd></div>
                <div><dt>更新时间</dt><dd>{time(task.updated_at)}</dd></div>
              </dl>
              <dl className="task-identifiers">
                <div><dt>任务编号</dt><dd>{task.id}</dd></div>
                {task.account_id && <div><dt>账号编码</dt><dd>{task.account_id}</dd></div>}
              </dl>
            </section>
          </>
        )}
      </div>
    </article>
  );
}

export default function Tasks({ api, token, active }) {
  const tasks = useTasks({ api, token, active });
  const errorNotice = useMemo(() => tasks.error
    ? { message: `读取任务失败：${tasks.error} 请刷新重试。`, type: "error" }
    : null, [tasks.error]);
  return (
    <div className="tasks-page" aria-busy={tasks.loading}>
      <header className="intro">
        <div>
          <p className="eyebrow">后台发布记录</p>
          <h1 id="tasks-title" tabIndex={-1}>任务</h1>
          <p className="muted">查看所有账号的图文与视频发布任务。任务由服务端执行，离开页面后仍会继续。</p>
        </div>
        <button type="button" className="button" onClick={tasks.refresh} disabled={tasks.loading}>刷新任务</button>
      </header>
      <ToastNotice active={active} notice={errorNotice} />
      {!tasks.loading && !tasks.error && tasks.total === 0 && (
        <div className="card tasks-empty"><h2>还没有发布任务</h2><p className="muted">在工作台核对并确认发布后，可在这里查看进度与结果。</p><a className="button" href="#workspace">前往工作台</a></div>
      )}
      <div className="task-list">
        {tasks.tasks.map((task) => (
          <TaskCard task={task} api={api} token={token} active={active} key={task.id} />
        ))}
      </div>
      {tasks.total > 0 && <nav className="tasks-pagination" aria-label="任务分页">
        <button type="button" className="button" disabled={tasks.loading || tasks.page <= 1} onClick={() => tasks.setPage(tasks.page - 1)}>上一页</button>
        <span>第 {tasks.page} / {tasks.pages} 页 · 共 {tasks.total} 项</span>
        <button type="button" className="button" disabled={tasks.loading || tasks.page >= tasks.pages} onClick={() => tasks.setPage(tasks.page + 1)}>下一页</button>
      </nav>}
    </div>
  );
}
