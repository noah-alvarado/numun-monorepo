import { expect, test } from "../fixtures";
import {
  approveDelegation,
  createAdvisor,
  createDelegation,
  uniqueSchool,
} from "../support/api";
import { getCognitoUser } from "../support/fakes";
import { showMail, waitForMail } from "../support/mail";
import { seedConferenceName } from "../support/seed";

test("admin creates a conference and can't open a second one", async ({
  page,
  signInAs,
  step,
}) => {
  const name = `E2E Conference ${Date.now()}`;
  const dialog = () => page.getByRole("dialog");
  const row = () => page.getByRole("row").filter({ hasText: name });
  await signInAs("admin");

  await step("create a draft conference", async () => {
    await page.getByRole("button", { name: /^Conferences/ }).click();
    await page.getByRole("button", { name: "New conference" }).click();
    await dialog().getByLabel("Name").fill(name);
    await dialog().getByLabel("Edition").fill("99");
    await dialog().getByLabel("Year").fill("2031");
    await dialog().getByLabel("Starts").fill("2031-04-03");
    await dialog().getByLabel("Ends").fill("2031-04-01");
    await dialog().getByRole("button", { name: "Save" }).click();
    await expect(
      dialog().getByText("The conference must end after it starts."),
    ).toBeVisible();
    await dialog().getByLabel("Ends").fill("2031-04-05");
    await dialog().getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(`Created ${name}.`)).toBeVisible();
    await expect(row()).toContainText("Draft");
    await expect(row()).toContainText("2031-04-03 → 2031-04-05");
  });

  await step("opening it while another is open is refused", async () => {
    await row()
      .getByRole("button", { name: `Edit ${name}` })
      .click();
    await expect(dialog().getByLabel("Name")).toHaveValue(name);
    await expect(
      dialog().getByLabel("Status").locator("option:checked"),
    ).toHaveText("Draft");
    await expect(dialog().getByLabel("Starts")).toHaveValue("2031-04-03");
    await dialog()
      .getByLabel("Status")
      .selectOption({ label: "Open for registration" });
    await dialog().getByRole("button", { name: "Save" }).click();
    await expect(dialog()).toContainText(
      `${seedConferenceName} is already open or in progress`,
    );
  });

  await step("close it instead", async () => {
    await dialog().getByLabel("Status").selectOption({ label: "Closed" });
    await dialog().getByRole("button", { name: "Save" }).click();
    await expect(dialog()).toBeHidden();
    await expect(row()).toContainText("Closed");
  });
});

