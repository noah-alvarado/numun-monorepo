import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import { createDelegation, uniqueSchool } from "../support/api";
import { seedUsers } from "../support/seed";

function balance(page: Page) {
  return page.getByText("Balance due:").locator("..");
}

function ledgerRow(page: Page, text: string) {
  return page.getByRole("row").filter({ hasText: text });
}

async function record(
  page: Page,
  entry: { amount: string; kind: string; method: string; reference: string },
) {
  await page.getByRole("button", { name: "Record payment" }).click();
  const modal = page.locator("div.fixed.inset-0");
  await modal.getByLabel("Amount").fill(entry.amount);
  await modal.getByLabel(entry.kind, { exact: true }).check();
  await modal.getByLabel("Method").selectOption({ label: entry.method });
  await modal.getByLabel("Reference").fill(entry.reference);
  await modal.getByRole("button", { name: "Save" }).click();
  await expect(modal).toBeHidden();
  await expect(ledgerRow(page, entry.reference)).toBeVisible();
}

test("payments ledger drives balance and paid-in-full", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("Payments"),
  );

  await signInAs("admin");

  await step("open the payments tab", async () => {
    await page.goto(`/delegations/${delegation.id}?tab=payments`);
    await expect(page.getByText("No ledger entries yet.")).toBeVisible();
    await expect(balance(page)).toContainText("Nothing due");
  });

  const recordModal = page.locator("div.fixed.inset-0");
  await step("invalid amount is refused", async () => {
    await page.getByRole("button", { name: "Record payment" }).click();
    await recordModal.getByLabel("Amount").fill("12.345");
    await recordModal.getByRole("button", { name: "Save" }).click();
    await expect(recordModal.getByText(/non-negative amount/)).toBeVisible();
  });
  await recordModal.getByRole("button", { name: "Cancel" }).click();
  await expect(recordModal).toBeHidden();

  await step("record a $1,000 charge", async () => {
    await record(page, {
      amount: "1000",
      kind: "Charge",
      method: "Other",
      reference: "INV-1",
    });
    await expect(ledgerRow(page, "INV-1")).toContainText("+$1,000.00");
    await expect(ledgerRow(page, "INV-1")).toContainText(seedUsers.admin.name);
    await expect(balance(page)).toContainText("$1,000.00");
    await expect(balance(page)).toContainText("Outstanding");
  });

  await step("record a partial $400 payment", async () => {
    await record(page, {
      amount: "400.00",
      kind: "Payment",
      method: "Check",
      reference: "CHK-101",
    });
    await expect(ledgerRow(page, "CHK-101")).toContainText("-$400.00");
    await expect(ledgerRow(page, "CHK-101")).toContainText("$600.00");
    await expect(balance(page)).toContainText("$600.00");
    await expect(balance(page)).toContainText("Outstanding");
  });

  await step("record the remaining $600", async () => {
    await record(page, {
      amount: "600",
      kind: "Payment",
      method: "Wire",
      reference: "WIRE-7",
    });
    await expect(balance(page)).toContainText("$0.00");
    await expect(balance(page)).toContainText("Paid in full");
  });

  await step("edit a payment's notes", async () => {
    await ledgerRow(page, "CHK-101")
      .getByRole("button", { name: "Edit" })
      .click();
    const modal = page.locator("div.fixed.inset-0");
    await modal.getByLabel("Notes").fill("Mailed by the bursar");
    await modal.getByRole("button", { name: "Save" }).click();
    await expect(modal).toBeHidden();
    await expect(ledgerRow(page, "CHK-101")).toContainText(
      "Mailed by the bursar",
    );
  });

  await step("deleting a payment restores the balance", async () => {
    await ledgerRow(page, "WIRE-7")
      .getByRole("button", { name: "Delete" })
      .click();
    const modal = page.locator("div.fixed.inset-0");
    await modal.getByRole("button", { name: "Delete" }).click();
    await expect(modal).toBeHidden();
    await expect(ledgerRow(page, "WIRE-7")).toHaveCount(0);
    await expect(balance(page)).toContainText("$600.00");
    await expect(balance(page)).toContainText("Outstanding");
  });

  await step("balance survives a reload", async () => {
    await page.reload();
    await expect(ledgerRow(page, "CHK-101")).toBeVisible();
    await expect(balance(page)).toContainText("$600.00");
  });

  await signInAs(newAdvisor);

  await step("advisor sees the ledger read-only", async () => {
    await page.goto("/delegation?tab=payments");
    await expect(balance(page)).toContainText("$600.00");
    await expect(ledgerRow(page, "CHK-101")).toBeVisible();
    await expect(ledgerRow(page, "INV-1")).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Record payment" }),
    ).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Edit" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
    await expect(
      page.getByRole("columnheader", { name: "Recorded by" }),
    ).toHaveCount(0);
    await expect(ledgerRow(page, "INV-1")).not.toContainText(
      seedUsers.admin.id,
    );
  });
});
