import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";
import mdx from "@astrojs/mdx";
import sitemap from "@astrojs/sitemap";
import { unified } from "@astrojs/markdown-remark";
import rehypeSanitize from "rehype-sanitize";
import { createReadStream, existsSync, statSync } from "node:fs";
import { cp } from "node:fs/promises";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

// Decap stores media in /content/uploads and links it as /uploads/... (see
// cms/config.yml media_folder/public_folder), so the site must publish that
// folder at /uploads. Copied into the build output; served directly in dev.
const uploadsDir = fileURLToPath(
  new URL("../content/uploads/", import.meta.url),
);
const uploadTypes = {
  ".pdf": "application/pdf",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".png": "image/png",
  ".webp": "image/webp",
  ".gif": "image/gif",
  ".svg": "image/svg+xml",
};
function cmsUploads() {
  return {
    name: "numun-cms-uploads",
    hooks: {
      "astro:server:setup": ({ server }) => {
        server.middlewares.use("/uploads", (req, res, next) => {
          const file = normalize(
            join(uploadsDir, decodeURIComponent(req.url.split("?")[0])),
          );
          if (
            !file.startsWith(uploadsDir) ||
            !existsSync(file) ||
            !statSync(file).isFile()
          ) {
            return next();
          }
          res.setHeader(
            "Content-Type",
            uploadTypes[extname(file).toLowerCase()] ??
              "application/octet-stream",
          );
          createReadStream(file).pipe(res);
        });
      },
      "astro:build:done": async ({ dir }) => {
        await cp(uploadsDir, new URL("uploads/", dir), { recursive: true });
      },
    },
  };
}

// Site is environment-aware: SITE_BASE is set in CI per environment so
// `<link rel="canonical">`, sitemap.xml, and feed.xml all carry the right
// origin. The deploy workflow (.github/workflows/site.yml) wires it from
// `vars.APEX_DOMAIN`. Falling back to a localhost sentinel for local `astro
// dev` keeps the build green without ever silently shipping a wrong origin
// to prod.
const site = process.env.SITE_BASE ?? "http://localhost:4321";
// The environment's apex, for links to the portal/API/CMS (see
// src/lib/site-urls.ts). Differs from SITE_BASE's host only for PR previews,
// which live under pr-<n>.preview.<apex> but link to <apex>'s portal.
const siteApex = process.env.SITE_APEX || new URL(site).hostname;

// https://astro.build/config
export default defineConfig({
  site,
  output: "static",
  // Pages link and set canonicals without a trailing slash (/about); make
  // the sitemap match. CloudFront serves both forms.
  trailingSlash: "never",
  integrations: [mdx(), sitemap(), cmsUploads()],
  vite: {
    // Tailwind v4 plugs into Astro via the Vite plugin (the legacy
    // `@astrojs/tailwind` integration is pinned to Tailwind 3 and is
    // not upgraded for v4 yet).
    plugins: [tailwindcss()],
    define: { __SITE_APEX__: JSON.stringify(siteApex) },
  },
  server: {
    port: 4321,
  },
  markdown: {
    processor: unified({ rehypePlugins: [rehypeSanitize] }),
  },
});
