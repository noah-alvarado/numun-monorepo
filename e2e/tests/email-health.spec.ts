import { expect, test } from "../fixtures";

// PROCEDURES_ADMIN.md §11. Listing and unsuppressing need a bounced or
// complained user, which only the SNS feedback Lambda can produce today, so
// this covers access and the healthy state.

test("email health is admin-only and loads", async ({
  page,
  signInAs,
  step,
}) => {
  await signInAs("advisor");
  await step("advisor is turned away", async () => {
    await page.goto("/admin/email-health");
    await expect(page.getByText("Admin only.")).toBeVisible();
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
