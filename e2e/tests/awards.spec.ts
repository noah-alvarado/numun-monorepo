import { expect, test } from "../fixtures";
import {
  assignStaffer,
  createAdvisor,
  createDelegate,
  createDelegation,
  createDelegationAward,
  rpc,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";
import { committedFile, failGitHub } from "../support/fakes";
import {
  seedConferenceId,
  seedConferenceName,
  seedUsers,
} from "../support/seed";

// A GitHub outage queued by a failed test mustn't leak into the next one.
test.afterEach(() => failGitHub(0));

test("admin creates and deletes an award", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const awardName = `E2E Award ${Date.now()}`;
  const school = uniqueSchool("AwardPick");
  const surname = uniqueSurname();
  const delegation = await createDelegation(newAdvisor.id, school);
  await createDelegate(newAdvisor.id, delegation.id, "Ada", surname);
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
  });

  await step("pick a delegate by name", async () => {
    await page.getByLabel("Search delegates by name").fill(surname);
    await page
      .getByRole("button", { name: `Ada ${surname} — ${school}` })
      .click();
    await expect(
      page.getByText(`Ada ${surname}`, { exact: true }),
    ).toBeVisible();
    await expect(page.getByLabel("Search delegates by name")).toHaveCount(0);
  });

  await step("add the conference as a second recipient", async () => {
    await page.getByRole("button", { name: "Add recipient" }).click();
    await page
      .getByLabel("Recipient type")
      .last()
      .selectOption({ label: "conference" });
    await page
      .getByLabel("Recipient", { exact: true })
      .selectOption({ label: seedConferenceName });
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
    await expect(row).toContainText(`Ada ${surname} (delegate)`);
    await expect(row).toContainText(`${seedConferenceName} (conference)`);
  });

  await step("confirm delete", async () => {
    await row.getByRole("button", { name: "Delete" }).click();
    const modal = page
      .getByText(`This will delete "${awardName}"`)
      .locator("..");
    await expect(modal).toBeVisible();
    await modal.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(modal).toBeHidden();
    await expect(
      page.getByText("Deleted and synced to the public site."),
    ).toBeVisible();
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
    await expect(
      page.getByLabel("Recipient", { exact: true }).locator("option:checked"),
    ).toHaveText(delegation.school);
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
    // Advisors can't edit anything, so there's no Actions column.
    await expect(
      page.getByRole("columnheader", { name: "Actions" }),
    ).toHaveCount(0);
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
  const recipientRows = modal.getByLabel("Recipient type");

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
    await expect(recipientRows.first().locator("option")).toHaveText([
      "delegate",
      "delegation",
    ]);
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

  await step("only their own delegations are offered", async () => {
    await modal.getByLabel("Name", { exact: true }).fill(stafferAward);
    await modal.getByLabel("Category").fill("Delegation Awards");
    await recipientRows.selectOption({ label: "delegation" });
    const options = modal
      .getByLabel("Recipient", { exact: true })
      .locator("option");
    await expect(options.filter({ hasText: mine.school })).toHaveCount(1);
    await expect(options.filter({ hasText: other.school })).toHaveCount(0);
    // The API enforces the same scope.
    await expect(
      rpc(seedUsers.staffer.id, "AwardService/CreateAward", {
        conferenceId: seedConferenceId,
        name: stafferAward,
        category: "Delegation Awards",
        recipients: [
          {
            kind: "AWARD_RECIPIENT_KIND_DELEGATION",
            id: other.id,
            displayName: other.school,
          },
        ],
      }),
    ).rejects.toThrow(/permission_denied|not_found/);
  });

  await step("an award for their own delegation saves", async () => {
    await modal
      .getByLabel("Recipient", { exact: true })
      .selectOption({ label: mine.school });
    await modal.getByRole("button", { name: "Save" }).click();
    await expect(modal).toBeHidden();
    const row = page.getByRole("row").filter({ hasText: stafferAward });
    await expect(row).toContainText(mine.school);
    await expect(row.getByRole("button", { name: "Edit" })).toBeVisible();
  });
});

test("awards reach the public site, and outages are explained", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("AwardSync"),
  );
  const name = `E2E Synced Award ${Date.now()}`;
  const award = await createDelegationAward(name, delegation);
  const file = `content/awards-archive/${award.id}.md`;
  const row = () => page.getByRole("row", { name: new RegExp(name) });

  await signInAs("admin");
  await page.goto("/awards");

  async function rename(category: string) {
    await row().getByRole("button", { name: "Edit" }).click();
    await page.getByLabel("Category").fill(category);
    await page.getByRole("button", { name: "Save" }).click();
    await expect(
      page.getByRole("heading", { name: "Edit award" }),
    ).toBeHidden();
  }

  async function remove() {
    await row().getByRole("button", { name: "Delete" }).click();
    await page
      .getByText(`This will delete "${name}"`)
      .locator("..")
      .getByRole("button", { name: "Delete", exact: true })
      .click();
  }

  await step("a new award is committed to the site's content", async () => {
    expect(await committedFile(file)).toContain(`awardName: "${name}"`);
    expect(await committedFile(file)).toContain(
      `displayName: "${delegation.school}"`,
    );
  });

  await step(
    "an edit during a GitHub outage is saved but not published",
    async () => {
      await failGitHub(100);
      await rename("Outstanding Delegation");
      await expect(
        page.getByText(
          "Saved, but the public site wasn't updated. Save the award again to retry.",
        ),
      ).toBeVisible();
      await expect(
        page.getByText("GitHub returned an error on all 3 tries."),
      ).toBeVisible();
      await expect(row()).toContainText("Outstanding Delegation");
      expect(await committedFile(file)).not.toContain("Outstanding Delegation");
    },
  );

  await step("saving again once GitHub is back publishes it", async () => {
    await failGitHub(0);
    await rename("Outstanding Delegation");
    await expect(
      page.getByText("Saved and synced to the public site."),
    ).toBeVisible();
    expect(await committedFile(file)).toContain(
      'category: "Outstanding Delegation"',
    );
  });

  await step(
    "a delete during an outage leaves the award to retry",
    async () => {
      await failGitHub(100);
      await remove();
      await expect(
        page.getByText(/Not deleted: the award couldn't be taken off/),
      ).toBeVisible();
      await expect(row()).toBeVisible();
      expect(await committedFile(file)).not.toBeNull();
    },
  );

  await step("deleting again takes it off the site", async () => {
    await failGitHub(0);
    await remove();
    await expect(
      page.getByText("Deleted and synced to the public site."),
    ).toBeVisible();
    await expect(row()).toHaveCount(0);
    expect(await committedFile(file)).toBeNull();
  });
});
