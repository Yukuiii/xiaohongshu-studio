function sortedJSON(value) {
  if (Array.isArray(value)) return value.map(sortedJSON);
  if (value && typeof value === "object")
    return Object.fromEntries(
      Object.keys(value).sort().map((key) => [key, sortedJSON(value[key])]),
    );
  return value;
}

export function samePublishedContent(body, fingerprint) {
  if (!fingerprint) return false;
  try {
    const normalize = (value) => ({
      ...value,
      tags: value.tags?.length ? value.tags : [],
      visibility: value.visibility || "公开可见",
    });
    return JSON.stringify(sortedJSON(normalize(body))) ===
      JSON.stringify(sortedJSON(normalize(JSON.parse(fingerprint))));
  } catch {
    return false;
  }
}

export const protectionKey = (accountId) =>
  `hongshu-workbench.publish.v1:${accountId}`;

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
