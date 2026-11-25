import { expect, test } from "../fixtures";
import { seedConferenceId, seedConferenceName } from "../support/seed";

test("admin creates and deletes an award", async ({ page, signInAs, step }) => {
  const awardName = `E2E Award ${Date.now()}`;
  const errorBanner = page.locator("p.bg-red-50");

  // The award list is keyed on the active conference, which loads after the
  // user; wait for it so an empty list can't pass for a loaded one.
  async function waitForAwardsLoaded() {
    await expect(page.getByText(seedConferenceName)).toBeVisible();
    await expect(page.getByText("Loading awards…")).toBeHidden();
  }

  await signInAs("admin");

  await step("open awards", async () => {
    await page.goto("/awards");
    await expect(page.getByRole("heading", { name: "Awards" })).toBeVisible();
    await waitForAwardsLoaded();
  });

  await step("fill new award form", async () => {
    await page.getByRole("button", { name: "New award" }).click();
    await expect(
      page.getByRole("heading", { name: "Create award" }),
    ).toBeVisible();
    await page.getByLabel("Name", { exact: true }).fill(awardName);
    await page.getByLabel("Category").fill("Best Position Paper");
    await page.getByRole("combobox").selectOption({ label: "conference" });
    await page.getByPlaceholder("ID", { exact: true }).fill(seedConferenceId);
    await page.getByPlaceholder("Display name (optional)").fill("E2E Seed");
  });

  const row = page.getByRole("row", { name: new RegExp(awardName) });

  await step("save award", async () => {
    await page.getByRole("button", { name: "Save" }).click();
    await expect(
      page.getByRole("heading", { name: "Create award" }),
    ).toBeHidden();
    await expect(errorBanner).toBeHidden();
    await expect(page.getByText("Saved and synced")).toBeVisible();
    await expect(row).toBeVisible();
    await expect(row).toContainText("Best Position Paper");
    await expect(row).toContainText("(conference)");
  });

  await step("confirm delete", async () => {
    await row.getByRole("button", { name: "Delete" }).click();
    const modal = page
      .getByText(`This will delete "${awardName}"`)
      .locator("..");
    await expect(modal).toBeVisible();
    await modal.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(modal).toBeHidden();
  });

  await step("award is gone", async () => {
    await expect(errorBanner).toBeHidden();
    await expect(row).toHaveCount(0);
    await page.reload();
    await waitForAwardsLoaded();
    await expect(page.getByText(awardName)).toHaveCount(0);
  });
});
