// Every editor uses optimistic locking. When a save loses to a change made
// after the page loaded, the page refetches, shows the other change and says
// so, rather than overwriting it or leaving stale data on screen. Each test
// makes the competing change through the API while the editor is open.

import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import {
  createCommittee,
  createDelegate,
  createDelegation,
  createDelegationAward,
  createDraftConference,
  rpc,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";
import { seedUsers } from "../support/seed";

const admin = seedUsers.admin.id;

function modal(page: Page) {
  return page.locator("div.fixed.inset-0");
}

function row(page: Page, text: string) {
  return page.getByRole("row").filter({ hasText: text });
}

function conflictNotice(page: Page, what: string) {
  return page.getByText(`Someone else changed ${what} first.`);
}

test("committee and position edits that lose a race", async ({
  page,
  signInAs,
  step,
}) => {
  const conferenceId = await createDraftConference("Conflicts");
  const committeeId = await createCommittee(
    conferenceId,
    "Security Council",
    "NON_CRISIS",
    ["France"],
  );

  await signInAs("admin");
  await page.goto(`/conferences/${conferenceId}/committees`);

  await step("a committee renamed meanwhile", async () => {
    await row(page, "Security Council")
      .getByRole("button", { name: "Edit" })
      .click();
    await modal(page).getByLabel("Name").fill("Mine");
    const { committee } = await rpc<{ committee: { version: number } }>(
      admin,
      "CommitteeService/GetCommittee",
      { committeeId },
    );
    await rpc(admin, "CommitteeService/UpdateCommittee", {
      committeeId,
      name: "General Assembly",
      expectedVersion: committee.version,
    });
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();
    await expect(conflictNotice(page, "this committee")).toBeVisible();
    await expect(row(page, "General Assembly")).toBeVisible();
  });

  await step("a position renamed meanwhile", async () => {
    await row(page, "General Assembly")
      .getByRole("button", { name: "Expand" })
      .click();
    const position = page
      .locator("td[colspan='7'] table")
      .getByRole("row")
      .filter({ hasText: "France" });
    await position.getByRole("button", { name: "Edit" }).click();
    await modal(page).getByLabel("Max delegates").fill("2");
    const { items } = await rpc<{ items: { id: string; version: number }[] }>(
      admin,
      "PositionService/ListPositions",
      { committeeId },
    );
    await rpc(admin, "PositionService/UpdatePosition", {
      positionId: items[0].id,
      name: "French Republic",
      expectedVersion: items[0].version,
    });
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();
    await expect(conflictNotice(page, "this position")).toBeVisible();
    await expect(
      page.locator("td[colspan='7'] table").getByText("French Republic"),
    ).toBeVisible();
  });
});

test("an award edited meanwhile", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("AwardConflict"),
  );
  const original = `E2E Award ${Date.now()}`;
  const award = await createDelegationAward(original, delegation);

  await signInAs("admin");
  await page.goto("/awards");

  await step("save an award someone else renamed", async () => {
    await row(page, original).getByRole("button", { name: "Edit" }).click();
    await page.getByLabel("Category").fill("Mine");
    const { award: latest } = await rpc<{ award: { version: number } }>(
      admin,
      "AwardService/GetAward",
      { awardId: award.id },
    );
    await rpc(admin, "AwardService/UpdateAward", {
      awardId: award.id,
      name: `${original} (theirs)`,
      expectedVersion: latest.version,
    });
    await page.getByRole("button", { name: "Save" }).click();
    await expect(
      page.getByRole("heading", { name: "Edit award" }),
    ).toBeHidden();
    await expect(conflictNotice(page, "this award")).toBeVisible();
    await expect(row(page, `${original} (theirs)`)).toBeVisible();
  });
});

