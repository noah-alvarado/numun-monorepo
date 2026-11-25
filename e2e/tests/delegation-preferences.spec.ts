import type { Locator, Page } from "@playwright/test";

import { expect, test } from "../fixtures";

// Each axis row renders "—" / "Yes" / "Neutral" / "No" as visually-styled
// radio labels (the inputs themselves are sr-only).
type Choice = "Yes" | "Neutral" | "No";

function prefRow(page: Page, label: string): Locator {
  return page.getByText(label, { exact: true }).locator("..");
}

async function choose(row: Locator, choice: Choice) {
  await row.getByText(choice, { exact: true }).click();
  await expect(row.getByRole("radio", { name: choice })).toBeChecked();
}

test("advisor committee preferences save and reload", async ({
  page,
  signInAs,
  step,
}) => {
  await signInAs("advisor");

  await step("open my delegation", async () => {
    await page.goto("/delegation");
    await expect(
      page.getByRole("button", { name: "Save changes" }),
    ).toBeVisible();
  });

  const crisis = prefRow(page, "Crisis committees");
  const nonCrisis = prefRow(page, "Non-crisis committees");
  const small = prefRow(page, "Small committees");
  const large = prefRow(page, "Large committees");

  // Flip between two preference sets so every run saves a real change.
  const flipped = await crisis.getByRole("radio", { name: "Yes" }).isChecked();
  const want: [Locator, Choice][] = flipped
    ? [
        [crisis, "No"],
        [nonCrisis, "Yes"],
        [small, "Neutral"],
        [large, "Yes"],
      ]
    : [
        [crisis, "Yes"],
        [nonCrisis, "Neutral"],
        [small, "No"],
        [large, "Neutral"],
      ];

  const versionText = page.getByText(/^Version \d+$/);
  const before = Number((await versionText.textContent())!.split(" ")[1]);

  await step("set preferences", async () => {
    for (const [row, choice] of want) await choose(row, choice);
  });

  await step("save", async () => {
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(versionText).toHaveText(`Version ${before + 1}`);
    await expect(page.locator("div.bg-red-50")).toBeHidden();
  });

  await step("reload shows saved preferences", async () => {
    await page.reload();
    await expect(versionText).toHaveText(`Version ${before + 1}`);
    for (const [row, choice] of want) {
      await expect(row.getByRole("radio", { name: choice })).toBeChecked();
    }
  });
});
