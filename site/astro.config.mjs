import { defineConfig } from "astro/config";
import tailwind from "@astrojs/tailwind";
import mdx from "@astrojs/mdx";
import rehypeSanitize from "rehype-sanitize";

// https://astro.build/config
export default defineConfig({
  output: "static",
  integrations: [tailwind(), mdx()],
  server: {
    port: 4321,
  },
  markdown: {
    rehypePlugins: [rehypeSanitize],
  },
});
