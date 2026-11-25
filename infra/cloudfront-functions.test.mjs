// Unit tests for the CloudFront Functions defined inline in the SAM templates.
// They run on the edge for every request, so a mistake (an open redirect, a
// spoofable viewer IP, a broken index rewrite) would hit every user. Run with
// `node --test infra/`.
//
// Each test pulls the function's source out of its template, substitutes the
// template variables CloudFormation would, and runs it on sample events.

import { readFileSync } from "node:fs";
import { describe, it } from "node:test";
import assert from "node:assert/strict";

// cloudFrontFunction extracts the `FunctionCode` block of the named resource
// and returns its `handler`.
function cloudFrontFunction(templatePath, resource, vars = {}) {
  const lines = readFileSync(
    new URL(templatePath, import.meta.url),
    "utf8",
  ).split("\n");
  const start = lines.findIndex((l) => l === `  ${resource}:`);
  assert.ok(start >= 0, `${resource} not found in ${templatePath}`);
  const codeLine = lines.findIndex(
    (l, i) => i > start && /^\s+FunctionCode:/.test(l),
  );
  // The code is the indented block after `FunctionCode: |` or, for !Sub,
  // after the `- |` line that follows it.
  let first = codeLine + 1;
  if (/^\s+- \|\s*$/.test(lines[first])) first += 1;
  const indent = lines[first].match(/^\s*/)[0].length;
  const body = [];
  for (let i = first; i < lines.length; i++) {
    const l = lines[i];
    if (l.trim() !== "" && l.match(/^\s*/)[0].length < indent) break;
    body.push(l.slice(indent));
  }
  let code = body.join("\n");
  code = code.replace(/\$\{(\w+)\}/g, (m, name) => {
    assert.ok(name in vars, `template variable ${name} needs a test value`);
    return vars[name];
  });
  return new Function(`${code}\nreturn handler;`)();
}

function request(uri, headers = {}) {
  const h = {};
  for (const [k, v] of Object.entries(headers)) h[k] = { value: v };
  return { request: { uri, headers: h }, viewer: { ip: "198.51.100.7" } };
}

describe("ApiViewerRequestFunction", () => {
  const handler = cloudFrontFunction(
    "./api/template.yaml",
    "ApiViewerRequestFunction",
  );

  it("stamps the viewer's IP", () => {
    const out = handler(request("/v1/health"));
    assert.equal(out.headers["x-numun-viewer-ip"].value, "198.51.100.7");
  });

  it("overwrites a client-supplied viewer IP", () => {
    const out = handler(
      request("/v1/health", { "x-numun-viewer-ip": "10.0.0.1" }),
    );
    assert.equal(out.headers["x-numun-viewer-ip"].value, "198.51.100.7");
  });
});

describe("WwwRedirectFunction", () => {
  const handler = cloudFrontFunction(
    "./base-cdn/template.yaml",
    "WwwRedirectFunction",
    {
      Apex: "test.numun.org",
    },
  );

  it("redirects www to the apex, keeping the path", () => {
    const out = handler(request("/about/", { host: "www.test.numun.org" }));
    assert.equal(out.statusCode, 301);
    assert.equal(out.headers.location.value, "https://test.numun.org/about/");
  });

  it("redirects the legacy /portal paths to the portal host", () => {
    for (const [uri, to] of [
      ["/portal", "https://portal.test.numun.org"],
      ["/portal/delegation", "https://portal.test.numun.org/delegation"],
    ]) {
      const out = handler(request(uri, { host: "test.numun.org" }));
      assert.equal(out.statusCode, 301, uri);
      assert.equal(out.headers.location.value, to, uri);
    }
  });

  it("doesn't treat /portal-something as the portal", () => {
    const out = handler(request("/portals", { host: "test.numun.org" }));
    assert.equal(out.statusCode, undefined);
    assert.equal(out.uri, "/portals/index.html");
  });

  it("maps directory paths to their index.html", () => {
    for (const [uri, want] of [
      ["/", "/index.html"],
      ["/about/", "/about/index.html"],
      ["/about", "/about/index.html"],
      ["/news/2026/award-winners", "/news/2026/award-winners/index.html"],
      ["/favicon.svg", "/favicon.svg"],
      ["/_astro/app.1a2b.js", "/_astro/app.1a2b.js"],
    ]) {
      assert.equal(
        handler(request(uri, { host: "test.numun.org" })).uri,
        want,
        uri,
      );
    }
  });

  it("only redirects its own www host", () => {
    const out = handler(request("/", { host: "www.evil.example" }));
    assert.equal(out.statusCode, undefined);
  });
});

