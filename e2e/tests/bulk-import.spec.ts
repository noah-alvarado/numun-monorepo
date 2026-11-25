import { readFile } from "node:fs/promises";
import path from "node:path";

import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import { createDelegation, uniqueSchool } from "../support/api";

function csv(rows: string[][]): {
  name: string;
  mimeType: string;
  buffer: Buffer;
} {
  return {
    name: "roster.csv",
    mimeType: "text/csv",
    buffer: Buffer.from(rows.map((r) => r.join(",")).join("\r\n") + "\r\n"),
  };
}

function stat(page: Page, label: string) {
  // The summary grid; "Errors" is also a table column header.
  return page
    .locator("section.grid")
    .getByText(label, { exact: true })
    .locator("..");
}

function previewRow(page: Page, rowNumber: number) {
  return page.getByRole("row").filter({
    has: page.getByRole("cell", { name: String(rowNumber), exact: true }),
  });
}

test("advisor imports a CSV roster", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("Import"),
  );
  await signInAs(newAdvisor);

  await step("open import from the delegation", async () => {
    await page.goto("/delegation");
    await page.getByRole("link", { name: "Bulk import delegates" }).click();
    await expect(page).toHaveURL(
      `/delegations/${delegation.id}/delegates/import`,
    );
  });

  await step("upload a CSV", async () => {
    await page.getByLabel("File").setInputFiles(
      csv([
        ["First Name", "Last Name", "Email", "Experience", "T-Shirt"],
        ["Ada", "Lovelace", "ada@example.com", "advanced", "M"],
        ["Grace", "Hopper", "grace@example.com", "", "S"],
        ["Mary Jane", "Smith", "", "novice", "L"],
      ]),
    );
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(
      page.getByRole("heading", { name: "Review import" }),
    ).toBeVisible();
  });

  await step("preview summarises the rows", async () => {
    await expect(stat(page, "Parsed")).toContainText("3");
    await expect(stat(page, "Valid")).toContainText("3");
    await expect(stat(page, "Create")).toContainText("3");
    await expect(
      page.getByText(/1 unrecognized column will be ignored/),
    ).toBeVisible();
    await expect(
      previewRow(page, 1).getByRole("combobox").locator("option:checked"),
    ).toHaveText("Advanced");
    // Empty experience defaults to Intermediate (BULK_IMPORT.md §3.2).
    await expect(
      previewRow(page, 2).getByRole("combobox").locator("option:checked"),
    ).toHaveText("Intermediate");
    await expect(
      page.getByRole("button", { name: "Confirm import" }),
    ).toBeEnabled();
  });

  await step("confirm import", async () => {
    await page.getByRole("button", { name: "Confirm import" }).click();
    await expect(
      page.getByRole("heading", { name: "Import result" }),
    ).toBeVisible();
    await expect(page.getByText("Imported 3 delegates")).toBeVisible();
    await expect(
      page.getByText("3 created · 0 updated · 0 soft-deleted"),
    ).toBeVisible();
  });

  await step("re-importing updates instead of duplicating", async () => {
    await page.goto(`/delegations/${delegation.id}/delegates/import`);
    await page.getByLabel("File").setInputFiles(
      csv([
        ["First Name", "Last Name", "Email", "Experience"],
        ["Ada", "Lovelace", "ada@example.com", "intermediate"],
      ]),
    );
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(stat(page, "Update")).toContainText("1");
    await expect(stat(page, "Create")).toContainText("0");
    await page.getByRole("button", { name: /update — see diff/ }).click();
    await expect(page.getByText("Diff", { exact: true })).toBeVisible();
  });

  await step("cancel discards the preview", async () => {
    await page.getByRole("button", { name: "Cancel" }).first().click();
    await expect(page).toHaveURL(/\/delegation$/);
  });
});

