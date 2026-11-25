import { test as base, expect, type Page } from "@playwright/test";

import { createAdvisor, type TestUser } from "./support/api";
import { seedUsers, type SeedRole } from "./support/seed";

type StepFn = <T>(
  title: string,
  body: () => Promise<T>,
  // Page to screenshot; defaults to the test's page.
  shot?: Page,
) => Promise<T>;

type Fixtures = {
  // Wraps test.step and attaches a full-page screenshot once the step's body
  // finishes. Files land in test-results/<test>/NN-<slug>.png as well as the
  // HTML report, so they can be reviewed without opening the report.
  step: StepFn;
  // Starts a real cookie session via the dev-bypass branch of
  // AuthService.Exchange (no Cognito), then lands on /dashboard.
  signInAs: (who: SeedRole | TestUser, page?: Page) => Promise<void>;
  // A fresh advisor owned by this test, for flows that would otherwise
  // mutate the shared seed advisor's delegation.
  newAdvisor: TestUser;
};

export const test = base.extend<Fixtures>({
  step: async ({ page }, use, testInfo) => {
    let n = 0;
    await use(<T>(title: string, body: () => Promise<T>, shot = page) =>
      base.step(title, async () => {
        const result = await body();
        n += 1;
        const name = `${String(n).padStart(2, "0")}-${slug(title)}`;
        const path = testInfo.outputPath(`${name}.png`);
        await shot.screenshot({ path, fullPage: true });
        await testInfo.attach(name, { path, contentType: "image/png" });
        return result;
      }),
    );
  },

  signInAs: async ({ page: defaultPage }, use) => {
    await use(async (who, page = defaultPage) => {
      const user = typeof who === "string" ? seedUsers[who] : who;
      await exchangeSession(page, user.id);
      await page.goto("/dashboard");
      await expect(page.getByText(user.name)).toBeVisible();
    });
  },

  newAdvisor: async ({}, use) => {
    await use(await createAdvisor("Advisor"));
  },
});

export { expect };

async function exchangeSession(page: Page, userId: string) {
  // page.request shares the browser context's cookie jar, so the
  // numun_session + csrf_token cookies set here are what the portal uses.
  const resp = await page.request.post("/numun.v1.AuthService/Exchange", {
    headers: { "Connect-Protocol-Version": "1" },
    data: {
      idToken: "dev-token",
      accessToken: `dev-${userId}`,
      refreshToken: "dev-rt",
      expiresIn: 3600,
    },
  });
  expect(resp.ok(), await resp.text()).toBe(true);
}

function slug(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 60);
}
