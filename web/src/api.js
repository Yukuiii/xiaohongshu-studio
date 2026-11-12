export function redact(message, settings = {}, formSettings = {}) {
  let result = String(message || "请求失败，请稍后检查连接或设置。");
  const secrets = [
    settings.token,
    settings.text?.api_key,
    settings.image?.api_key,
    formSettings.token,
    formSettings.text?.api_key,
    formSettings.image?.api_key,
  ];
  for (const secret of secrets) {
    if (secret) result = result.split(secret).join("[已隐藏凭证]");
  }
  return result.slice(0, 400);
}

export function createAPI(getSettings, getFormSettings = getSettings) {
  return async function api(
    path,
    {
      method = "GET",
      body,
      signal,
      token = getSettings().token,
      blob = false,
    } = {},
  ) {
    const headers = {};
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body && !(body instanceof FormData))
      headers["Content-Type"] = "application/json";
    let response;
    try {
      response = await fetch(`/api/v1${path}`, {
        method,
        headers,
        body:
          body instanceof FormData
            ? body
            : body
              ? JSON.stringify(body)
              : undefined,
        signal,
        credentials: "same-origin",
        cache: "no-store",
      });
    } catch (error) {
      if (error.name === "AbortError") throw error;
      throw new Error("无法连接服务，请检查网络以及工作台服务是否运行。");
    }
    if (blob && response.ok) return response.blob();
    let payload;
    try {
      payload = await response.json();
    } catch {
      throw new Error(
        `服务返回了无法识别的响应（${response.status}），请检查服务或接口地址。`,
      );
    }
    if (!response.ok || payload.success !== true) {
      if (response.status === 401 || response.status === 403) {
        throw new Error(
          "访问被拒绝，请在左侧「设置」的高级选项中检查服务访问令牌。",
        );
      }
      const message =
        payload.error || payload.message || `请求失败（${response.status}）`;
      const details =
        typeof payload.details === "string" ? payload.details : "";
      throw new Error(
        redact(
          details ? `${message}：${details}` : message,
          getSettings(),
          getFormSettings(),
        ),
      );
    }
    return payload.data;
  };
}