describe("PreviewRouterFunction", () => {
  const vars = { Apex: "test.numun.org" };
  const handler = cloudFrontFunction(
    "./base-cdn/template.yaml",
    "PreviewRouterFunction",
    vars,
  );
  const site = cloudFrontFunction(
    "./base-cdn/template.yaml",
    "WwwRedirectFunction",
    vars,
  );
  const host = "pr-42.preview.test.numun.org";

  it("serves a PR's files from its prefix", () => {
    const out = handler(request("/about/", { host }));
    assert.equal(out.uri, "/pr-42/about/index.html");
  });

  // Previews must behave exactly like the site, apart from the prefix.
  it("routes and redirects every path the way the site does", () => {
    for (const uri of [
      "/",
      "/about",
      "/about/",
      "/news/2026/award-winners",
      "/favicon.svg",
      "/_astro/app.1a2b.js",
      "/uploads/documents/DelegateGuide.pdf",
      "/portal",
      "/portal/delegation",
      "/portals",
      "/no-such-page",
    ]) {
      const want = site(request(uri, { host: "test.numun.org" }));
      const got = handler(request(uri, { host }));
      if (want.statusCode) {
        assert.equal(got.statusCode, want.statusCode, uri);
        assert.deepEqual(got.headers, want.headers, uri);
      } else {
        assert.equal(got.statusCode, undefined, uri);
        assert.equal(got.uri, `/pr-42${want.uri}`, uri);
      }
    }
  });

  it("404s hosts that aren't a PR preview", () => {
    for (const h of [
      "preview.test.numun.org",
      "pr-x.preview.test.numun.org",
      "",
    ]) {
      assert.equal(handler(request("/", { host: h })).statusCode, 404, h);
    }
  });
});

// lambdaHandler extracts an inline (ZipFile) Lambda's source and runs it as
// CommonJS with the given module stubs.
function lambdaHandler(templatePath, resource, modules, env) {
  const lines = readFileSync(
    new URL(templatePath, import.meta.url),
    "utf8",
  ).split("\n");
  const start = lines.findIndex((l) => l === `  ${resource}:`);
  assert.ok(start >= 0, `${resource} not found in ${templatePath}`);
  const zip = lines.findIndex((l, i) => i > start && /^\s+ZipFile: \|/.test(l));
  const indent = lines[zip + 1].match(/^\s*/)[0].length;
  const body = [];
  for (let i = zip + 1; i < lines.length; i++) {
    const l = lines[i];
    if (l.trim() !== "" && l.match(/^\s*/)[0].length < indent) break;
    body.push(l.slice(indent));
  }
  const exports = {};
  new Function("require", "exports", "process", body.join("\n"))(
    (name) => modules[name],
    exports,
    { env },
  );
  return exports.handler;
}

describe("PreviewNotFoundFunction", () => {
  const files = { "pr-42/404.html": "<h1>Page not found</h1>" };
  const reads = [];
  class GetObjectCommand {
    constructor(input) {
      this.input = input;
    }
  }
  class S3Client {
    async send(cmd) {
      reads.push(cmd.input);
      const f = files[cmd.input.Key];
      if (f === undefined) {
        throw Object.assign(new Error("denied"), { name: "AccessDenied" });
      }
      return { Body: { transformToString: async () => f } };
    }
  }
  const handler = lambdaHandler(
    "./base-cdn/template.yaml",
    "PreviewNotFoundFunction",
    { "@aws-sdk/client-s3": { S3Client, GetObjectCommand } },
    { BUCKET: "numun-test-previews" },
  );

  it("serves the PR's own 404 page with a 404 status", async () => {
    const out = await handler({ rawPath: "/pr-42/no-such-page/index.html" });
    assert.equal(out.statusCode, 404);
    assert.equal(out.headers["content-type"], "text/html; charset=utf-8");
    assert.equal(out.body, "<h1>Page not found</h1>");
    assert.deepEqual(reads.at(-1), {
      Bucket: "numun-test-previews",
      Key: "pr-42/404.html",
    });
  });

  it("falls back to plain text when the PR has no 404 page", async () => {
    const out = await handler({ rawPath: "/pr-7/missing.png" });
    assert.equal(out.statusCode, 404);
    assert.equal(out.body, "Not found");
  });

  it("never reads outside a PR prefix", async () => {
    const before = reads.length;
    const out = await handler({ rawPath: "/404.html" });
    assert.equal(out.statusCode, 404);
    assert.equal(reads.length, before);
  });
});
