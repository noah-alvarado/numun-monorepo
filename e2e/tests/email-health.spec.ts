import { expect, test } from "../fixtures";
import { createAdvisor, setEmailStatus } from "../support/api";

// PROCEDURES_ADMIN.md §11, EMAIL.md §9. Suppression normally comes from the
// SES feedback Lambda; setEmailStatus stands in for it locally.

test("email health is admin-only and loads", async ({
  page,
  signInAs,
  step,
}) => {
  await signInAs("advisor");
  await step("advisor is turned away", async () => {
    await page.goto("/admin/email-health");
    await expect(page.getByText("Admin only.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Reload" })).toHaveCount(0);
  });

  await signInAs("admin");
  await step("admin sees the suppression list", async () => {
    await page.goto("/admin/email-health");
    await expect(
      page.getByRole("heading", { name: "Email Health" }),
    ).toBeVisible();
    await expect(
      page
        .getByText("No suppressed users. Inbox is healthy.")
        .or(page.getByRole("columnheader", { name: "Status" })),
    ).toBeVisible();
    await expect(page.locator("p.bg-red-50")).toHaveCount(0);
  });
});

test("admin unsuppresses bounced and complained users", async ({
  page,
  signInAs,
  step,
}) => {
  // Dev-bypass advisors have no email address, so rows are matched by name.
  const bounced = await createAdvisor("Bounced");
  const complained = await createAdvisor("Complained");
  await setEmailStatus(bounced.id, "bounced");
  const row = (name: string) => page.getByRole("row").filter({ hasText: name });

  await signInAs("admin");

  await step("bounced user is listed", async () => {
    await page.goto("/admin/email-health");
    await expect(row(bounced.name)).toContainText("bounced");
    await expect(row(complained.name)).toHaveCount(0);
  });

  await step("reload picks up a new complaint", async () => {
    await setEmailStatus(complained.id, "complained");
    await page.getByRole("button", { name: "Reload" }).click();
    await expect(row(complained.name)).toContainText("complained");
  });

  await step("unsuppress removes them from the list", async () => {
    for (const u of [bounced, complained]) {
      await row(u.name).getByRole("button", { name: "Unsuppress" }).click();
      await expect(row(u.name)).toHaveCount(0);
    }
    await expect(page.locator("p.bg-red-50")).toHaveCount(0);
  });

  await step("they stay unsuppressed after a reload", async () => {
    await page.reload();
    await expect(
      page.getByRole("heading", { name: "Email Health" }),
    ).toBeVisible();
    await expect(
      page
        .getByText("No suppressed users. Inbox is healthy.")
        .or(page.getByRole("columnheader", { name: "Status" })),
    ).toBeVisible();
    await expect(row(bounced.name)).toHaveCount(0);
    await expect(row(complained.name)).toHaveCount(0);
  });
});