test("admin invites a staffer, who signs in with the emailed password", async ({
  page,
  signInAs,
  step,
}) => {
  const stamp = Date.now();
  const email = `staff.${stamp}@example.com`;
  const name = `E2E Invited ${stamp}`;
  const invite = () => page.getByRole("form", { name: "Invite staff" });
  await signInAs("admin");

  await step("invite a staffer", async () => {
    await page.goto("/admin/staff");
    await invite().getByLabel("Email").fill(email);
    await invite().getByLabel("Name").fill(name);
    await invite().getByLabel("Role").selectOption({ label: "Staffer" });
    await invite().getByRole("button", { name: "Send invite" }).click();
    await expect(
      page.getByText(
        `Invited ${email}. They'll get an email with a temporary password.`,
      ),
    ).toBeVisible();
    await expect(page.getByRole("row").filter({ hasText: name })).toContainText(
      "Staffer",
    );
  });

  await step("inviting the same email again is refused", async () => {
    await invite().getByLabel("Email").fill(email);
    await invite().getByRole("button", { name: "Send invite" }).click();
    await expect(
      page.getByText("An account already uses that email."),
    ).toBeVisible();
  });

  await step("the staffer signs in and sets a password", async () => {
    const { password } = await getCognitoUser(email);
    await page.goto("/dashboard");
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill(password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.getByLabel(/New password/).fill("my own staff pass 1");
    await page
      .getByRole("button", { name: "Set password and continue" })
      .click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(page.getByText(name)).toBeVisible();
    await expect(page.getByText("Staffer", { exact: true })).toBeVisible();
  });
});

test("an admin sends an announcement and an advisor unsubscribes", async ({
  page,
  signInAs,
  step,
}) => {
  const advisor = await createAdvisor("Announce", { withEmail: true });
  await approveDelegation(
    await createDelegation(advisor.id, uniqueSchool("Announce")),
  );
  const subject = `E2E Announcement ${Date.now()}`;
  await signInAs(advisor);

  await step("the profile rejects a bad phone number", async () => {
    await page.getByRole("link", { name: "Edit profile" }).click();
    await expect(page).toHaveURL(/\/profile$/);
    await expect(page.getByText(advisor.email!)).toBeVisible();
    await page.getByLabel("Phone").fill("123");
    await page.getByRole("button", { name: "Save profile" }).click();
    await expect(page.locator("#phone-error")).toBeVisible();
  });

  await step("save a phone; announcements are on by default", async () => {
    await page.getByLabel("Phone").fill("847 246 2882");
    await expect(page.getByLabel(/Email me NUMUN announcements/)).toBeChecked();
    await page.getByRole("button", { name: "Save profile" }).click();
    await expect(page.getByText("Profile saved.")).toBeVisible();
    await page.reload();
    await expect(page.getByLabel("Phone")).toHaveValue("+18472462882");
    await expect(page.getByLabel(/Email me NUMUN announcements/)).toBeChecked();
  });

  await step("the advisor's exports cover their delegation", async () => {
    await page.goto("/exports");
    await expect(page.getByText(/Files cover your delegation\./)).toBeVisible();
  });

  await signInAs("admin");
  const form = () => page.getByRole("form", { name: "New announcement" });

  await step("compose and review the audience", async () => {
    await page.goto("/admin/announcements");
    await form()
      .getByLabel("To")
      .selectOption({ label: "Advisors of approved delegations" });
    await form().getByLabel("Subject").fill(subject);
    await form()
      .getByLabel(/Message/)
      .fill("Position papers are due Friday.\n\nSee you in Evanston!");
    await form().getByRole("button", { name: "Review and send" }).click();
    await expect(page.getByRole("dialog")).toContainText(
      /^Send to \d+ advisors?\?/,
    );
    await expect(page.getByRole("dialog")).toContainText(subject);
  });

  await step("send it", async () => {
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Send" })
      .click();
    await expect(
      page.getByText(new RegExp(`Sent "${subject}" to \\d+ advisors?\\.`)),
    ).toBeVisible();
    await expect(
      page.getByRole("listitem").filter({ hasText: subject }),
    ).toContainText("Advisors of approved delegations");
  });

  let unsubscribeUrl = "";
  await step("the advisor receives it, with an unsubscribe link", async () => {
    const mail = await waitForMail(advisor.email!, new RegExp(`^${subject}$`));
    await showMail(page, mail);
    expect(mail.from).toBe("NUMUN <announcements@mail.test.numun.org>");
    expect(mail.text).toContain("Position papers are due Friday.");
    expect(mail.html).toMatch(
      /white-space:pre-wrap;">Position papers are due Friday.(\r?\n){2}See you in Evanston!</,
    );
    expect(mail.headers["List-Unsubscribe-Post"]).toEqual([
      "List-Unsubscribe=One-Click",
    ]);
    unsubscribeUrl = /href="([^"]+)"[^>]*>Unsubscribe from announcements/
      .exec(mail.html)![1]
      .replace(/&amp;/g, "&");
    expect(mail.headers["List-Unsubscribe"]).toEqual([`<${unsubscribeUrl}>`]);
  });

  await step("the link asks before unsubscribing", async () => {
    await page.goto(unsubscribeUrl);
    await expect(
      page.getByRole("heading", {
        name: "Unsubscribe from NUMUN announcements?",
      }),
    ).toBeVisible();
    await expect(page.getByText(advisor.email!)).toBeVisible();
  });

  await step("confirming unsubscribes them", async () => {
    await page.getByRole("button", { name: "Unsubscribe" }).click();
    await expect(
      page.getByRole("heading", { name: "You've been unsubscribed" }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Open your profile" }),
    ).toHaveAttribute("href", "http://localhost:4173/profile");
  });

  await signInAs(advisor);
  await step("their profile shows announcements off", async () => {
    await page.goto("/profile");
    await expect(
      page.getByLabel(/Email me NUMUN announcements/),
    ).not.toBeChecked();
  });
});

test("an announcement with no one to receive it can't be sent", async ({
  page,
  signInAs,
  step,
}) => {
  // The shared table always has advisors to send to, so stand in for an
  // audience that resolves to nobody (e.g. before any delegation is
  // approved).
  await page.route("**/numun.v1.AnnouncementService/PreviewSendAudience", (r) =>
    r.fulfill({ contentType: "application/json", body: "{}" }),
  );
  await signInAs("admin");

  await step("the review explains why", async () => {
    await page.goto("/admin/announcements");
    const form = page.getByRole("form", { name: "New announcement" });
    await form.getByLabel("Subject").fill("Nobody home");
    await form.getByLabel(/Message/).fill("Hello?");
    await form.getByRole("button", { name: "Review and send" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("No one to send to");
    await expect(dialog).toContainText(
      "None of the advisors of approved delegations",
    );
    await expect(dialog.getByRole("button", { name: "Send" })).toHaveCount(0);
    await dialog.getByRole("button", { name: "Close" }).click();
    await expect(dialog).toBeHidden();
  });
});
