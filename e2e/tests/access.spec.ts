import { expect, test } from "../fixtures";
import { createAdvisor, createDelegation, uniqueSchool } from "../support/api";
import { seedUsers } from "../support/seed";

const dashboardCards = [
  "My Delegation",
  "Pending Delegations",
  "Committees",
  "Assignment Studio",
  "Day-of Check-In",
  "Awards",
  "Announcements",
  "Exports",
  "Conferences",
  "Staff",
  "Email Health",
] as const;

async function expectCards(
  page: import("@playwright/test").Page,
  visible: readonly (typeof dashboardCards)[number][],
) {
  for (const title of dashboardCards) {
    const card = page.getByRole("button", { name: new RegExp(`^${title}`) });
    if (visible.includes(title)) await expect(card).toBeVisible();
    else await expect(card).toHaveCount(0);
  }
}

test("dashboard shows each role its own tools", async ({
  page,
  signInAs,
  step,
}) => {
  await signInAs("advisor");
  await step("advisor dashboard", async () => {
    await expect(page.getByText("Advisor", { exact: true })).toBeVisible();
    await expectCards(page, ["My Delegation", "Awards", "Exports"]);
  });

  await signInAs("staffer");
  await step("staffer dashboard", async () => {
    await expect(page.getByText("Staffer", { exact: true })).toBeVisible();
    await expectCards(page, ["Day-of Check-In", "Awards", "Exports"]);
  });

  await signInAs("admin");
  await step("admin dashboard", async () => {
    await expect(
      page.getByText("Administrator", { exact: true }),
    ).toBeVisible();
    await expectCards(page, [
      "Pending Delegations",
      "Committees",
      "Assignment Studio",
      "Day-of Check-In",
      "Awards",
      "Announcements",
      "Exports",
      "Conferences",
      "Staff",
      "Email Health",
    ]);
  });

  await step("admin cards open their pages", async () => {
    for (const [title, url, heading] of [
      ["Day-of Check-In", "/admin/check-in", "Day-of Check-In"],
      ["Awards", "/awards", "Awards"],
      ["Announcements", "/admin/announcements", "Announcements"],
      ["Exports", "/exports", "Exports"],
      ["Conferences", "/admin/conferences", "Conferences"],
      ["Staff", "/admin/staff", "Staff"],
      ["Email Health", "/admin/email-health", "Email Health"],
    ] as const) {
      await page.goto("/dashboard");
      await page.getByRole("button", { name: new RegExp(`^${title}`) }).click();
      await expect(page).toHaveURL(url);
      await expect(
        page.getByRole("heading", { level: 1, name: heading }),
      ).toBeVisible();
      await expect(page.getByText(/^Loading/)).toHaveCount(0);
    }
  });
});

test("advisor cannot use the admin pending list", async ({
  page,
  signInAs,
  step,
}) => {
  await signInAs("advisor");
  await step("open admin page as advisor", async () => {
    await page.goto("/admin/delegations");
    await expect(page.getByText("Admin only.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Approve" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Reload" })).toHaveCount(0);
  });
});

test("another advisor's delegation is not found", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const owner = await createAdvisor("Owner");
  const theirs = await createDelegation(owner.id, uniqueSchool("Scoped"));

  await signInAs(newAdvisor);

  await step("open someone else's delegation", async () => {
    // AUTH.md §7.3: out-of-scope reads look like missing rows, not 403s.
    const resp = await page.request.post(
      "/numun.v1.DelegationService/GetDelegation",
      {
        headers: { "Connect-Protocol-Version": "1" },
        data: { delegationId: theirs.id },
      },
    );
    expect(resp.status()).toBe(404);
    expect(((await resp.json()) as { code: string }).code).toBe("not_found");

    await page.goto(`/delegations/${theirs.id}`);
    await expect(page.getByText("Delegation not found.")).toBeVisible();
    await expect(page.getByLabel("School name")).toHaveCount(0);
  });
});

test("an invalid session sends the user to sign-in", async ({
  page,
  context,
  signInAs,
  step,
}) => {
  await signInAs("advisor");

  await step("session no longer exists server-side", async () => {
    const [session] = (await context.cookies()).filter(
      (c) => c.name === "numun_session",
    );
    await context.addCookies([{ ...session, value: "revoked-session-id" }]);
    await page.goto("/delegation");
    await expect(page).toHaveURL(/\/sign-in\?ended=1$/);
    await expect(
      page.getByText("Your session ended. Sign in again to continue."),
    ).toBeVisible();
    const cookies = await context.cookies();
    expect(cookies.find((c) => c.name === "numun_session")).toBeUndefined();
  });
});

test("mutations without the CSRF token are rejected", async ({
  page,
  context,
  signInAs,
}) => {
  await signInAs("advisor");

  const csrf = (await context.cookies()).find((c) => c.name === "csrf_token");
  expect(csrf?.value).toBeTruthy();

  const me = await page.request.post("/numun.v1.UserService/GetMe", {
    headers: { "Connect-Protocol-Version": "1" },
    data: {},
  });
  const { user } = (await me.json()) as { user: { version: number } };
  const update = (headers: Record<string, string>) =>
    page.request.post("/numun.v1.UserService/UpdateUser", {
      headers: { "Connect-Protocol-Version": "1", ...headers },
      data: {
        userId: seedUsers.advisor.id,
        name: seedUsers.advisor.name,
        expectedVersion: user.version,
      },
    });

  expect((await update({})).status()).toBe(403);
  expect((await update({ "X-CSRF-Token": "forged" })).status()).toBe(403);
  expect((await update({ "X-CSRF-Token": csrf!.value })).status()).toBe(200);
});
