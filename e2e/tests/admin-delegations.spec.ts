import { expect, test } from "../fixtures";
import { e2eSchool } from "../support/seed";

test("admin opens a pending delegation from the list", async ({
  page,
  signInAs,
  step,
}) => {
  await signInAs("admin");

  const item = page.getByRole("listitem").filter({ hasText: e2eSchool });

  await step("open pending delegations", async () => {
    await page.getByRole("button", { name: /Pending Delegations/ }).click();
    await expect(page).toHaveURL(/\/admin\/delegations$/);
    await expect(item).toBeVisible();
  });

  await step("open delegation detail", async () => {
    await item.getByRole("link", { name: "Open" }).click();
    await expect(page).toHaveURL(/\/delegations\/[0-9a-f-]+$/);
    await expect(
      page.getByRole("heading", { name: "Delegation", exact: true }),
    ).toBeVisible();
    await expect(page.getByLabel("School name")).toHaveValue(e2eSchool);
    await expect(page.getByText("Pending review")).toBeVisible();
    await expect(page.locator("div.bg-red-50")).toBeHidden();
  });
});
