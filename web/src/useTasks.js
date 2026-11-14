import { useCallback, useEffect, useRef, useState } from "react";

export const taskIsActive = (task) => task.status === "queued" || task.status === "running";

export function useTasks({ api, token, active }) {
  const [page, setPage] = useState(1);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [state, setState] = useState({ tasks: [], total: 0, loading: true, error: "", token, page: 1 });
  const latest = useRef({ token, active, page });
  latest.current = { token, active, page };
  const owner = useRef(token);
  if (owner.current !== token) {
    owner.current = token;
    if (page !== 1) setPage(1);
  }
  const refresh = useCallback(() => setRefreshVersion((value) => value + 1), []);

  useEffect(() => {
    if (!active) return;
    let disposed = false;
    let controller;
    let timer;
    const suspend = () => {
      clearTimeout(timer);
      controller?.abort();
    };
    const load = async (silent = false) => {
      suspend();
      if (disposed || document.hidden) return;
      const request = new AbortController();
      controller = request;
      const valid = () => !disposed && !request.signal.aborted && controller === request &&
        latest.current.token === token && latest.current.page === page && latest.current.active;
      setState((previous) => ({
        ...(previous.token === token && previous.page === page ? previous : { tasks: [], total: 0 }),
        token, page, loading: silent ? previous.loading : true,
        error: silent ? previous.error : "",
      }));
      try {
        const data = await api(`/tasks?page=${page}&page_size=20`, { token, signal: request.signal });
        if (!valid()) return;
        if (!Array.isArray(data?.tasks) || !Number.isSafeInteger(data.total) || data.total < 0)
          throw new Error("任务列表响应无效，请检查服务版本后刷新。");
        const lastPage = Math.max(1, Math.ceil(data.total / 20));
        if (page > lastPage) {
          setPage(lastPage);
          return;
        }
        setState({ tasks: data.tasks, total: data.total, loading: false, error: "", token, page });
        window.dispatchEvent(new Event("publish-tasks-changed"));
      } catch (error) {
        if (!valid()) return;
        setState((previous) => ({ ...previous, loading: false, error: error.message }));
      } finally {
        if (valid()) timer = setTimeout(() => load(true), 3000);
      }
    };
    const visibility = () => document.hidden ? suspend() : load();
    const resume = () => load();
    load();
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("pagehide", suspend);
    window.addEventListener("pageshow", resume);
    return () => {
      disposed = true;
      suspend();
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("pagehide", suspend);
      window.removeEventListener("pageshow", resume);
    };
  }, [api, token, active, page, refreshVersion]);

  const visible = state.token === token && state.page === page
    ? state : { tasks: [], total: 0, loading: true, error: "" };
  return { ...visible, page, setPage, refresh, pages: Math.max(1, Math.ceil(visible.total / 20)) };
}
