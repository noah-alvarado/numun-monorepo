import { expect, test } from "../fixtures";

// The limiters are token buckets that refill continuously, so a one-off burst
// is refilled by the time the page acts. drain empties the bucket (resolving
// once a call is refused) and keeps it empty until stop() is called.
async function drain(
  fire: () => Promise<{ status(): number }>,
): Promise<() => Promise<void>> {
  let on = true;
  let empty!: () => void;
  const drained = new Promise<void>((r) => (empty = r));
  const loop = (async () => {
    while (on) {
      const statuses = (
        await Promise.all(Array.from({ length: 10 }, fire))
      ).map((r) => r.status());
      if (statuses.includes(429)) empty();
    }
  })();
  await drained;
  return async () => {
    on = false;
    await loop;
  };
}
import { putCognitoUser } from "../support/fakes";

// Rate limits (SECURITY.md §3). The counters are per API process and per
// minute, so each test uses its own user or viewer IP. Locally there is no
// CloudFront; tests stamp the viewer-IP header it would add.

test("a throttled IP gets an explanation and a retry, not wrong data", async ({
  page,
  step,
}) => {
  const email = `throttle.${Date.now()}@example.com`;
  const password = "throttle test pass 1";
  await putCognitoUser({ email, password, name: "Throttled Advisor" });
  const ip = `203.0.113.${Math.floor(Math.random() * 250) + 1}`;
  // Stamp the header CloudFront would add, on API calls only (on other
  // origins it would trip CORS preflights).
  await page.route(/\/(numun\.v1\.|v1\/)/, (route) =>
    route.continue({
      headers: { ...route.request().headers(), "x-numun-viewer-ip": ip },
    }),
  );

  await step("sign in", async () => {
    await page.goto("/sign-in");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill(password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByText("Throttled Advisor")).toBeVisible();
  });

  // The public conference lookup has the tight anonymous per-IP cap.
  const stop = await drain(() =>
    page.request.post("/numun.v1.PublicService/GetActiveConference", {
      headers: { "Connect-Protocol-Version": "1", "X-Numun-Viewer-Ip": ip },
      data: {},
    }),
  );
  try {
    await step("pages say to wait instead of 'no conference'", async () => {
      await page.goto("/delegation");
      await expect(
        page.getByText("Too many requests. Wait a minute and try again."),
      ).toBeVisible();
      await expect(page.getByText(/No active conference/)).toHaveCount(0);
      await expect(page.getByRole("button", { name: "Retry" })).toBeVisible();
    });
  } finally {
    await stop();
  }

  await step("retry works once the limit clears", async () => {
    await expect(async () => {
      await page.getByRole("button", { name: "Retry" }).click();
      await expect(
        page.getByRole("heading", { name: "My Delegation" }),
      ).toBeVisible({ timeout: 1000 });
    }).toPass({ timeout: 15_000 });
  });
});

test("a signed-in user over the per-user limit sees a clear message", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  await signInAs(newAdvisor);

  const stop = await drain(() =>
    page.request.post("/numun.v1.UserService/GetMe", {
      headers: { "Connect-Protocol-Version": "1" },
      data: {},
    }),
  );

  try {
    await step("pages explain the wait instead of misleading", async () => {
      await page.goto("/delegation");
      await expect(
        page.getByText("Too many requests. Wait a minute and try again."),
      ).toBeVisible();
      // Not signed out, and a failed load doesn't look like "no delegation".
      await expect(page).toHaveURL(/\/delegation$/);
      await expect(
        page.getByRole("heading", { name: "Register your school" }),
      ).toHaveCount(0);
      await expect(
        page.getByRole("button", { name: /^(Reload|Retry)$/ }),
      ).toBeVisible();
    });
  } finally {
    await stop();
  }
});
