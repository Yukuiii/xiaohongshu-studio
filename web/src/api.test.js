import { describe, expect, it, vi } from "vitest";
import { createAPI } from "./api.js";

const settings = () => ({ token: "service-token" });

describe("账号请求契约", () => {
  it("给所有账号业务请求携带捕获的账号，公共接口不携带账号", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ success: true, data: {} }),
      });
    vi.stubGlobal("fetch", fetch);
    const api = createAPI(settings);
    for (const path of [
      "/login/status",
      "/login/browser",
      "/images/i",
      "/images/upload",
      "/feeds/search",
      "/feeds/detail",
      "/ai/compose",
      "/ai/image",
      "/publish",
    ])
      await api(path, { accountId: "A" });
    for (const [, options] of fetch.mock.calls) {
      expect(options.headers["X-XHS-Account-ID"]).toBe("A");
      expect(options.headers.Authorization).toBe("Bearer service-token");
    }
    fetch.mockClear();
    for (const path of [
      "/accounts",
      "/accounts/A",
      "/accounts/A/reset-profile",
      "/ai/models",
    ])
      await api(path, { accountId: "A" });
    for (const [, options] of fetch.mock.calls)
      expect(options.headers).not.toHaveProperty("X-XHS-Account-ID");
  });

  it.each(["ACCOUNT_BUSY", "LOGIN_BROWSER_BUSY"])(
    "保留 %s 错误码与 HTTP 状态",
    async (code) => {
      vi.stubGlobal(
        "fetch",
        vi
          .fn()
          .mockResolvedValue({
            ok: false,
            status: 409,
            json: async () => ({ success: false, code, error: "busy" }),
          }),
      );
      await expect(
        createAPI(settings)("/login/browser", { accountId: "A" }),
      ).rejects.toMatchObject({ code, status: 409 });
    },
  );
});
