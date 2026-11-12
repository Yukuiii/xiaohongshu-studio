import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

beforeEach(() => {
  localStorage.clear();
  history.replaceState(null, "", "/web/#workspace");
  vi.stubGlobal("scrollTo", vi.fn());
  vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:preview");
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  const held = new Set();
  Object.defineProperty(navigator, "locks", {
    configurable: true,
    value: {
      request: vi.fn(async (name, options, callback) => {
        if (held.has(name)) return callback(null);
        held.add(name);
        try {
          return await callback({ name });
        } finally {
          held.delete(name);
        }
      }),
    },
  });
});

Object.defineProperty(URL, "createObjectURL", {
  configurable: true,
  writable: true,
  value: () => "blob:preview",
});
Object.defineProperty(URL, "revokeObjectURL", {
  configurable: true,
  writable: true,
  value: () => {},
});
Object.defineProperty(HTMLImageElement.prototype, "decode", {
  configurable: true,
  value: async () => {},
});
HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
HTMLDialogElement.prototype.close = function () {
  this.open = false;
};

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
