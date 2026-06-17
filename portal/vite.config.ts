// Vitest config lives alongside vite config; importing defineConfig from
// "vitest/config" (instead of "vite") makes TypeScript accept the `test`
// block while preserving the underlying Vite UserConfig shape.
import { defineConfig } from "vitest/config";
import solid from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [solid(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
  },
  build: {
    target: "es2022",
    sourcemap: true,
  },
  test: {
    // No DOM tests in v1 — keep the environment "node" so vitest doesn't
    // demand jsdom/happy-dom as peer deps. Switch to "jsdom" when a
    // component test lands.
    environment: "node",
  },
});
