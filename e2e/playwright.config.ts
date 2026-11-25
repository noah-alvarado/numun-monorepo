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
const SITE_URL = "http://127.0.0.1:4322";
const API_URL = "http://localhost:3000";
// Fake Google Sheets + Cognito (fakes/server.ts).
export const FAKES_URL = "http://127.0.0.1:4599";
const COGNITO_POOL_ID = "us-east-2_e2eFake";
const COGNITO_CLIENT_ID = "e2efakeclient";

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
  // Also switches presigned URLs to path-style, which LocalStack needs.
  AWS_ENDPOINT_URL_S3: "http://localhost:4566",
  DDB_TABLE_NAME: "numun-test",
  UPLOADS_BUCKET_NAME: "numun-test-uploads",
  EMAIL_SENDER_TRANSACTIONAL: "noreply@mail.test.numun.org",
  EMAIL_SENDER_ANNOUNCEMENTS: "announcements@mail.test.numun.org",
  EMAIL_QUEUE_URL: "http://localhost:4566/000000000000/numun-test-email-send",
  EMAIL_UNSUBSCRIBE_BASE_URL: `${API_URL}/v1/email/unsubscribe`,
  EMAIL_UNSUBSCRIBE_SECRET: "dev-unsubscribe-secret-not-for-prod",
  PORTAL_BASE_URL: PORTAL_URL,
  ASSETS_BASE_URL: "http://localhost:4566/numun-test-assets",
  DEV_SHEETS_ORIGIN: FAKES_URL,
  AWS_ENDPOINT_URL_COGNITO_IDENTITY_PROVIDER: FAKES_URL,
  COGNITO_USER_POOL_ID: COGNITO_POOL_ID,
  COGNITO_CLIENT_ID: COGNITO_CLIENT_ID,
  // ID tokens from the fake are verified against its JWKS.
  COGNITO_ISSUER_URL: `${FAKES_URL}/${COGNITO_POOL_ID}`,
  // Mail goes to Mailpit (docker compose) instead of SES; tests read it back
  // through its API (support/mail.ts).
  DEV_SMTP_ADDR: "localhost:1025",
  DEV_REGISTRATION_SUMMARY_DELAY: "1s",
  // Lets /cms-oauth/auth hand off to GitHub (the CMS test stops there).
  CMS_OAUTH_CLIENT_ID: "e2e-cms-client",
  CMS_OAUTH_CLIENT_SECRET: "e2e-cms-secret",
  CMS_OAUTH_STATE_SECRET: "e2e-cms-state-secret",
};

// The portal talks to the fake Cognito like it would to the real one, and
// the API verifies the fake's signed tokens.
const portalEnv = {
  VITE_DEV_BYPASS: "false",
  VITE_API_BASE_URL: PORTAL_URL,
  VITE_COGNITO_USER_POOL_ID: COGNITO_POOL_ID,
  VITE_COGNITO_CLIENT_ID: COGNITO_CLIENT_ID,
  VITE_COGNITO_ENDPOINT: FAKES_URL,
  VITE_SITE_BASE_URL: SITE_URL,
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
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    // The static landing site and the CMS bundle (site/), served like
    // CloudFront serves them.
    {
      name: "site",
      testDir: "./site",
      use: { ...devices["Desktop Chrome"], baseURL: SITE_URL },
    },
  ],
  webServer: [
    {
      name: "fakes",
      // The fake Cognito runs the real post-confirmation trigger binary.
      command:
        "go build -C ../api -o ../e2e/.bin/cognito-post-confirmation ./cmd/cognito-post-confirmation && node fakes/server.ts",
      env: {
        ...apiEnv,
        POST_CONFIRMATION_BIN: ".bin/cognito-post-confirmation",
      },
      url: `${FAKES_URL}/__control/health`,
      reuseExistingServer: !isCI,
      timeout: 180_000,
      stdout: "ignore",
      stderr: "pipe",
    },
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
      // Drains the LocalStack email queue like the SQS-triggered Lambda.
      // No readiness URL: it has no HTTP surface, and a second copy polling
      // the same queue is harmless.
      name: "email-worker",
      command: "go run ./cmd/email-worker -local",
      cwd: "../api",
      env: apiEnv,
      stdout: "ignore",
      stderr: "pipe",
    },
    {
      name: "site",
      command:
        "pnpm --filter @numun/site build >/dev/null && node e2e/fakes/static-site.ts",
      cwd: "..",
      url: SITE_URL,
      reuseExistingServer: !isCI,
      timeout: 180_000,
      stdout: "ignore",
      stderr: "pipe",
    },
    {
      name: "portal",
      // A separate outDir keeps the e2e build away from portal/dist.
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
