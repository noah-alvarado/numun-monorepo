import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import {
  approveDelegation,
  createCommittee,
  createDelegate,
  createDelegation,
  createDraftConference,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";

function assignmentRow(page: Page, delegate: string) {
  return page.getByRole("row").filter({ hasText: delegate });
}

function emptyState(page: Page) {
  return page.getByText("No assignments yet. Use Propose to generate.");
}

// One approved delegation with `delegates` delegates, in its own draft
// conference with two committees of two single-seat positions each.
async function arrange(advisorId: string, delegates: number) {
  const conferenceId = await createDraftConference("Assignments");
  await createCommittee(conferenceId, "Security Council", "CRISIS", [
    "France",
    "Kenya",
  ]);
  await createCommittee(conferenceId, "General Assembly", "NON_CRISIS", [
    "Brazil",
    "Japan",
  ]);
  const delegation = await createDelegation(advisorId, uniqueSchool("Assign"), {
    conferenceId,
    committeePreferences: {
      type: { crisis: "TRINARY_POSITIVE", nonCrisis: "TRINARY_NEUTRAL" },
    },
  });
  await approveDelegation(delegation);
  const surname = uniqueSurname();
  const names: string[] = [];
  for (const first of ["Ada", "Grace", "Alan", "Edsger", "Barbara"].slice(
    0,
    delegates,
  )) {
    await createDelegate(advisorId, delegation.id, first, surname);
    names.push(`${first} ${surname}`);
  }
  return { conferenceId, names };
}

test("admin proposes, approves and re-runs assignments", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const { conferenceId, names } = await arrange(newAdvisor.id, 3);
  await signInAs("admin");

  await step("open an empty studio", async () => {
    await page.goto(`/conferences/${conferenceId}/assignments`);
    await expect(emptyState(page)).toBeVisible();
  });

  await step("dry run proposes without saving", async () => {
    await page.getByRole("button", { name: "Dry run" }).click();
    await expect(
      page.getByText(/Dry run complete — objective .*, 3 assignments\./),
    ).toBeVisible();
    await expect(emptyState(page)).toBeVisible();
  });

  await step("propose persists assignments", async () => {
    await page.getByRole("button", { name: "Propose", exact: true }).click();
    await expect(page.getByText("Proposed 3 assignments.")).toBeVisible();
    for (const name of names) {
      await expect(assignmentRow(page, name)).toContainText("Proposed");
    }
    await expect(
      page.getByText("3 assignments", { exact: true }),
    ).toBeVisible();
  });

  let pinned = "";
  await step("approve one assignment", async () => {
    const row = assignmentRow(page, names[0]);
    pinned = (await row.getByRole("cell").nth(2).textContent()) ?? "";
    await row.getByRole("button", { name: "Approve" }).click();
    await expect(row).toContainText("Approved");
    await expect(row.getByRole("button", { name: "Unapprove" })).toBeVisible();
  });

  await step("re-run keeps the approved assignment pinned", async () => {
    await page.getByRole("button", { name: "Re-shuffle" }).click();
    await expect(page.getByText("Proposed 2 assignments.")).toBeVisible();
    const row = assignmentRow(page, names[0]);
    await expect(row).toContainText("Approved");
    await expect(row.getByRole("cell").nth(2)).toHaveText(pinned);
  });

  await step("bulk-approve the rest", async () => {
    for (const name of names.slice(1)) {
      await assignmentRow(page, name)
        .getByRole("checkbox", { name: "Select assignment" })
        .check();
    }
    await expect(page.getByText("2 selected")).toBeVisible();
    await page
      .getByRole("button", { name: "Approve selected", exact: true })
      .click();
    for (const name of names) {
      await expect(assignmentRow(page, name)).toContainText("Approved");
    }
    await expect(
      page.getByRole("button", { name: "Approve all" }),
    ).toBeDisabled();
  });

  await step("unapprove returns it to proposed", async () => {
    const row = assignmentRow(page, names[2]);
    await row.getByRole("button", { name: "Unapprove" }).click();
    await expect(row).toContainText("Proposed");
    await expect(
      page.getByRole("button", { name: "Approve all" }),
    ).toBeEnabled();
  });
});

test("infeasible inputs fail with a diagnostic and save nothing", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  // Five delegates from one school can't fit two per committee across two
  // committees (H3), and there are only four seats.
  const { conferenceId } = await arrange(newAdvisor.id, 5);
  await signInAs("admin");

  await step("propose an infeasible conference", async () => {
    await page.goto(`/conferences/${conferenceId}/assignments`);
    await page.getByRole("button", { name: "Propose", exact: true }).click();
    await expect(page.locator("aside p.bg-amber-50")).toContainText(
      /infeasible/,
    );
    await expect(emptyState(page)).toBeVisible();
  });
});
