import { expect, test } from "../fixtures";
import {
  createDelegation,
  recordCharge,
  rpc,
  uniqueSchool,
} from "../support/api";
import { seedConferenceId, seedUsers } from "../support/seed";

test("unknown URLs show a not-found page", async ({ page, signInAs, step }) => {
  await signInAs("advisor");

  await step("unknown path", async () => {
    await page.goto("/no/such/page");
    await expect(
      page.getByRole("heading", { name: "Page not found" }),
    ).toBeVisible();
  });

  await step("link returns to the dashboard", async () => {
    await page.getByRole("link", { name: "Go to dashboard" }).click();
    await expect(page).toHaveURL("/dashboard");
  });
});

test("import screens opened directly point back to the start", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("DirectImport"),
  );
  const base = `/delegations/${delegation.id}/delegates/import`;
  await signInAs(newAdvisor);

  await step("preview without an upload", async () => {
    await page.goto(`${base}/preview`);
    await expect(
      page.getByText("No preview to show. Start an import first."),
    ).toBeVisible();
    await page.getByRole("button", { name: "Go to import" }).click();
    await expect(page).toHaveURL(base);
  });

  await step("result without an import", async () => {
    await page.goto(`${base}/result`);
    await expect(
      page.getByText("No result to show. Start an import first."),
    ).toBeVisible();
  });
});

test("Reload picks up changes made elsewhere", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  await signInAs("admin");

  await step("pending delegations", async () => {
    await page.goto("/admin/delegations");
    const school = uniqueSchool("ReloadList");
    await expect(page.getByRole("listitem").first()).toBeVisible();
    await createDelegation(newAdvisor.id, school);
    await page.getByRole("button", { name: "Reload" }).click();
    await expect(
      page.getByRole("listitem").filter({ hasText: school }),
    ).toBeVisible();
  });

  await step("committees", async () => {
    await page.goto(`/conferences/${seedConferenceId}/committees`);
    await expect(page.getByText("Loading committees…")).toBeHidden();
    const name = `E2E Reload Committee ${Date.now()}`;
    await rpc(seedUsers.admin.id, "CommitteeService/CreateCommittee", {
      conferenceId: seedConferenceId,
      name,
      type: "COMMITTEE_TYPE_NON_CRISIS",
      size: "COMMITTEE_SIZE_SMALL",
    });
    await page.getByRole("button", { name: "Reload" }).click();
    await expect(page.getByRole("row").filter({ hasText: name })).toBeVisible();
    const { items } = await rpc<{
      items: { id: string; name: string; version: number }[];
    }>(seedUsers.admin.id, "CommitteeService/ListCommittees", {
      conferenceId: seedConferenceId,
    });
    const created = items.find((c) => c.name === name)!;
    await rpc(seedUsers.admin.id, "CommitteeService/DeleteCommittee", {
      committeeId: created.id,
      expectedVersion: created.version,
    });
  });

  await step("payments ledger and balance", async () => {
    const delegation = await createDelegation(
      newAdvisor.id,
      uniqueSchool("ReloadLedger"),
    );
    await page.goto(`/delegations/${delegation.id}?tab=payments`);
    await expect(page.getByText("No ledger entries yet.")).toBeVisible();
    await recordCharge(delegation.id, 250, "RELOAD-1");
    await page.getByRole("button", { name: "Reload" }).last().click();
    await expect(
      page.getByRole("row").filter({ hasText: "RELOAD-1" }),
    ).toBeVisible();
    await expect(page.getByText("Balance due:").locator("..")).toContainText(
      "$250.00",
    );
  });
});
