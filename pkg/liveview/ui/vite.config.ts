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
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            // three is pinned, large, and only the Graph and Library pages load
            // it. It is pure ESM with no dependencies, so in a chunk of its own
            // nothing else's hash leaks into its file name: the name changes
            // only when three does, or when the app starts using a part of it
            // it did not before, and a browser keeps it cached across the app
            // releases in between.
            // The force-graph libraries stay with the page: one of them is
            // CommonJS, and the interop helpers it needs live in the app's
            // chunk, whose name changes with every build.
            { name: "three", test: /node_modules[\\/]three[\\/]/ },
          ],
        },
      },
    },
    // That chunk is the one past Vite's 500 kB default: large by nature,
    // loaded lazily, and cached for good.
    chunkSizeWarningLimit: 800,
  },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});
