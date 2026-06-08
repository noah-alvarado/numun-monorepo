import { defineConfig } from "astro/config";
import tailwind from "@astrojs/tailwind";
import mdx from "@astrojs/mdx";
import sitemap from "@astrojs/sitemap";
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
  integrations: [tailwind(), mdx(), sitemap()],
  server: {
    port: 4321,
  },
  markdown: {
    rehypePlugins: [rehypeSanitize],
  },
});
