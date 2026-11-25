import { expect, test } from "../fixtures";
import {
  assignStaffer,
  createAdvisor,
  createDelegation,
  createDelegationAward,
  uniqueSchool,
} from "../support/api";
import {
  seedConferenceId,
  seedConferenceName,
  seedUsers,
} from "../support/seed";

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

test("staffer awards only within their delegations", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const mine = await createDelegation(newAdvisor.id, uniqueSchool("Mine"));
  await assignStaffer(mine.id, seedUsers.staffer.id);
  const other = await createDelegation(
    (await createAdvisor("OtherAward")).id,
    uniqueSchool("NotMine"),
  );
  const adminAward = `E2E Admin Award ${Date.now()}`;
  await createDelegationAward(adminAward, other);
  const stafferAward = `E2E Staffer Award ${Date.now()}`;
  const modal = page
    .getByRole("heading", { name: "Create award" })
    .locator("../..");
  const recipientRows = modal.getByPlaceholder("ID", { exact: true });

  await signInAs("staffer");

  await step("staffer sees all awards but can't edit admin ones", async () => {
    await page.goto("/awards");
    await expect(page.getByText(seedConferenceName)).toBeVisible();
    const row = page.getByRole("row").filter({ hasText: adminAward });
    await expect(row).toBeVisible();
    await expect(row.getByRole("button", { name: "Edit" })).toHaveCount(0);
    await expect(row.getByRole("button", { name: "Delete" })).toHaveCount(0);
  });

  await step("recipient kinds are limited for staffers", async () => {
    await page.getByRole("button", { name: "New award" }).click();
    await expect(
      modal.getByRole("combobox").first().locator("option"),
    ).toHaveText(["delegate", "delegation", "user"]);
  });

  await step("recipient rows can be added and removed", async () => {
    await modal.getByRole("button", { name: "Add recipient" }).click();
    await expect(recipientRows).toHaveCount(2);
    await modal
      .getByRole("button", { name: "Remove recipient" })
      .last()
      .click();
    await expect(recipientRows).toHaveCount(1);
  });

  await step("an award outside their scope is refused", async () => {
    await modal.getByLabel("Name", { exact: true }).fill(stafferAward);
    await modal.getByLabel("Category").fill("Delegation Awards");
    await modal.getByRole("combobox").selectOption({ label: "delegation" });
    await recipientRows.fill(other.id);
    await modal.getByRole("button", { name: "Save" }).click();
    await expect(page.locator("p.bg-red-50")).toContainText(
      "You don't have permission",
    );
  });

  await step("an award for their own delegation saves", async () => {
    await recipientRows.fill(mine.id);
    await modal.getByPlaceholder("Display name (optional)").fill(mine.school);
    await modal.getByRole("button", { name: "Save" }).click();
    await expect(modal).toBeHidden();
    const row = page.getByRole("row").filter({ hasText: stafferAward });
    await expect(row).toContainText(mine.school);
    await expect(row.getByRole("button", { name: "Edit" })).toBeVisible();
  });
});
