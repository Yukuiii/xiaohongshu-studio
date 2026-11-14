import { useCallback, useEffect, useRef, useState } from "react";

const listData = (data) => {
  if (!Array.isArray(data?.schedules)) throw new Error("定时任务列表响应无效，请刷新重试。");
  return data;
};
const runData = (data) => {
  if (!Array.isArray(data?.runs) || !Number.isSafeInteger(data.total) || data.total < 0)
    throw new Error("执行记录响应无效，请刷新重试。");
  return data;
};

function useScheduleQuery({ api, token, active, path, version, parse }) {
  const [state, setState] = useState({ path, token, data: null, loading: true, error: "" });
  const latest = useRef(null);
  latest.current = { path, token, active, version };
  useEffect(() => {
    if (!active || !path) return;
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
        latest.current.active && latest.current.token === token && latest.current.path === path && latest.current.version === version;
      setState((previous) => ({
        path, token, data: previous.path === path && previous.token === token ? previous.data : null,
        loading: !silent, error: silent && previous.path === path && previous.token === token ? previous.error : "",
      }));
      try {
        const data = await api(path, { token, signal: request.signal });
        if (valid()) setState({ path, token, data: parse(data), loading: false, error: "" });
      } catch (error) {
        if (valid()) setState((previous) => ({ ...previous, loading: false, error: error.message }));
      } finally {
        if (valid()) timer = setTimeout(() => load(true), 5000);
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
  }, [api, token, active, path, version, parse]);
  const updateData = (update) => setState((previous) => previous.path === path && previous.token === token
    ? { ...previous, data: update(previous.data), error: "" } : previous);
  return { ...(state.path === path && state.token === token
    ? state : { data: null, loading: true, error: "" }), updateData };
}

export function useSchedules({ api, token, active }) {
  const [selectedId, setSelectedId] = useState("");
  const [page, setPage] = useState(1);
  const [version, setVersion] = useState(0);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState(null);
  const mutation = useRef(null);
  const latest = useRef(null);
  latest.current = { token, active };
  const refresh = useCallback(() => setVersion((value) => value + 1), []);
  const list = useScheduleQuery({ api, token, active, path: "/schedules", version, parse: listData });
  const runs = useScheduleQuery({
    api, token, active, version, parse: runData,
    path: selectedId ? `/schedules/${encodeURIComponent(selectedId)}/runs?page=${page}&page_size=20` : "",
  });
  const pages = Math.max(1, Math.ceil((runs.data?.total || 0) / 20));
  useEffect(() => {
    if (runs.data && page > pages) setPage(pages);
  }, [runs.data, page, pages]);
  useEffect(() => {
    setBusy("");
    setNotice(null);
    return () => {
      mutation.current?.abort();
      mutation.current = null;
    };
  }, [token, active]);
  const select = (id) => {
    if (mutation.current) return;
    setSelectedId(id);
    setPage(1);
  };
  const mutate = async (operation, path, options, message) => {
    if (mutation.current || !active) return false;
    const controller = new AbortController();
    mutation.current = controller;
    const valid = () => mutation.current === controller && !controller.signal.aborted &&
      latest.current.token === token && latest.current.active;
    setBusy(operation);
    setNotice(null);
    try {
      const data = await api(path, { ...options, token, signal: controller.signal });
      if (!valid()) return false;
      if (data?.schedule) list.updateData((previous) => {
        const schedules = previous?.schedules || [];
        return { schedules: schedules.some((item) => item.id === data.schedule.id)
          ? schedules.map((item) => item.id === data.schedule.id ? data.schedule : item)
          : [data.schedule, ...schedules] };
      });
      setNotice({ message, type: "success" });
      refresh();
      return true;
    } catch (error) {
      if (valid()) setNotice({ message: error.message, type: "error" });
      return false;
    } finally {
      if (mutation.current === controller) {
        mutation.current = null;
        setBusy("");
      }
    }
  };
  return {
    schedules: list.data?.schedules || [], loading: list.loading, error: list.error,
    runs: runs.data?.runs || [], runsLoading: runs.loading, runsError: runs.error,
    total: runs.data?.total || 0, pages, page, setPage, selectedId, select,
    refresh, busy, notice, mutate,
  };
}
