import { defineConfig } from "astro/config";
import tailwind from "@astrojs/tailwind";
import mdx from "@astrojs/mdx";
import sitemap from "@astrojs/sitemap";
import rehypeSanitize from "rehype-sanitize";

// Site is environment-aware: SITE_BASE is set in CI per environment so
// `<link rel="canonical">`, sitemap.xml, and feed.xml all carry the right
// origin. Default for local dev is the staging origin so editors can preview.
const site = process.env.SITE_BASE ?? "https://test.numun.org";

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
