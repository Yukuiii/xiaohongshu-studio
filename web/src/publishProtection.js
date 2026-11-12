const DRAFT_KEY = "hongshu-workbench.draft.v1";

export const protectionKey = (accountId) =>
  `hongshu-workbench.publish.v1:${accountId}`;

export function readProtection(accountId, fallback = {}) {
  const record = localStorage.getItem(protectionKey(accountId));
  if (record) {
    const value = JSON.parse(record);
    if (
      typeof value.uncertain !== "boolean" ||
      typeof value.lastPublished !== "string" ||
      !Number.isSafeInteger(value.revision) ||
      value.revision < 0
    )
      throw new Error("发布保护记录无法读取，请先核对账号中的发布结果。");
    return value;
  }
  const draft = localStorage.getItem(`${DRAFT_KEY}:${accountId}`);
  const value = draft ? JSON.parse(draft) : fallback;
  return {
    uncertain: value?.uncertain === true,
    lastPublished:
      typeof value?.lastPublished === "string" ? value.lastPublished : "",
    revision: 0,
  };
}

export function writeProtection(accountId, previous, changes) {
  const value = { ...previous, ...changes, revision: previous.revision + 1 };
  try {
    localStorage.setItem(protectionKey(accountId), JSON.stringify(value));
  } catch {
    throw new Error("发布保护未能保存，请恢复本地存储后重试。");
  }
  return value;
}

export async function withPublishLock(accountId, action) {
  if (!navigator.locks?.request)
    throw new Error(
      "此浏览器无法协调多个标签页的发布，请使用支持 Web Locks 的浏览器，并通过 HTTPS 或本机地址访问。",
    );
  return navigator.locks.request(
    protectionKey(accountId),
    { mode: "exclusive", ifAvailable: true },
    (lock) => {
      if (!lock)
        throw new Error(
          "另一个标签页正在处理此账号的发布，请等待完成并核对发布结果。",
        );
      return action();
    },
  );
}
