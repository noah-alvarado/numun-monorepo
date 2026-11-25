import { expect, test } from "../fixtures";
import {
  createDelegation,
  createDelegationAward,
  uniqueSchool,
} from "../support/api";
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

test("admin edits an award", async ({ page, signInAs, newAdvisor, step }) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("AwardEdit"),
  );
  const original = `E2E Award ${Date.now()}`;
  const renamed = `${original} (edited)`;
  await createDelegationAward(original, delegation);

  await signInAs("admin");

  await step("edit name and category", async () => {
    await page.goto("/awards");
    await expect(page.getByText(seedConferenceName)).toBeVisible();
    await page
      .getByRole("row", { name: new RegExp(original) })
      .getByRole("button", { name: "Edit" })
      .click();
    await expect(
      page.getByRole("heading", { name: "Edit award" }),
    ).toBeVisible();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
      original,
    );
    await page.getByLabel("Name", { exact: true }).fill(renamed);
    await page.getByLabel("Category").fill("Outstanding Delegation");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(
      page.getByRole("heading", { name: "Edit award" }),
    ).toBeHidden();
    await expect(page.locator("p.bg-red-50")).toBeHidden();
  });

  await step("edit persists across reload", async () => {
    await page.reload();
    const row = page.getByRole("row").filter({ hasText: renamed });
    await expect(row).toContainText("Outstanding Delegation");
    await expect(row).toContainText(delegation.school);
  });
});

test("advisor sees and dismisses a congratulations banner", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("AwardHero"),
  );
  const name = `E2E Best Delegation ${Date.now()}`;
  await createDelegationAward(name, delegation);

  await signInAs(newAdvisor);

  const hero = page
    .getByRole("heading", { name: "Congratulations!" })
    .locator("..");

  await step("advisor sees their award", async () => {
    await page.goto("/awards");
    await expect(hero).toContainText(name);
    await expect(page.getByRole("button", { name: "New award" })).toHaveCount(
      0,
    );
    await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
  });

  await step("dismiss the banner", async () => {
    await hero.getByRole("button", { name: "Dismiss" }).click();
    await expect(
      page.getByRole("heading", { name: "Congratulations!" }),
    ).toHaveCount(0);
  });

  await step("dismissal persists", async () => {
    await page.reload();
    await expect(
      page.getByRole("row", { name: new RegExp(name) }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Congratulations!" }),
    ).toHaveCount(0);
  });
});
