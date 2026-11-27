import { defineConfig, devices } from "@playwright/test";

// Runs the site smoke tests (site/site.spec.ts) against a deployed site:
// .github/workflows/site.yml after each test/prod deploy and
// site-preview.yml against each PR preview. Needs DEPLOYED_SITE_URL and
// SITE_APEX (the environment whose portal the site links to).

if (!process.env.DEPLOYED_SITE_URL || !process.env.SITE_APEX) {
  throw new Error("set DEPLOYED_SITE_URL and SITE_APEX");
}

export default defineConfig({
  testDir: "./site",
  testMatch: "site.spec.ts",
  // Crawling every page and image over the network takes a while.
  timeout: 300_000,
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // Only for network blips between the runner and CloudFront.
  retries: process.env.CI ? 1 : 0,
  reporter: [
    [process.env.CI ? "github" : "list"],
    ["html", { open: "never", outputFolder: "playwright-report-deployed" }],
  ],
  outputDir: "test-results-deployed",
  use: {
    baseURL: process.env.DEPLOYED_SITE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    viewport: { width: 1280, height: 900 },
  },
  projects: [{ name: "deployed-site", use: { ...devices["Desktop Chrome"] } }],
});
