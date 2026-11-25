import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import {
  createAdvisor,
  createDelegate,
  createDelegation,
  rpc,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";
import { seedUsers } from "../support/seed";

const row = (page: Page, text: string) =>
  page.getByRole("row").filter({ hasText: text });

test("advisor manages the roster by hand", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  await createDelegation(newAdvisor.id, uniqueSchool("Roster"));
  const surname = uniqueSurname();
  await signInAs(newAdvisor);

  await step("an empty roster explains how to add delegates", async () => {
    await page.goto("/delegation");
    await page.getByRole("button", { name: "Delegates", exact: true }).click();
    await expect(page.getByText(/No delegates yet\./)).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Bulk import delegates" }),
    ).toBeVisible();
  });

  const form = () => page.getByRole("form", { name: "New delegate" });

  await step("add two delegates", async () => {
    for (const [first, email, level] of [
      ["Ada", "ada@example.com", "Advanced"],
      ["Alan", "", "Novice"],
    ]) {
      await page.getByRole("button", { name: "Add delegate" }).click();
      await form().getByLabel("First name").fill(first);
      await form().getByLabel("Last name").fill(surname);
      await form().getByLabel("Email (optional)").fill(email);
      await form().getByLabel("Experience").selectOption({ label: level });
      await form().getByRole("button", { name: "Save delegate" }).click();
      await expect(page.getByText(`Added ${first} ${surname}.`)).toBeVisible();
    }
    await expect(row(page, `Ada ${surname}`)).toContainText("ada@example.com");
    await expect(row(page, `Ada ${surname}`)).toContainText("Advanced");
    await expect(row(page, `Alan ${surname}`)).toContainText("Novice");
    await expect(page.getByRole("heading", { name: /Delegates/ })).toHaveText(
      "Delegates (2)",
    );
  });

  await step("edit a delegate", async () => {
    await page.getByRole("button", { name: `Edit Alan ${surname}` }).click();
    const edit = page.getByRole("form", { name: `Edit Alan ${surname}` });
    await edit.getByLabel("Email (optional)").fill("alan@example.com");
    await edit.getByLabel("Experience").selectOption({ label: "Intermediate" });
    await edit.getByRole("button", { name: "Save" }).click();
    await expect(row(page, `Alan ${surname}`)).toContainText(
      "alan@example.com",
    );
    await expect(row(page, `Alan ${surname}`)).toContainText("Intermediate");
  });

  await step("remove a delegate after confirming", async () => {
    await page.getByRole("button", { name: `Remove Ada ${surname}` }).click();
    const dialog = page.getByRole("dialog", {
      name: `Remove Ada ${surname}?`,
    });
    await expect(dialog).toContainText("NUMUN staff can restore them");
    await dialog.getByRole("button", { name: "Remove" }).click();
    await expect(page.getByText(`Removed Ada ${surname}.`)).toBeVisible();
    await expect(row(page, `Ada ${surname}`)).toHaveCount(0);
    await expect(page.getByRole("heading", { name: /Delegates/ })).toHaveText(
      "Delegates (1)",
    );
    // Advisors can't see removed delegates.
    await expect(page.getByLabel("Show removed")).toHaveCount(0);
  });
});

test("admin restores a removed delegate", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("Restore"),
  );
  const surname = uniqueSurname();
  const grace = await createDelegate(
    newAdvisor.id,
    delegation.id,
    "Grace",
    surname,
  );
  await rpc(newAdvisor.id, "DelegateService/DeleteDelegate", {
    delegateId: grace.id,
    expectedVersion: grace.version,
  });
  await signInAs("admin");

  await step("removed delegates are hidden by default", async () => {
    await page.goto(`/delegations/${delegation.id}?tab=delegates`);
    await expect(page.getByText(/No delegates yet\./)).toBeVisible();
  });

  await step("show removed lists them", async () => {
    await page.getByLabel("Show removed").check();
    await expect(row(page, `Grace ${surname}`)).toContainText("Removed");
  });

  await step("restore puts them back", async () => {
    await row(page, `Grace ${surname}`)
      .getByRole("button", { name: "Restore" })
      .click();
    await expect(page.getByText(`Restored Grace ${surname}.`)).toBeVisible();
    await expect(row(page, `Grace ${surname}`)).not.toContainText("Removed");
    await page.getByLabel("Show removed").uncheck();
    await expect(row(page, `Grace ${surname}`)).toBeVisible();
  });
});

