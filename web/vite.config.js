import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backend = "http://127.0.0.1:18060";

export default defineConfig({
  base: "/web/",
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": { target: backend },
      "/desktop": { target: backend, ws: true },
      "/health": { target: backend },
    },
  },
});
