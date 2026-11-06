import path from "node:path";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vitest/config";

// The dev server proxies the JSON API to a running liveview so the SPA can be
// worked on against real data; the production build is served by Go from
// pkg/liveview/dist.
const api = `http://127.0.0.1:${process.env.CORTEXDB_LIVE_PORT ?? "46377"}`;

export default defineConfig({
  // Relative asset URLs: the page may be mounted under a prefix of another mux.
  base: "./",
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  server: { proxy: { "/api": { target: api, changeOrigin: false } } },
  build: {
    outDir: "../dist",
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
  },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});