test("row errors block confirm until fixed inline", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("ImportErrors"),
  );
  await signInAs(newAdvisor);

  await step("upload a CSV with a bad row", async () => {
    await page.goto(`/delegations/${delegation.id}/delegates/import`);
    await page.getByLabel("File").setInputFiles(
      csv([
        ["firstName", "lastName", "email"],
        ["Alan", "Turing", "alan@example.com"],
        ["Bad", "Email", "not-an-email"],
      ]),
    );
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(
      page.getByRole("heading", { name: "Review import" }),
    ).toBeVisible();
  });

  const bad = previewRow(page, 2);

  await step("bad row is flagged and confirm is blocked", async () => {
    await expect(stat(page, "Errors")).toContainText("1");
    await expect(bad).toContainText("email:");
    await expect(
      page.getByText("Fix all row errors before confirming."),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Confirm import" }),
    ).toBeDisabled();
  });

  await step("fix the email inline", async () => {
    // Type key by key, like a person: each keystroke must keep focus.
    const email = bad.getByRole("textbox").nth(2);
    await email.selectText();
    await email.pressSequentially("bad.email@example.com");
    await expect(email).toHaveValue("bad.email@example.com");
    await expect(bad).not.toContainText("email:");
    await expect(
      page.getByRole("button", { name: "Confirm import" }),
    ).toBeEnabled();
  });

  await step("confirm the fixed import", async () => {
    await page.getByRole("button", { name: "Confirm import" }).click();
    await expect(page.getByText("Imported 2 delegates")).toBeVisible();
  });
});

test("non-roster files are refused before upload", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("ImportType"),
  );
  await signInAs(newAdvisor);

  await step("upload a .txt file", async () => {
    await page.goto(`/delegations/${delegation.id}/delegates/import`);
    await page.getByLabel("File").setInputFiles({
      name: "roster.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("hello"),
    });
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(
      page.getByText("Only .csv and .xlsx files are supported."),
    ).toBeVisible();
  });

  await step("header-only CSV is rejected", async () => {
    await page
      .getByLabel("File")
      .setInputFiles(csv([["firstName", "lastName"]]));
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(page.getByText(/no data rows/)).toBeVisible();
  });
});

test("advisor imports an XLSX roster from a chosen tab", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("ImportXlsx"),
  );
  await signInAs(newAdvisor);

  await step("upload a two-tab workbook", async () => {
    await page.goto(`/delegations/${delegation.id}/delegates/import`);
    await page
      .getByLabel("File")
      .setInputFiles(
        path.join(import.meta.dirname, "../files/roster-two-tabs.xlsx"),
      );
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(
      page.getByRole("heading", { name: "Choose a tab" }),
    ).toBeVisible();
  });

  await step("pick the roster tab", async () => {
    await page.getByRole("button", { name: "Roster", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Review import" }),
    ).toBeVisible();
    await expect(stat(page, "Parsed")).toContainText("2");
    // fullName splits on the last space; "beginner" maps to Novice.
    const first = previewRow(page, 1);
    await expect(first.getByRole("textbox").nth(0)).toHaveValue("Katherine");
    await expect(first.getByRole("textbox").nth(1)).toHaveValue("Johnson");
    await expect(
      previewRow(page, 2).getByRole("combobox").locator("option:checked"),
    ).toHaveText("Novice");
  });

  await step("confirm import", async () => {
    await page.getByRole("button", { name: "Confirm import" }).click();
    await expect(page.getByText("Imported 2 delegates")).toBeVisible();
  });
});

// BULK_IMPORT.md §9: both starter templates download and import as-is — the
// two example rows, one advanced and one defaulting to intermediate.
test("starter templates download and import cleanly", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("Templates"),
  );
  await signInAs(newAdvisor);
  const importUrl = `/delegations/${delegation.id}/delegates/import`;

  for (const name of [
    "delegate-import-template.csv",
    "delegate-import-template.xlsx",
  ]) {
    await step(`${name} round-trips through preview`, async () => {
      await page.goto(importUrl);
      const download = page.waitForEvent("download");
      await page.getByRole("link", { name }).click();
      const file = await (await download).path();
      expect((await download).suggestedFilename()).toBe(name);

      await page.getByLabel("File").setInputFiles({
        name,
        mimeType: name.endsWith(".csv")
          ? "text/csv"
          : "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        buffer: await readFile(file),
      });
      await page.getByRole("button", { name: "Preview import" }).click();
      if (name.endsWith(".xlsx")) {
        await page
          .getByRole("button", { name: "Delegates", exact: true })
          .click();
      }
      await expect(
        page.getByRole("heading", { name: "Review import" }),
      ).toBeVisible();
      await expect(stat(page, "Valid")).toContainText("2");
      await expect(stat(page, "Errors")).toContainText("0");
      await expect(previewRow(page, 1).getByRole("textbox").nth(0)).toHaveValue(
        "Ada",
      );
      await expect(
        previewRow(page, 1).getByRole("combobox").locator("option:checked"),
      ).toHaveText("Advanced");
      await expect(
        previewRow(page, 2).getByRole("combobox").locator("option:checked"),
      ).toHaveText("Intermediate");
    });
  }
});
