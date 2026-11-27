import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import { createCommittee, createDraftConference, rpc } from "../support/api";
import {
  seedConferenceId,
  seedConferenceName,
  seedUsers,
} from "../support/seed";

function modal(page: Page) {
  return page.locator("div.fixed.inset-0");
}

function committeeRow(page: Page, name: string) {
  return page.getByRole("row").filter({ hasText: name });
}

function positionRow(page: Page, name: string) {
  return page
    .locator("td[colspan='7'] table")
    .getByRole("row")
    .filter({ hasText: name });
}

test("admin manages committees and positions", async ({
  page,
  signInAs,
  step,
}) => {
  const name = `E2E Committee ${Date.now()}`;
  const renamed = `${name} (renamed)`;

  await signInAs("admin");

  await step("open committees from the dashboard", async () => {
    await page.getByRole("button", { name: /Committees/ }).click();
    await expect(page).toHaveURL(`/conferences/${seedConferenceId}/committees`);
    await expect(page.getByText("Loading committees…")).toBeHidden();
    await expect(page.locator("header")).toContainText(seedConferenceName);
    await expect(page.locator("header")).not.toContainText(seedConferenceId);
  });

  await step("create a crisis committee", async () => {
    await page.getByRole("button", { name: "Create committee" }).click();
    await modal(page).getByLabel("Name").fill(name);
    await modal(page).getByLabel("Type").selectOption({ label: "Crisis" });
    await modal(page).getByLabel("Size").selectOption({ label: "Small" });
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();
    await expect(committeeRow(page, name)).toContainText("Crisis");
    await expect(committeeRow(page, name)).toContainText("Small");
  });

  await step("add a standard and a dual position", async () => {
    await committeeRow(page, name)
      .getByRole("button", { name: "Expand" })
      .click();
    await expect(
      page.getByText("No positions yet for this committee."),
    ).toBeVisible();

    await page.getByRole("button", { name: "Add position" }).click();
    await modal(page).getByLabel("Name").fill("France");
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();

    await page.getByRole("button", { name: "Add position" }).click();
    await modal(page).getByLabel("Name").fill("Joint Chiefs");
    await modal(page).getByLabel("Dual delegation").check();
    await expect(modal(page).getByLabel("Max delegates")).toBeDisabled();
    await expect(modal(page).getByLabel("Max delegates")).toHaveValue("2");
    await modal(page)
      .getByLabel("Prestige tier")
      .selectOption({ label: "Elevated" });
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();

    await expect(positionRow(page, "France")).toContainText("1No");
    await expect(positionRow(page, "Joint Chiefs")).toContainText("2Yes");
    await expect(positionRow(page, "Joint Chiefs")).toContainText(/Elevated/i);
    // Positions column of the committee row.
    await expect(committeeRow(page, name).getByRole("cell").nth(4)).toHaveText(
      "2",
    );
  });

  await step("server rejects dual delegation without 2 seats", async () => {
    const { items } = await rpc<{ items: { id: string; name: string }[] }>(
      seedUsers.admin.id,
      "CommitteeService/ListCommittees",
      { conferenceId: seedConferenceId },
    );
    const committee = items.find((c) => c.name === name)!;
    await expect(
      rpc(seedUsers.admin.id, "PositionService/CreatePosition", {
        committeeId: committee.id,
        name: "Bad Dual",
        maxDelegates: 1,
        dualDelegation: true,
      }),
    ).rejects.toThrow(/invalid_argument/);
  });

  await step("rename the committee", async () => {
    await committeeRow(page, name)
      .getByRole("button", { name: "Edit" })
      .click();
    await modal(page).getByLabel("Name").fill(renamed);
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();
    await expect(committeeRow(page, renamed)).toBeVisible();
  });

  await step("edit a position's seats", async () => {
    await positionRow(page, "France")
      .getByRole("button", { name: "Edit" })
      .click();
    await expect(
      modal(page).getByRole("heading", { name: "Edit position" }),
    ).toBeVisible();
    await modal(page).getByLabel("Max delegates").fill("3");
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();
    await expect(positionRow(page, "France")).toContainText("3No");
  });

  await step("delete a position", async () => {
    await positionRow(page, "France")
      .getByRole("button", { name: "Delete" })
      .click();
    await modal(page).getByRole("button", { name: "Delete" }).click();
    await expect(modal(page)).toBeHidden();
    await expect(positionRow(page, "France")).toHaveCount(0);
    await expect(positionRow(page, "Joint Chiefs")).toBeVisible();
  });

  await step("delete the committee", async () => {
    await committeeRow(page, renamed)
      .getByRole("button", { name: "Delete" })
      .click();
    await expect(modal(page)).toContainText(`This will delete "${renamed}"`);
    await modal(page).getByRole("button", { name: "Delete" }).click();
    await expect(modal(page)).toBeHidden();
    await page.reload();
    await expect(page.getByText("Loading committees…")).toBeHidden();
    await expect(committeeRow(page, renamed)).toHaveCount(0);
  });
});

test("committees are admin-only", async ({ page, signInAs, step }) => {
  await signInAs("staffer");
  await step("staffer sees the admin-only notice", async () => {
    await page.goto(`/conferences/${seedConferenceId}/committees`);
    await expect(page.getByText("Admin only.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Expand" })).toHaveCount(0);
    for (const name of ["Create committee", "Reload"]) {
      await expect(page.getByRole("button", { name })).toHaveCount(0);
    }
  });
});

test("a committee links to its background guide on the site", async ({
  page,
  signInAs,
  step,
}) => {
  const conferenceId = await createDraftConference("Guides");
  await createCommittee(conferenceId, "Security Council", "NON_CRISIS", []);

  await signInAs("admin");
  await page.goto(`/conferences/${conferenceId}/committees`);

  await step("a path that isn't a CMS guide is refused", async () => {
    await committeeRow(page, "Security Council")
      .getByRole("button", { name: "Edit" })
      .click();
    await modal(page).getByLabel("Background guide").fill("unsc.pdf");
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(
      page.getByText(/Background guide must be a path like/),
    ).toBeVisible();
  });

  await step("the guide's CMS path becomes a link", async () => {
    await modal(page)
      .getByLabel("Background guide")
      .fill("/content/background-guides/numun-xxiv/unclos.md");
    await modal(page).getByRole("button", { name: "Save" }).click();
    await expect(modal(page)).toBeHidden();
    await expect(
      committeeRow(page, "Security Council").getByRole("link", {
        name: "View guide",
      }),
    ).toBeVisible();
  });

  await step("the link opens the committee's public page", async () => {
    const popup = page.waitForEvent("popup");
    await committeeRow(page, "Security Council")
      .getByRole("link", { name: "View guide" })
      .click();
    const site = await popup;
    await expect(site).toHaveURL(/\/committees\/numun-xxiv\/unclos$/);
    await expect(site.getByRole("heading", { level: 1 })).toBeVisible();
  });
});
