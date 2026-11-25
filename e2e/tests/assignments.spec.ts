import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import {
  approveDelegation,
  createAdvisor,
  createCommittee,
  createDelegate,
  createDelegation,
  createDraftConference,
  rpc,
  uniqueSchool,
  uniqueSurname,
} from "../support/api";
import { showMail, waitForMail } from "../support/mail";
import { seedUsers } from "../support/seed";

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
  return { conferenceId, delegationId: delegation.id, names };
}

test("admin proposes, approves and re-runs assignments", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  const { conferenceId, delegationId, names } = await arrange(newAdvisor.id, 3);
  await signInAs("admin");

  await step("open an empty studio", async () => {
    await page.goto(`/conferences/${conferenceId}/assignments`);
    await expect(emptyState(page)).toBeVisible();
    // A draft conference isn't the session's active one, so this exercises
    // the GetConference lookup.
    await expect(page.locator("header").first()).toContainText(
      /E2E Assignments \w{8} \(\d{4}\)/,
    );
    await expect(page.locator("header").first()).not.toContainText(
      conferenceId,
    );
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

  await step("the admin who ran it is emailed", async () => {
    const { conference } = await rpc<{ conference: { name: string } }>(
      seedUsers.admin.id,
      "ConferenceService/GetConference",
      { conferenceId },
    );
    const mail = await waitForMail(
      seedUsers.admin.email,
      new RegExp(`^Assignment run completed for ${conference.name}$`),
    );
    expect(mail.text).toContain(`/conferences/${conferenceId}/assignments`);
    await showMail(page, mail);
  });

  let pinned = "";
  await step("approve one assignment", async () => {
    await page.goto(`/conferences/${conferenceId}/assignments`);
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

  const rosterRow = (name: string) =>
    page.getByRole("row").filter({ hasText: name });

  await step("the roster shows staff the proposal too", async () => {
    await page.goto(`/delegations/${delegationId}?tab=delegates`);
    // The delegation's own (draft) conference, not the active one.
    await expect(
      page.getByText(/^E2E Assignments \w{8} \(\d{4}\)$/),
    ).toBeVisible();
    await expect(rosterRow(names[0])).toContainText(
      /(Security Council|General Assembly) · \w+/,
    );
    await expect(rosterRow(names[2])).toContainText("proposed");
  });

  await signInAs(newAdvisor);
  await step(
    "the advisor's roster shows approved assignments only",
    async () => {
      await page.goto(`/delegations/${delegationId}?tab=delegates`);
      for (const name of names.slice(0, 2)) {
        await expect(rosterRow(name)).toContainText(
          /(Security Council|General Assembly) · \w+/,
        );
      }
      await expect(rosterRow(names[2])).not.toContainText(" · ");
      await expect(rosterRow(names[2])).not.toContainText("proposed");
    },
  );
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

test("admin filters, edits, swaps and approves all", async ({
  page,
  signInAs,
  newAdvisor,
  step,
}) => {
  // Two delegates from the arranged school plus one from a second school:
  // three delegates over four seats, so one position stays free to edit into.
  const { conferenceId, names } = await arrange(newAdvisor.id, 2);
  const otherAdvisor = await createAdvisor("AssignOther");
  const otherSchool = uniqueSchool("AssignOther");
  const other = await createDelegation(otherAdvisor.id, otherSchool, {
    conferenceId,
  });
  await approveDelegation(other);
  const otherSurname = uniqueSurname();
  await createDelegate(otherAdvisor.id, other.id, "Katherine", otherSurname);
  const all = [...names, `Katherine ${otherSurname}`];
  const filter = (label: string) =>
    page.locator("label").filter({ hasText: label }).getByRole("combobox");
  const placement = (name: string) =>
    assignmentRow(page, name).getByRole("cell").nth(2);

  await signInAs("admin");

  await step("propose", async () => {
    await page.goto(`/conferences/${conferenceId}/assignments`);
    await expect(page.getByText("No runs yet")).toBeVisible();
    await page.getByRole("button", { name: "Propose", exact: true }).click();
    await expect(page.getByText("Proposed 3 assignments.")).toBeVisible();
    for (const name of all) {
      await expect(assignmentRow(page, name)).toBeVisible();
    }
    await expect(page.getByText(/^Last run: done · obj /)).toBeVisible();
  });

  await step("filter by committee, and it survives a reload", async () => {
    await filter("Committee").selectOption({ label: "Security Council" });
    await expect(page).toHaveURL(/committee=/);
    await page.reload();
    await expect(filter("Committee").locator("option:checked")).toHaveText(
      "Security Council",
    );
    const rows = page
      .getByRole("row")
      .filter({ has: page.getByRole("checkbox") });
    await expect(rows.first()).toBeVisible();
    for (const text of await rows.allTextContents()) {
      expect(text).toContain("Security Council");
    }
  });

  await step("filter by delegation", async () => {
    await filter("Committee").selectOption({ label: "All" });
    await filter("Delegation").selectOption({ label: otherSchool });
    await expect(page.getByText("1 assignment", { exact: true })).toBeVisible();
    await expect(assignmentRow(page, all[2])).toBeVisible();
  });

  await step("filter by status shows a filtered empty state", async () => {
    await filter("Delegation").selectOption({ label: "All" });
    await filter("Status").selectOption({ label: "Approved" });
    await expect(
      page.getByText("No assignments match these filters."),
    ).toBeVisible();
  });

  await step(
    "edit refuses a full position, then moves to the free one",
    async () => {
      await filter("Status").selectOption({ label: "All" });
      await expect(
        page.getByText("3 assignments", { exact: true }),
      ).toBeVisible();
      const slots = new Map<string, string>();
      for (const name of all) {
        const [committee, position] = await placement(name)
          .locator("div")
          .allTextContents();
        slots.set(`${committee} — ${position}`, name);
      }
      const row = assignmentRow(page, all[0]);
      const dialog = page
        .getByRole("heading", { name: "Edit assignment" })
        .locator("../..");
      const options = dialog.getByRole("combobox").locator("option");

      await row.getByRole("button", { name: "Edit" }).click();
      const full = [...slots].find(([, holder]) => holder !== all[0])![0];
      await dialog.getByRole("combobox").selectOption({ label: full });
      await dialog.getByRole("button", { name: "Save" }).click();
      await expect(dialog.locator("p.bg-red-50")).toHaveText(
        `${full.split(" — ")[1]} is full`,
      );

      const free = (await options.allTextContents()).find(
        (o) => !slots.has(o),
      )!;
      await dialog.getByRole("combobox").selectOption({ label: free });
      await dialog.getByRole("button", { name: "Save" }).click();
      await expect(dialog).toBeHidden();
      const [committee, position] = free.split(" — ");
      await expect(placement(all[0])).toContainText(committee);
      await expect(placement(all[0])).toContainText(position);
      await expect(row).toContainText("manually edited by staff");
    },
  );

  await step("swap exchanges two placements, and swaps back", async () => {
    const before = [
      (await placement(all[0]).textContent()) ?? "",
      (await placement(all[1]).textContent()) ?? "",
    ];
    for (let round = 0; round < 2; round++) {
      for (const name of all.slice(0, 2)) {
        await assignmentRow(page, name)
          .getByRole("checkbox", { name: "Select assignment" })
          .check();
      }
      await page.getByRole("button", { name: "Swap", exact: true }).click();
      await expect(page.getByText(/^\d+ selected$/)).toBeHidden();
      await expect(page.locator("p.bg-red-50")).toHaveCount(0);
      const expected = round === 0 ? [before[1], before[0]] : before;
      await expect(placement(all[0])).toHaveText(expected[0]);
      await expect(placement(all[1])).toHaveText(expected[1]);
    }
  });

  await step("with a filter on, approve only what's shown", async () => {
    await filter("Delegation").selectOption({ label: otherSchool });
    await page.getByRole("button", { name: "Approve shown" }).click();
    const dialog = page.getByRole("dialog", {
      name: "Approve the 1 proposed assignment shown?",
    });
    await expect(dialog).toContainText(
      "Only assignments matching the current filters are approved.",
    );
    await dialog.getByRole("button", { name: "Approve shown" }).click();
    await expect(dialog).toBeHidden();
    await expect(assignmentRow(page, all[2])).toContainText("Approved");
    await filter("Delegation").selectOption({ label: "All" });
    for (const name of all.slice(0, 2)) {
      await expect(assignmentRow(page, name)).toContainText("Proposed");
    }
  });

  await step("approve all", async () => {
    await page.getByRole("button", { name: "Approve all" }).click();
    const dialog = page
      .getByRole("heading", {
        name: "Approve all proposed assignments?",
      })
      .locator("../..");
    await dialog.getByRole("button", { name: "Approve all" }).click();
    await expect(dialog).toBeHidden();
    for (const name of all) {
      await expect(assignmentRow(page, name)).toContainText("Approved");
    }
  });

  await step("run history lists the run", async () => {
    await page.getByRole("button", { name: /Run history/ }).click();
    const history = page.getByRole("row").filter({ hasText: "Done" });
    await expect(history).toHaveCount(1);
    await expect(history).toContainText("3");
  });
});