test("advisors add co-advisors by email; leads manage them", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  await createDelegation(newAdvisor.id, uniqueSchool("CoAdvisors"));
  const coAdvisor = await createAdvisor("CoAdvisor", { withEmail: true });
  const third = await createAdvisor("Third", { withEmail: true });
  await signInAs(newAdvisor);

  const addForm = () => page.getByRole("form", { name: "Add an advisor" });

  await step("the lead sees themselves", async () => {
    await page.goto("/delegation?tab=advisors");
    await expect(row(page, newAdvisor.name)).toContainText("Lead");
    await expect(row(page, newAdvisor.name)).toContainText("(you)");
  });

  await step("an email without an account is refused", async () => {
    await addForm()
      .getByLabel(/Co-advisor's email/)
      .fill("nobody.here@example.com");
    await addForm().getByRole("button", { name: "Add advisor" }).click();
    await expect(
      page.getByText(
        "No advisor account uses that email; ask them to sign up first",
      ),
    ).toBeVisible();
  });

  await step("add a co-advisor by email", async () => {
    await addForm()
      .getByLabel(/Co-advisor's email/)
      .fill(coAdvisor.email!);
    await addForm().getByRole("button", { name: "Add advisor" }).click();
    await expect(page.getByText(`Added ${coAdvisor.name}.`)).toBeVisible();
    await expect(row(page, coAdvisor.name)).toContainText("Co-advisor");
    await expect(row(page, coAdvisor.name)).toContainText(coAdvisor.email!);
  });

  await signInAs(coAdvisor);
  await step("the co-advisor sees the delegation", async () => {
    await page.goto("/delegation?tab=advisors");
    await expect(row(page, newAdvisor.name)).toContainText("Lead");
    // Only leads change roles or remove advisors.
    await expect(page.getByRole("button", { name: "Make lead" })).toHaveCount(
      0,
    );
    await expect(page.getByRole("button", { name: /^Remove / })).toHaveCount(0);
    await expect(addForm().getByLabel("Role")).toHaveCount(0);
  });

  await step("a co-advisor can add another co-advisor", async () => {
    await addForm()
      .getByLabel(/Co-advisor's email/)
      .fill(third.email!);
    await addForm().getByRole("button", { name: "Add advisor" }).click();
    await expect(row(page, third.name)).toContainText("Co-advisor");
  });

  await signInAs(newAdvisor);
  await step("the lead promotes and removes advisors", async () => {
    await page.goto("/delegation?tab=advisors");
    await row(page, coAdvisor.name)
      .getByRole("button", { name: "Make lead" })
      .click();
    await expect(
      page.getByText(`${coAdvisor.name} is now lead.`),
    ).toBeVisible();
    await expect(row(page, coAdvisor.name)).toContainText("Lead");

    await page.getByRole("button", { name: `Remove ${third.name}` }).click();
    await page
      .getByRole("dialog", { name: `Remove ${third.name}?` })
      .getByRole("button", { name: "Remove" })
      .click();
    await expect(page.getByText(`Removed ${third.name}.`)).toBeVisible();
    await expect(row(page, third.name)).toHaveCount(0);
  });
});

test("admin approves, rejects and staffs from the delegation page", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const toApprove = await createDelegation(
    newAdvisor.id,
    uniqueSchool("DetailApprove"),
  );
  const rejectedAdvisor = await createAdvisor("Rejected");
  const toReject = await createDelegation(
    rejectedAdvisor.id,
    uniqueSchool("DetailReject"),
  );
  const status = () => page.locator("span.uppercase").first();
  await signInAs("admin");

  await step("approve a pending delegation", async () => {
    await page.goto(`/delegations/${toApprove.id}`);
    await expect(status()).toHaveText("Pending review");
    await page.getByRole("button", { name: "Approve" }).click();
    await expect(status()).toHaveText("Approved");
    await expect(page.getByRole("button", { name: "Approve" })).toHaveCount(0);
  });

  await step("assign and unassign a staffer", async () => {
    const assign = page.getByRole("form", { name: "Assign a staffer" });
    const option = assign
      .locator("option")
      .filter({ hasText: seedUsers.staffer.name });
    await assign
      .getByLabel("Staffer")
      .selectOption((await option.getAttribute("value"))!);
    await assign.getByRole("button", { name: "Assign" }).click();
    await expect(
      page.getByRole("button", { name: `Unassign ${seedUsers.staffer.name}` }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: `Unassign ${seedUsers.staffer.name}` })
      .click();
    await expect(page.getByText("No staffers assigned.")).toBeVisible();
  });

  await step("reject another after confirming", async () => {
    await page.goto(`/delegations/${toReject.id}`);
    await page.getByRole("button", { name: "Reject" }).click();
    const dialog = page.getByRole("dialog", {
      name: `Reject ${toReject.school}?`,
    });
    await dialog.getByLabel(/Reason/).fill("Duplicate registration");
    await dialog.getByRole("button", { name: "Reject" }).click();
    await expect(status()).toHaveText("Rejected");
    // Staff can still edit a rejected registration.
    await expect(
      page.getByRole("button", { name: "Save changes" }),
    ).toBeVisible();
  });

  await signInAs(rejectedAdvisor);
  await step("the school sees a read-only page", async () => {
    await page.goto("/delegation");
    await expect(
      page.getByText(/NUMUN staff didn't approve this registration/),
    ).toBeVisible();
    await expect(page.getByText(toReject.school)).toBeVisible();
    await expect(page.getByText("Duplicate registration")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Save changes" }),
    ).toHaveCount(0);
  });

  await step("its roster and advisors are read-only too", async () => {
    await page.getByRole("button", { name: "Delegates", exact: true }).click();
    await expect(page.getByText("No delegates yet.")).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Add delegate" }),
    ).toHaveCount(0);
    await page.getByRole("button", { name: "Advisors", exact: true }).click();
    await expect(row(page, rejectedAdvisor.name)).toBeVisible();
    await expect(
      page.getByRole("form", { name: "Add an advisor" }),
    ).toHaveCount(0);
  });
});
