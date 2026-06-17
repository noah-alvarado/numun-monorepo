import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";
import mdx from "@astrojs/mdx";
import sitemap from "@astrojs/sitemap";
import { unified } from "@astrojs/markdown-remark";
import rehypeSanitize from "rehype-sanitize";

// Site is environment-aware: SITE_BASE is set in CI per environment so
// `<link rel="canonical">`, sitemap.xml, and feed.xml all carry the right
// origin. The deploy workflow (.github/workflows/site.yml) wires it from
// `vars.APEX_DOMAIN`. Falling back to a localhost sentinel for local `astro
// dev` keeps the build green without ever silently shipping a wrong origin
// to prod.
const site = process.env.SITE_BASE ?? "http://localhost:4321";

// https://astro.build/config
export default defineConfig({
  site,
  output: "static",
  integrations: [mdx(), sitemap()],
  vite: {
    // Tailwind v4 plugs into Astro via the Vite plugin (the legacy
    // `@astrojs/tailwind` integration is pinned to Tailwind 3 and is
    // not upgraded for v4 yet).
    plugins: [tailwindcss()],
  },
  server: {
    port: 4321,
  },
  markdown: {
    processor: unified({ rehypePlugins: [rehypeSanitize] }),
  },
});
