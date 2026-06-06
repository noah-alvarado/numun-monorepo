import { defineConfig } from "vite";
import solid from "vite-plugin-solid";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [solid()],
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
});