test("a conference edited meanwhile", async ({ page, signInAs, step }) => {
  const conferenceId = await createDraftConference("ConfConflict");
  const { conference } = await rpc<{
    conference: { name: string; version: number };
  }>(admin, "ConferenceService/GetConference", { conferenceId });
  const theirs = `${conference.name} (theirs)`;

  await signInAs("admin");
  await page.goto("/admin/conferences");

  await step("save a conference someone else renamed", async () => {
    await row(page, conference.name)
      .getByRole("button", { name: `Edit ${conference.name}` })
      .click();
    await page.getByRole("dialog").getByLabel("Edition").fill("98");
    await rpc(admin, "ConferenceService/UpdateConference", {
      conferenceId,
      name: theirs,
      expectedVersion: conference.version,
    });
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Save" })
      .click();
    await expect(page.getByRole("dialog")).toBeHidden();
    await expect(conflictNotice(page, "this conference")).toBeVisible();
    await expect(row(page, theirs)).toBeVisible();
  });
});

test("a profile edited in another tab", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  await signInAs(newAdvisor);
  await page.goto("/profile");

  await step("save over a change from another tab", async () => {
    await page.getByLabel("Phone").fill("312-555-0100");
    const { user } = await rpc<{ user: { version: number } }>(
      newAdvisor.id,
      "UserService/GetMe",
    );
    await rpc(newAdvisor.id, "UserService/UpdateUser", {
      userId: newAdvisor.id,
      name: `${newAdvisor.name} Jr.`,
      expectedVersion: user.version,
    });
    await page.getByRole("button", { name: "Save profile" }).click();
    await expect(conflictNotice(page, "your profile")).toBeVisible();
    await expect(page.getByLabel("Name")).toHaveValue(`${newAdvisor.name} Jr.`);
    await expect(page.getByLabel("Phone")).toHaveValue("");
  });

  await step("saving again keeps both changes", async () => {
    await page.getByLabel("Phone").fill("312-555-0100");
    await page.getByRole("button", { name: "Save profile" }).click();
    await expect(page.getByText("Profile saved.")).toBeVisible();
    await page.reload();
    await expect(page.getByLabel("Name")).toHaveValue(`${newAdvisor.name} Jr.`);
    await expect(page.getByLabel("Phone")).not.toHaveValue("");
  });
});

test("a roster row edited meanwhile", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("RosterConflict"),
  );
  const surname = uniqueSurname();
  const delegate = await createDelegate(
    newAdvisor.id,
    delegation.id,
    "Ada",
    surname,
  );

  await signInAs(newAdvisor);
  await page.goto("/delegation");
  await page.getByRole("button", { name: "Delegates", exact: true }).click();

  await step("save a delegate a co-advisor just renamed", async () => {
    await page.getByRole("button", { name: `Edit Ada ${surname}` }).click();
    const edit = page.getByRole("form", { name: `Edit Ada ${surname}` });
    await edit.getByLabel("Email (optional)").fill("ada@example.com");
    await rpc(newAdvisor.id, "DelegateService/UpdateDelegate", {
      delegateId: delegate.id,
      firstName: "Adele",
      expectedVersion: delegate.version,
    });
    await edit.getByRole("button", { name: "Save" }).click();
    await expect(edit).toBeHidden();
    await expect(conflictNotice(page, "this roster")).toBeVisible();
    await expect(row(page, `Adele ${surname}`)).toBeVisible();
  });
});

test("approving a delegation its advisor just edited", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("ApproveConflict"),
  );
  const renamed = `${delegation.school} Upper School`;

  await signInAs("admin");
  await page.goto("/admin/delegations");
  const item = (school: string) =>
    page.getByRole("listitem").filter({ hasText: school });

  await step("approve a stale row", async () => {
    await expect(item(delegation.school)).toBeVisible();
    await rpc(newAdvisor.id, "DelegationService/UpdateDelegation", {
      delegationId: delegation.id,
      school: renamed,
      expectedVersion: delegation.version,
    });
    await item(delegation.school)
      .getByRole("button", { name: "Approve" })
      .click();
    await expect(conflictNotice(page, "this delegation")).toBeVisible();
    await expect(item(renamed)).toBeVisible();
  });

  await step("approve the refreshed row", async () => {
    await item(renamed).getByRole("button", { name: "Approve" }).click();
    await expect(item(renamed)).toHaveCount(0);
  });
});
