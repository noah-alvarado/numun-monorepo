import { readFile } from "node:fs/promises";

import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import {
  createAdvisor,
  createDelegate,
  createDelegation,
  recordCharge,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";
import { seedConferenceId, seedConferenceName } from "../support/seed";

// API.md §12.3.
const columns = {
  assignments:
    "assignment_id,conference_id,delegation_id,school,delegate_id,delegate_first_name,delegate_last_name,committee_id,committee_name,position_id,position_name,status,score,reason,run_id,approved_at,approved_by",
  delegates:
    "delegate_id,conference_id,delegation_id,school,first_name,last_name,email,experience_level,checked_in_at",
  payments:
    "payment_id,conference_id,delegation_id,school,recorded_at,amount_currency,amount_units,amount_cents,kind,method,reference,notes,recorded_by",
};

async function download(page: Page, kind: keyof typeof columns) {
  const [file] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("link", { name: `Export ${kind}.csv` }).click(),
  ]);
  expect(file.suggestedFilename()).toBe(`${kind}-${seedConferenceId}.csv`);
  return readFile((await file.path())!, "utf8");
}

function expectCsvShape(body: string, header: string) {
  // UTF-8 BOM so Excel picks the encoding; CRLF line endings (API.md §12).
  expect(body.charCodeAt(0)).toBe(0xfeff);
  const text = body.slice(1);
  expect(text.split("\r\n")[0]).toBe(header);
  expect(text.replace(/\r\n/g, "")).not.toContain("\n");
}

test("admin downloads the CSV exports", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const school = uniqueSchool("Export");
  const surname = uniqueSurname();
  const delegation = await createDelegation(newAdvisor.id, school);
  await createDelegate(newAdvisor.id, delegation.id, "Export", surname);
  const reference = `EXP-${surname}`;
  await recordCharge(delegation.id, 250, reference);

  await signInAs("admin");

  await step("open exports from the dashboard", async () => {
    await page.getByRole("button", { name: /^Exports/ }).click();
    await expect(page).toHaveURL(/\/exports$/);
    await expect(page.getByText(seedConferenceName)).toBeVisible();
    await expect(
      page.getByText("Files cover every delegation in the conference."),
    ).toBeVisible();
  });

  await step("delegates.csv", async () => {
    const body = await download(page, "delegates");
    expectCsvShape(body, columns.delegates);
    const row = body.split("\r\n").find((l) => l.includes(surname));
    expect(row).toContain(delegation.id);
    expect(row).toContain(school);
  });

  await step("payments.csv", async () => {
    const body = await download(page, "payments");
    expectCsvShape(body, columns.payments);
    const row = body.split("\r\n").find((l) => l.includes(reference));
    expect(row).toContain(delegation.id);
    // Charges are debits on the ledger, so the amount is negative.
    expect(row).toContain(",USD,-250,0,charge,other,");
  });

  await step("assignments.csv", async () => {
    expectCsvShape(await download(page, "assignments"), columns.assignments);
  });
});

test("advisor exports are scoped to their own delegation", async ({
  page,
  signInAs,
  newAdvisor,
}) => {
  const mine = uniqueSurname();
  const theirs = uniqueSurname();
  const own = await createDelegation(newAdvisor.id, uniqueSchool("ExportMine"));
  await createDelegate(newAdvisor.id, own.id, "Mine", mine);
  const other = await createAdvisor("Other");
  const otherDelegation = await createDelegation(
    other.id,
    uniqueSchool("ExportTheirs"),
  );
  await createDelegate(other.id, otherDelegation.id, "Theirs", theirs);

  await signInAs(newAdvisor);
  // API.md §12.2: advisors may export their own delegation(s) only.
  const resp = await page.request.get(
    `/v1/exports/delegates.csv?conference_id=${seedConferenceId}`,
  );
  expect(resp.status()).toBe(200);
  const body = await resp.text();
  expectCsvShape(body, columns.delegates);
  expect(body).toContain(mine);
  expect(body).not.toContain(theirs);
});
