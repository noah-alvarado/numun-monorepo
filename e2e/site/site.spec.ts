import { readFile } from "node:fs/promises";

import { expect, test } from "../fixtures";

// Smoke tests for the static landing site (site/dist served like CloudFront
// serves it; see fakes/static-site.ts). Built with SITE_BASE=localhost:4321,
// so absolute URLs in the sitemap use that origin.

async function sitemapPaths(): Promise<string[]> {
  const xml = await readFile(
    new URL("../../site/dist/sitemap-0.xml", import.meta.url),
    "utf8",
  );
  return [...xml.matchAll(/<loc>([^<]+)<\/loc>/g)].map(
    (m) => new URL(m[1]).pathname,
  );
}

test("every page in the sitemap renders", async ({ page, step }) => {
  const paths = await sitemapPaths();
  expect(paths.length).toBeGreaterThan(20);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(`${page.url()}: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(`${page.url()}: ${m.text()}`);
  });

  for (const path of paths) {
    const resp = await page.goto(path);
    expect(resp?.status(), path).toBe(200);
    await expect(page.locator("h1").first(), path).toBeVisible();
    await expect(page, path).toHaveTitle(/NUMUN|Model United Nations/);
    const canonical = await page
      .locator('link[rel="canonical"]')
      .getAttribute("href");
    expect(canonical, path).toBe(`http://localhost:4321${path}`);
  }
  expect(errors).toEqual([]);

  await step("the home page", async () => {
    await page.goto("/");
  });
});

test("no internal link or image is broken", async ({ page, request }) => {
  const seen = new Set<string>();
  const broken: string[] = [];
  for (const path of await sitemapPaths()) {
    await page.goto(path);
    const hrefs = [
      ...(await page
        .locator("a[href]")
        .evaluateAll((as) => as.map((a) => (a as HTMLAnchorElement).href))),
      ...(await page
        .locator("img[src]")
        .evaluateAll((is) => is.map((i) => (i as HTMLImageElement).src))),
    ];
    for (const href of hrefs) {
      const url = new URL(href);
      if (url.origin !== new URL(page.url()).origin) continue;
      const target = url.pathname;
      if (seen.has(target)) continue;
      seen.add(target);
      const resp = await request.get(target);
      if (resp.status() !== 200)
        broken.push(`${path} → ${target} (${resp.status()})`);
    }
  }
  expect(broken).toEqual([]);
  expect(seen.size).toBeGreaterThan(20);
});

test("visitors find their way around", async ({ page, step }) => {
  await step("the header links to each section", async () => {
    await page.goto("/");
    await page
      .getByRole("link", { name: "NUMUN", exact: true })
      .first()
      .click();
    await expect(page).toHaveURL(/\/$/);
  });

  await step("Register points at portal sign-up", async () => {
    await expect(
      page.getByRole("link", { name: "Register" }).first(),
    ).toHaveAttribute("href", /\/sign-up$/);
  });

  await step("committees list links to each committee", async () => {
    await page.goto("/committees");
    const first = page.locator('a[href^="/committees/"]').first();
    const href = await first.getAttribute("href");
    await first.click();
    await expect(page).toHaveURL(new RegExp(`${href}/?$`));
    await expect(page.locator("h1").first()).toBeVisible();
  });

  await step("news has an RSS feed", async () => {
    const resp = await page.request.get("/news/rss.xml");
    expect(resp.status()).toBe(200);
    const xml = await resp.text();
    expect(xml).toContain("<rss");
    expect(xml).toMatch(/<item>[\s\S]*<link>http:\/\/localhost:4321\/news\//);
    await page.goto("/news");
  });

  await step("unknown pages show a not-found page", async () => {
    const resp = await page.goto("/no-such-page");
    expect(resp?.status()).toBe(404);
    await expect(
      page.getByRole("heading", { name: "Page not found" }),
    ).toBeVisible();
  });

  await step("the not-found page links back into the site", async () => {
    await page.getByRole("link", { name: "Committees" }).last().click();
    await expect(page).toHaveURL(/\/committees\/?$/);
  });
});
