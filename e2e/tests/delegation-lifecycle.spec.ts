import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import { createDelegation, uniqueSchool } from "../support/api";
import { e2eSchool } from "../support/seed";

function statusBadge(page: Page) {
  return page.getByText(/^(Pending review|Approved|Rejected)$/);
}

function pendingItem(page: Page, school: string) {
  return page.getByRole("listitem").filter({ hasText: school });
}

test("advisor registers a delegation and an admin approves it", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const school = uniqueSchool("Register");

  await signInAs(newAdvisor);

  await step("open empty registration form", async () => {
    await page.getByRole("button", { name: /My Delegation/ }).click();
    await expect(
      page.getByRole("heading", { name: "Register your school" }),
    ).toBeVisible();
  });

  await step("submitting without a school shows an error", async () => {
    await page.getByRole("button", { name: "Submit registration" }).click();
    await expect(page.getByText("School name is required.")).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Register your school" }),
    ).toBeVisible();
  });

  await step("fill registration", async () => {
    await page.getByLabel("School name").fill(school);
    await page.getByLabel("Street").fill("1 University Pl");
    await page.getByLabel("City").fill("Chicago");
    await page.getByLabel("State").fill("IL");
    await page.getByLabel("Postal code").fill("60601");
    await page.getByLabel("Country").fill("US");
    await page.getByLabel("Total").fill("20");
    await page.getByLabel("Financially qualifying").fill("4");
    await page
      .getByText("Crisis committees", { exact: true })
      .locator("..")
      .getByText("Yes", { exact: true })
      .click();
  });

  await step("submit registration", async () => {
    await page.getByRole("button", { name: "Submit registration" }).click();
    await expect(statusBadge(page)).toHaveText("Pending review");
    await expect(page.getByText("Version 1")).toBeVisible();
    await expect(page.getByLabel("School name")).toHaveValue(school);
    await expect(page.getByLabel("Total")).toHaveValue("20");
  });

  await signInAs("admin");

  await step("admin sees it pending and approves", async () => {
    await page.getByRole("button", { name: /^Delegations/ }).click();
    const item = pendingItem(page, school);
    await expect(item).toContainText("Chicago, IL US");
    await expect(item).toContainText("Est. 20 delegates");
    await item.getByRole("button", { name: "Approve" }).click();
    await expect(item).toHaveCount(0);
    await expect(page.locator("p.bg-red-50")).toBeHidden();
  });

  await step("approval survives a reload", async () => {
    await page.reload();
    await expect(page.getByText("Loading…")).toBeHidden();
    // The seed advisor's pending delegation proves the list loaded.
    await expect(pendingItem(page, e2eSchool)).toBeVisible();
    await expect(pendingItem(page, school)).toHaveCount(0);
  });

  await signInAs(newAdvisor);

  await step("advisor sees approved", async () => {
    await page.goto("/delegation");
    await expect(statusBadge(page)).toHaveText("Approved");
  });
});

test("admin rejects a delegation", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const school = uniqueSchool("Reject");
  await createDelegation(newAdvisor.id, school);

  await signInAs("admin");

  const item = pendingItem(page, school);
  const dialog = page.getByRole("dialog", { name: `Reject ${school}?` });

  await step("reject asks for confirmation", async () => {
    await page.goto("/admin/delegations");
    await item.getByRole("button", { name: "Reject" }).click();
    await expect(dialog).toContainText("can't be undone");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toBeHidden();
    await expect(item).toBeVisible();
  });

  await step("confirmed reject with a reason", async () => {
    await item.getByRole("button", { name: "Reject" }).click();
    await dialog.getByLabel(/Reason/).fill("Duplicate registration");
    await dialog.getByRole("button", { name: "Reject" }).click();
    await expect(item).toHaveCount(0);
    await expect(page.locator("p.bg-red-50")).toBeHidden();
  });

  await signInAs(newAdvisor);

  await step("advisor sees rejected", async () => {
    await page.goto("/delegation");
    await expect(statusBadge(page)).toHaveText("Rejected");
  });
});

test("concurrent edits in two tabs surface a conflict", async ({
  page,
  context,
  signInAs,
  newAdvisor,
  step,
}) => {
  const school = uniqueSchool("Conflict");
  await createDelegation(newAdvisor.id, school);
  await signInAs(newAdvisor);

  const tabA = page;
  const tabB = await context.newPage();

  await step("open the delegation in two tabs", async () => {
    for (const tab of [tabA, tabB]) {
      await tab.goto("/delegation");
      await expect(tab.getByText("Version 1")).toBeVisible();
    }
  });

  await step("tab A saves first", async () => {
    await tabA.getByLabel("City").fill("Tab A City");
    await tabA.getByRole("button", { name: "Save changes" }).click();
    await expect(tabA.getByText("Version 2")).toBeVisible();
  });

  await step(
    "tab B's stale save is rejected",
    async () => {
      await tabB.getByLabel("City").fill("Tab B City");
      await tabB.getByRole("button", { name: "Save changes" }).click();
      await expect(
        tabB.getByText(/Another change was saved meanwhile/),
      ).toBeVisible();
      await expect(tabB.getByText("Version 1")).toBeVisible();
    },
    tabB,
  );

  await step(
    "tab B reloads tab A's change",
    async () => {
      await tabB.getByRole("button", { name: "Reload" }).click();
      await expect(tabB.getByText("Version 2")).toBeVisible();
      await expect(tabB.getByLabel("City")).toHaveValue("Tab A City");
    },
    tabB,
  );
});
