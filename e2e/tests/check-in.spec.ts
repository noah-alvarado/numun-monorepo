import { expect, test } from "../fixtures";
import {
  assignStaffer,
  createDelegate,
  createDelegation,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";
import { seedUsers } from "../support/seed";

test("staff check in a delegate by name", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const school = uniqueSchool("CheckIn");
  const surname = uniqueSurname();
  const delegation = await createDelegation(newAdvisor.id, school);
  await createDelegate(newAdvisor.id, delegation.id, "Rosalind", surname);
  await createDelegate(newAdvisor.id, delegation.id, "Lise", surname);

  const hit = (first: string) =>
    page.getByRole("listitem").filter({ hasText: `${surname}, ${first}` });

  await signInAs("admin");

  await step("search by last name", async () => {
    await page.goto("/admin/check-in");
    await page.getByPlaceholder(/Search by first or last name/).fill(surname);
    await page.getByRole("button", { name: "Search" }).click();
    await expect(hit("Rosalind")).toContainText(school);
    await expect(hit("Lise")).toBeVisible();
  });

  await step("check one delegate in", async () => {
    await hit("Rosalind").getByRole("button", { name: "Check in" }).click();
    await expect(
      hit("Rosalind").getByRole("button", { name: "✓ Checked in" }),
    ).toBeDisabled();
    await expect(hit("Rosalind")).toContainText("checked in");
    await expect(
      hit("Lise").getByRole("button", { name: "Check in" }),
    ).toBeEnabled();
  });

  await step("check-in persists across a fresh search", async () => {
    await page.reload();
    await page.getByPlaceholder(/Search by first or last name/).fill(surname);
    await page.getByRole("button", { name: "Search" }).click();
    await expect(
      hit("Rosalind").getByRole("button", { name: "✓ Checked in" }),
    ).toBeVisible();
    await expect(
      hit("Lise").getByRole("button", { name: "Check in" }),
    ).toBeVisible();
  });

  await step("too-short queries are refused", async () => {
    await page.getByPlaceholder(/Search by first or last name/).fill("Z");
    await page.getByRole("button", { name: "Search" }).click();
    await expect(page.getByText("Type at least 2 characters.")).toBeVisible();
    await expect(page.getByRole("listitem")).toHaveCount(0);
  });
});

test("check-in is staff-only and scoped", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const surname = uniqueSurname();
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("Scope"),
  );
  await createDelegate(newAdvisor.id, delegation.id, "Ada", surname);

  await signInAs(newAdvisor);
  await step("advisor is turned away", async () => {
    await page.goto("/admin/check-in");
    await expect(page.getByText("Staff only.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Search" })).toHaveCount(0);
  });

  await signInAs("staffer");
  await step("unassigned staffer finds nothing", async () => {
    await page.goto("/admin/check-in");
    await page.getByPlaceholder(/Search by first or last name/).fill(surname);
    await page.getByRole("button", { name: "Search" }).click();
    await expect(page.getByText("No matches.")).toBeVisible();
  });

  await step("assigned staffer finds and checks them in", async () => {
    await assignStaffer(delegation.id, seedUsers.staffer.id);
    await page.getByRole("button", { name: "Search" }).click();
    const hit = page
      .getByRole("listitem")
      .filter({ hasText: `${surname}, Ada` });
    await hit.getByRole("button", { name: "Check in" }).click();
    await expect(
      hit.getByRole("button", { name: "✓ Checked in" }),
    ).toBeDisabled();
  });
});
