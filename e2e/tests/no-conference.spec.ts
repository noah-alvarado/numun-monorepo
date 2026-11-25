// Between conferences nothing is open: every role should be told so rather
// than shown empty or broken screens. The seed conference is closed for the
// test and reopened after it.

import { expect, test } from "../fixtures";
import { rpc } from "../support/api";
import { seedConferenceId, seedUsers } from "../support/seed";

const admin = seedUsers.admin.id;

async function setSeedStatus(status?: string): Promise<string> {
  const { conference } = await rpc<{
    conference: { status: string; version: number };
  }>(admin, "ConferenceService/GetConference", {
    conferenceId: seedConferenceId,
  });
  if (status && status !== conference.status) {
    await rpc(admin, "ConferenceService/UpdateConference", {
      conferenceId: seedConferenceId,
      status,
      expectedVersion: conference.version,
    });
  }
  return conference.status;
}

let original = "";
test.beforeEach(async () => {
  original = await setSeedStatus("STATUS_CLOSED");
});
test.afterEach(async () => {
  await setSeedStatus(original);
});

test("between conferences each role is told nothing is open", async ({
  page,
  signInAs,
  step,
}) => {
  await signInAs("advisor");
  await step("advisor dashboard", async () => {
    await expect(
      page.getByText("Registration isn't open right now."),
    ).toBeVisible();
  });
  await step("advisor's delegation page", async () => {
    await page.goto("/delegation");
    await expect(page.getByText(/No active conference/)).toBeVisible();
    await expect(page.getByLabel("School name")).toHaveCount(0);
  });

  await signInAs("admin");
  await step("admin dashboard", async () => {
    await expect(page.getByText("No conference is open.")).toBeVisible();
    await expect(page.getByRole("button", { name: /^Committees/ })).toHaveCount(
      0,
    );
  });
  for (const [title, path] of [
    ["delegations", "/admin/delegations"],
    ["awards", "/awards"],
    ["check-in", "/admin/check-in"],
    ["exports", "/exports"],
  ]) {
    await step(`admin ${title}`, async () => {
      await page.goto(path);
      await expect(page.getByText("No active conference.")).toBeVisible();
      await expect(page.getByText(/for this conference/)).toHaveCount(0);
    });
  }
});
