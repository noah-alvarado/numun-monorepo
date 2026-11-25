import { defineConfig, devices } from "@playwright/test";

// The suite drives the built portal (vite preview) against the Go API running
// as a plain HTTP server (LOCAL_HTTP=true). DynamoDB Local + LocalStack come
// from docker compose and must already be up and seeded — `make e2e` does it.
//
// The preview server proxies API paths to :3000 (portal/vite.config.ts), so
// the portal and API share one origin: no CORS (API Gateway adds that in
// prod) and the SameSite=Strict session cookie behaves like it does on
// portal.numun.org → api.numun.org.

const isCI = !!process.env.CI;
const PORTAL_URL = "http://localhost:4173";
const API_URL = "http://localhost:3000";

const apiEnv = {
  LOCAL_HTTP: "true",
  DEV_MODE: "true",
  DEV_BYPASS_AUTH: "true",
  AWS_REGION: "us-east-2",
  AWS_ACCESS_KEY_ID: "local",
  AWS_SECRET_ACCESS_KEY: "local",
  // Catch-all so nothing (e.g. the CMS SSM lookup) reaches real AWS.
  AWS_ENDPOINT_URL: "http://localhost:4566",
  AWS_ENDPOINT_URL_DYNAMODB: "http://localhost:8000",
  DDB_TABLE_NAME: "numun-test",
  UPLOADS_BUCKET_NAME: "numun-test-uploads",
  EMAIL_SENDER_TRANSACTIONAL: "noreply@mail.test.numun.org",
  EMAIL_SENDER_ANNOUNCEMENTS: "announcements@mail.test.numun.org",
  EMAIL_QUEUE_URL: "http://localhost:4566/000000000000/numun-test-email-send",
  EMAIL_UNSUBSCRIBE_BASE_URL: `${API_URL}/v1/email/unsubscribe`,
  EMAIL_UNSUBSCRIBE_SECRET: "dev-unsubscribe-secret-not-for-prod",
  PORTAL_BASE_URL: PORTAL_URL,
  ASSETS_BASE_URL: "http://localhost:4566/numun-test-assets",
};

const portalEnv = {
  VITE_DEV_BYPASS: "true",
  VITE_API_BASE_URL: PORTAL_URL,
};

export default defineConfig({
  testDir: "./tests",
  globalSetup: "./global-setup.ts",
  // Tests share one DynamoDB table and the same seed users; run them serially.
  fullyParallel: false,
  workers: 1,
  forbidOnly: isCI,
  retries: isCI ? 1 : 0,
  reporter: [
    [isCI ? "github" : "list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  outputDir: "test-results",
  use: {
    baseURL: PORTAL_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
    viewport: { width: 1280, height: 900 },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      name: "api",
      command: "go run ./cmd/api",
      cwd: "../api",
      env: apiEnv,
      url: `${API_URL}/v1/health`,
      reuseExistingServer: !isCI,
      timeout: 180_000,
      stdout: "ignore",
      stderr: "pipe",
    },
    {
      name: "portal",
      // A separate outDir keeps the dev-bypass build away from portal/dist.
      command:
        "pnpm exec vite build --outDir dist-e2e --emptyOutDir --logLevel error && pnpm exec vite preview --outDir dist-e2e",
      cwd: "../portal",
      env: portalEnv,
      url: PORTAL_URL,
      reuseExistingServer: !isCI,
      timeout: 180_000,
      stdout: "ignore",
      stderr: "pipe",
    },
  ],
});
