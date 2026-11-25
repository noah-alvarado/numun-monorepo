// Serves site/dist the way the site's CloudFront distribution does: directory
// paths map to index.html and /portal redirects to the portal
// (WwwRedirectFunction), unknown paths get /404.html with a 404, and every
// response carries SecurityHeadersPolicy's headers. Also serves the Decap CMS bundle under /cms/ with config.yml
// substituted the way .github/workflows/cms.yml does at deploy, pointed at
// the local API.

import { createReadStream, existsSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";

const ROOT = new URL("../../site/dist/", import.meta.url).pathname;
const CMS = new URL("../../cms/", import.meta.url).pathname;
// Stand-ins for uploads that content references but the repo doesn't hold
// (e.g. stub background guides), served only when site/dist lacks them.
const UPLOAD_STUBS = new URL("../files/site-uploads/", import.meta.url)
  .pathname;
const API_ORIGIN = process.env.CMS_API_ORIGIN ?? "http://localhost:3000";
const PORT = Number(process.env.SITE_PORT ?? 4322);
// The site's build-time apex (SITE_APEX, defaulting to SITE_BASE's host).
const APEX = process.env.SITE_APEX ?? "localhost";

// infra/base-cdn/template.yaml SecurityHeadersPolicy.
const SECURITY_HEADERS = {
  "Strict-Transport-Security": "max-age=31536000; includeSubDomains; preload",
  "X-Content-Type-Options": "nosniff",
  "X-Frame-Options": "DENY",
  "Referrer-Policy": "strict-origin-when-cross-origin",
};

const TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css",
  ".js": "text/javascript",
  ".xml": "application/xml",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".webp": "image/webp",
  ".pdf": "application/pdf",
  ".txt": "text/plain",
  ".json": "application/json",
  ".woff2": "font/woff2",
};

function resolve(uri: string): string {
  let path = decodeURIComponent(uri.split("?")[0]);
  if (path.endsWith("/")) path += "index.html";
  else if (!path.split("/").pop()!.includes(".")) path += "/index.html";
  return normalize(join(ROOT, path));
}

function serveCms(path: string, res: import("node:http").ServerResponse) {
  if (path === "/cms" || path === "/cms/") {
    res.writeHead(200, { "Content-Type": TYPES[".html"] });
    createReadStream(join(CMS, "index.html")).pipe(res);
    return;
  }
  if (path === "/cms/config.yml") {
    const api = new URL(API_ORIGIN);
    const config = readFileSync(join(CMS, "config.yml"), "utf8")
      .replace(/__GITHUB_REPO__/g, "noah-alvarado/numun-monorepo")
      // base_url is https://api.__APEX_DOMAIN__ in the template.
      .replace("https://api.__APEX_DOMAIN__", api.origin)
      .replace(/__APEX_DOMAIN__/g, "localhost:4322");
    res.writeHead(200, { "Content-Type": "text/yaml" });
    res.end(config);
    return;
  }
  res.writeHead(404);
  res.end("Not found");
}

function isFile(f: string): boolean {
  return existsSync(f) && statSync(f).isFile();
}

createServer((req, res) => {
  const path = (req.url ?? "/").split("?")[0];
  if (path === "/cms" || path.startsWith("/cms/")) return serveCms(path, res);
  for (const [k, v] of Object.entries(SECURITY_HEADERS)) res.setHeader(k, v);
  if (path === "/portal" || path.startsWith("/portal/")) {
    res.writeHead(301, {
      Location: `https://portal.${APEX}${(req.url ?? "").slice(7)}`,
    });
    res.end();
    return;
  }
  let file = resolve(req.url ?? "/");
  if (!isFile(file) && path.startsWith("/uploads/")) {
    const stub = normalize(
      join(UPLOAD_STUBS, decodeURIComponent(path.slice(9))),
    );
    if (stub.startsWith(UPLOAD_STUBS) && isFile(stub)) file = stub;
  }
  const allowed = file.startsWith(ROOT) || file.startsWith(UPLOAD_STUBS);
  if (!allowed || !isFile(file)) {
    const notFound = join(ROOT, "404.html");
    res.writeHead(404, { "Content-Type": "text/html; charset=utf-8" });
    if (existsSync(notFound)) createReadStream(notFound).pipe(res);
    else res.end("Not found");
    return;
  }
  res.writeHead(200, {
    "Content-Type": TYPES[extname(file)] ?? "application/octet-stream",
  });
  createReadStream(file).pipe(res);
}).listen(PORT, "127.0.0.1", () => {
  console.log(`site on http://127.0.0.1:${PORT}`);
});
