import { readFile } from "node:fs/promises";

import { expect, test } from "../fixtures";
import {
  approveDelegation,
  assignStaffer,
  createAdvisor,
  createDelegate,
  createDelegation,
  recordCharge,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";
import { seedUsers } from "../support/seed";

test("a staffer works on the delegations they oversee", async ({
  page,
  signInAs,
  step,
}) => {
  const surname = uniqueSurname();
  const advisor = await createAdvisor("Overseen");
  const mine = await createDelegation(advisor.id, uniqueSchool("Overseen"));
  await approveDelegation(mine);
  await createDelegate(advisor.id, mine.id, "Ada", surname);
  await recordCharge(mine.id, 500, `INV-${surname}`);
  await assignStaffer(mine.id, seedUsers.staffer.id);
  const notMine = await createDelegation(
    (await createAdvisor("NotOverseen")).id,
    uniqueSchool("NotOverseen"),
  );

  await signInAs("staffer");
  const item = (school: string) =>
    page.getByRole("listitem").filter({ hasText: school });

  await step("the dashboard lists their delegations", async () => {
    await page.getByRole("button", { name: /^Delegations/ }).click();
    await expect(page.getByLabel("Status")).toHaveValue("all");
    await expect(item(mine.school)).toContainText("Approved");
    await expect(item(mine.school)).toContainText("Balance due $500.00");
    await expect(item(notMine.school)).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Approve" })).toHaveCount(0);
  });

  await step("open one and see its roster", async () => {
    await item(mine.school).getByRole("link", { name: "Open" }).click();
    await page.getByRole("button", { name: "Delegates", exact: true }).click();
    await expect(
      page.getByRole("row").filter({ hasText: `Ada ${surname}` }),
    ).toBeVisible();
  });

  await step("its payments are read-only", async () => {
    await page.getByRole("button", { name: "Payments", exact: true }).click();
    await expect(page.getByText("Balance due:").locator("..")).toContainText(
      "$500.00",
    );
    await expect(
      page.getByRole("row").filter({ hasText: `INV-${surname}` }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Record payment" }),
    ).toHaveCount(0);
  });

  await step("exports cover only their delegations", async () => {
    await page.goto("/exports");
    const [file] = await Promise.all([
      page.waitForEvent("download"),
      page.getByRole("link", { name: "Export delegates.csv" }).click(),
    ]);
    const csv = await readFile((await file.path())!, "utf8");
    expect(csv).toContain(`${mine.school},Ada,${surname}`);
    expect(csv).not.toContain(notMine.school);
  });
});

test("admins filter delegations by status", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const approved = await createDelegation(
    newAdvisor.id,
    uniqueSchool("FilterApproved"),
  );
  await approveDelegation(approved);
  const item = page.getByRole("listitem").filter({ hasText: approved.school });

  await signInAs("admin");

  await step("pending is the default", async () => {
    await page.getByRole("button", { name: /^Delegations/ }).click();
    await expect(page.getByLabel("Status")).toHaveValue("pending");
    await expect(page.getByRole("listitem").first()).toBeVisible();
    await expect(item).toHaveCount(0);
  });

  await step("approved delegations can be opened", async () => {
    await page.getByLabel("Status").selectOption("Approved");
    await expect(page).toHaveURL(/status=approved/);
    await expect(item).toContainText("Approved");
    await expect(item.getByRole("button", { name: "Approve" })).toHaveCount(0);
  });

  await step("the filter survives a reload", async () => {
    await page.reload();
    await expect(page.getByLabel("Status")).toHaveValue("approved");
    await expect(item).toBeVisible();
  });
});
