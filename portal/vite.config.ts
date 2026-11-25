// Vitest config lives alongside vite config; importing defineConfig from
// "vitest/config" (instead of "vite") makes TypeScript accept the `test`
// block while preserving the underlying Vite UserConfig shape.
import { defineConfig } from "vitest/config";
import solid from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";
import { sentryVitePlugin } from "@sentry/vite-plugin";
import { fileURLToPath, URL } from "node:url";

// Set only by the deploy workflow (portal.yml). Without it, builds keep plain
// public source maps and never talk to Sentry. See docs/runbooks/sentry-setup.md.
const uploadSourceMaps = !!process.env.SENTRY_AUTH_TOKEN;

export default defineConfig({
  plugins: [
    solid(),
    tailwindcss(),
    // Must stay last so it sees the final bundle.
    uploadSourceMaps &&
      sentryVitePlugin({
        org: "numun",
        project: "numun-frontend",
        authToken: process.env.SENTRY_AUTH_TOKEN,
        release: {
          name: process.env.VITE_RELEASE_VERSION,
          // Shallow CI checkout; suspect commits come from the GitHub code mapping.
          setCommits: false,
        },
        // Maps go to Sentry only, not to the public bucket.
        sourcemaps: { filesToDeleteAfterUpload: ["./dist/**/*.map"] },
        telemetry: false,
        // A Sentry outage shouldn't block a deploy; it only costs symbolication.
        errorHandler: (err) =>
          console.warn("::warning::Sentry source map upload failed:", err),
      }),
  ],
  // amazon-cognito-identity-js pulls in buffer@4, which reads Node's `global` at module init.
  define: {
    global: "globalThis",
  },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
  },
  // `vite preview` serves the e2e build (see /e2e). Proxying the API puts it
  // on the portal's origin: the plain-HTTP Go server (LOCAL_HTTP=true) sends
  // no CORS headers — API Gateway adds those in prod.
  preview: {
    port: 4173,
    strictPort: true,
    proxy: {
      "^/(numun\\.v1\\.|v1/)": "http://localhost:3000",
    },
  },
  build: {
    target: "es2022",
    // "hidden" omits the sourceMappingURL comment for maps deleted after upload.
    sourcemap: uploadSourceMaps ? "hidden" : true,
  },
  test: {
    // No DOM tests in v1 — keep the environment "node" so vitest doesn't
    // demand jsdom/happy-dom as peer deps. Switch to "jsdom" when a
    // component test lands.
    environment: "node",
  },
});
