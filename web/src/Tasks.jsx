import { useMemo, useState } from "react";
import { PublishResult } from "./components.jsx";
import { ToastNotice } from "./notifications.jsx";
import { taskIsActive, useTasks } from "./useTasks.js";
import "./tasks.css";

const statuses = {
  queued: "排队中", running: "执行中", succeeded: "已完成", failed: "失败", uncertain: "结果未知",
};
const time = (value) => {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString("zh-CN", { hour12: false });
};

function TaskResult({ task, api, token, active }) {
  const [open, setOpen] = useState(false);
  return (
    <details className="task-result" onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>查看发布结果</summary>
      <PublishResult
        result={task.result}
        images={task.image_ids}
        api={api}
        token={token}
        accountId={task.account_id}
        active={active && open}
      />
    </details>
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
          <article className="card task-item" key={task.id} aria-label={`发布任务：${task.title || "未命名笔记"}`}>
            <div className="task-heading"><h2>{task.title || "未命名笔记"}</h2><span className={`task-status ${task.status}`}>{statuses[task.status] || "未知状态"}</span></div>
            <div className="task-meta">
              <p className="task-account">{task.account_name || task.account_id} · {task.kind === "publish_video" ? "视频" : "图文"}</p>
              <small className="task-id muted">任务编号：{task.id}</small>
            </div>
            <dl className="task-times">
              <div><dt>创建时间</dt><dd>{time(task.created_at)}</dd></div>
              <div><dt>开始时间</dt><dd>{time(task.started_at)}</dd></div>
              <div><dt>结束时间</dt><dd>{time(task.finished_at)}</dd></div>
              <div><dt>更新时间</dt><dd>{time(task.updated_at)}</dd></div>
            </dl>
            {task.message && <p>{task.message}</p>}
            {task.error && <p className="tasks-error">失败原因：{task.error}</p>}
            {task.status === "uncertain" && <p className="task-warning">请先在小红书账号核对实际发布结果，避免重复发布。当前账号可在工作台核对后解除锁定。</p>}
            {taskIsActive(task) && <p className="muted">任务进行中，状态将自动更新；此账号暂不能再次发布或手动解除锁定。</p>}
            {(task.result != null || task.image_ids?.length > 0) && (
              <TaskResult task={task} api={api} token={token} active={active} />
            )}
          </article>
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
