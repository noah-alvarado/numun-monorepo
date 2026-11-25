import type { Page } from "@playwright/test";

import { expect, test } from "../fixtures";
import {
  assignStaffer,
  createAdvisor,
  createDelegation,
  createDraftConference,
  recordCharge,
  rpc,
  uniqueSchool,
} from "../support/api";
import { expectNoMail, showMail, waitForMail } from "../support/mail";
import { sesHardBounce } from "../support/ses";
import { seedUsers } from "../support/seed";

// Emails the app sends, read back from Mailpit (EMAIL.md §3). The API sends
// transactional mail directly; the email worker sends queued mail.

const status = (page: Page) => page.locator("span.uppercase").first();

test("advisors are emailed when their delegation is approved or rejected", async ({
  page,
  signInAs,
  step,
}) => {
  const approved = await createAdvisor("MailApproved", { withEmail: true });
  const rejected = await createAdvisor("MailRejected", { withEmail: true });
  const approvedDel = await createDelegation(
    approved.id,
    uniqueSchool("MailApprove"),
  );
  const rejectedDel = await createDelegation(
    rejected.id,
    uniqueSchool("MailReject"),
  );
  await signInAs("admin");

  await step("approving emails the advisor", async () => {
    await page.goto(`/delegations/${approvedDel.id}`);
    await page.getByRole("button", { name: "Approve" }).click();
    await expect(status(page)).toHaveText("Approved");
    const mail = await waitForMail(
      approved.email!,
      /^Your NUMUN delegation has been approved$/,
    );
    await showMail(page, mail);
    expect(mail.from).toBe("NUMUN Portal <noreply@mail.test.numun.org>");
    expect(mail.text).toContain(`"${approvedDel.school}"`);
    expect(mail.text).toContain("http://localhost:4173");
    // Transactional mail can't be unsubscribed from (EMAIL.md §2.2).
    expect(mail.headers["List-Unsubscribe"]).toBeUndefined();
  });

  await step("rejecting emails the advisor the reason", async () => {
    await page.goto(`/delegations/${rejectedDel.id}`);
    await page.getByRole("button", { name: "Reject" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("included in the email to the advisors");
    await dialog
      .getByLabel(/Reason/)
      .fill("Registration closed for new schools");
    await dialog.getByRole("button", { name: "Reject" }).click();
    await expect(status(page)).toHaveText("Rejected");
    const mail = await waitForMail(
      rejected.email!,
      /^Update on your NUMUN delegation$/,
    );
    await showMail(page, mail);
    expect(mail.text).toContain("was not approved");
    expect(mail.text).toContain("Reason: Registration closed for new schools");
  });
});

test("advisors are emailed about payments and imports", async ({
  page,
  signInAs,
  step,
}) => {
  const advisor = await createAdvisor("MailLedger", { withEmail: true });
  const delegation = await createDelegation(
    advisor.id,
    uniqueSchool("MailLedger"),
  );

  await step("recording a charge emails the new balance", async () => {
    await recordCharge(delegation.id, 450, "MAIL-1");
    const mail = await waitForMail(
      advisor.email!,
      /^Payment recorded for your NUMUN delegation$/,
    );
    await showMail(page, mail);
    expect(mail.text).toContain(`"${delegation.school}"`);
    expect(mail.text).toContain("New balance due: $450.00");
  });

  await signInAs(advisor);
  await step("committing an import emails a summary", async () => {
    await page.goto(`/delegations/${delegation.id}/delegates/import`);
    await page.getByLabel("File").setInputFiles({
      name: "roster.csv",
      mimeType: "text/csv",
      buffer: Buffer.from(
        "First Name,Last Name\r\nAda,Lovelace\r\nGrace,Hopper\r\n",
      ),
    });
    await page.getByRole("button", { name: "Preview import" }).click();
    await page.getByRole("button", { name: "Confirm import" }).click();
    await expect(page.getByText("Imported 2 delegates")).toBeVisible();
    const mail = await waitForMail(
      advisor.email!,
      /^Delegate import committed$/,
    );
    await showMail(page, mail);
    expect(mail.text).toContain("(additive mode)");
    expect(mail.text).toContain("2 delegates created");
    expect(mail.text).toContain("0 delegates unchanged");
    expect(mail.text).toContain(`/delegations/${delegation.id}?tab=delegates`);
  });
});

test("staffers are emailed when assigned to a delegation", async ({
  page,
  newAdvisor,
  step,
}) => {
  const delegation = await createDelegation(
    newAdvisor.id,
    uniqueSchool("MailStaff"),
  );

  await step("assigning a staffer emails them", async () => {
    await assignStaffer(delegation.id, seedUsers.staffer.id);
    const mail = await waitForMail(
      seedUsers.staffer.email,
      /^Your NUMUN account access changed$/,
      { containing: delegation.school },
    );
    await showMail(page, mail);
    expect(mail.text).toContain(
      `You were assigned to oversee delegation ${delegation.school}.`,
    );
  });
});

test("admins get a summary of new registrations", async ({ page, step }) => {
  // A fresh conference opens a fresh summary window. The worker sends it
  // after DEV_REGISTRATION_SUMMARY_DELAY instead of 15 minutes.
  const conferenceId = await createDraftConference("MailSummary");
  const { conference } = await rpc<{ conference: { name: string } }>(
    seedUsers.admin.id,
    "ConferenceService/GetConference",
    { conferenceId },
  );
  const advisor = await createAdvisor("MailSummary", { withEmail: true });
  const school = uniqueSchool("MailSummary");

  await step("the first registration is in the summary", async () => {
    await createDelegation(advisor.id, school, { conferenceId });
    const mail = await waitForMail(
      seedUsers.admin.email,
      /^New NUMUN delegations registered$/,
      { containing: school, timeout: 20_000 },
    );
    await showMail(page, mail);
    expect(mail.text).toContain(
      `registered for ${conference.name} in the last 15 minutes`,
    );
    expect(mail.text).toContain(school);
    expect(mail.text).toContain(advisor.email!);
  });
});

test("suppressed addresses get no mail", async ({ step }) => {
  const advisor = await createAdvisor("MailBounced", { withEmail: true });
  const delegation = await createDelegation(
    advisor.id,
    uniqueSchool("MailBounced"),
  );

  await step("after a hard bounce, approval sends nothing", async () => {
    await sesHardBounce(advisor.email!);
    await rpc(seedUsers.admin.id, "DelegationService/Approve", {
      delegationId: delegation.id,
      expectedVersion: delegation.version,
    });
    await expectNoMail(advisor.email!, /approved/);
  });
});
