import { expect, test } from "../fixtures";
import {
  clearCognitoErrors,
  failNextCognitoCall,
  putCognitoUser,
} from "../support/fakes";
import { seedPassword, seedUsers } from "../support/seed";

// Sign-in goes through the fake Cognito (fakes/server.ts) exactly as it
// would through the real one; only the token signature isn't checked.

test.afterEach(async () => {
  await clearCognitoErrors();
});

test("advisor signs in through the form and signs out", async ({
  page,
  step,
}) => {
  const advisor = seedUsers.advisor;

  await step("open sign-in", async () => {
    await page.goto("/sign-in");
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  });

  await step("sign in with email and password", async () => {
    await page.getByLabel("Email").fill(advisor.email);
    await page.getByLabel("Password").fill(seedPassword);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(page.getByText(advisor.name)).toBeVisible();
    await expect(page.getByText("Advisor", { exact: true })).toBeVisible();
  });

  await step("sign out", async () => {
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/sign-in$/);
    const cookies = await page.context().cookies();
    expect(cookies.find((c) => c.name === "numun_session")).toBeUndefined();
  });

  await step("protected page redirects to sign-in", async () => {
    await page.goto("/dashboard");
    await expect(page).toHaveURL(/\/sign-in$/);
  });
});

test("sign-in rejects a wrong password and an unknown email alike", async ({
  page,
  step,
}) => {
  await page.goto("/sign-in");

  await step("wrong password", async () => {
    await page.getByLabel("Email").fill(seedUsers.advisor.email);
    await page.getByLabel("Password").fill("not the password 1");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByText("Incorrect email or password.")).toBeVisible();
    await expect(page).toHaveURL(/\/sign-in$/);
  });

  await step("unknown email gets the same message", async () => {
    await page.getByLabel("Email").fill("nobody@example.com");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByText("Incorrect email or password.")).toBeVisible();
  });

  await step("throttled by Cognito", async () => {
    await failNextCognitoCall(
      "InitiateAuth",
      "TooManyRequestsException",
      "Too many requests",
    );
    await page.getByLabel("Email").fill(seedUsers.advisor.email);
    await page.getByLabel("Password").fill(seedPassword);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(
      page.getByText("Too many attempts. Wait a few minutes and try again."),
    ).toBeVisible();
  });
});

test("an unverified advisor is sent to verify their email", async ({
  page,
  step,
}) => {
  const email = `unverified.${Date.now()}@example.com`;
  await putCognitoUser({
    email,
    password: "unverified pass 1",
    name: "Unverified Advisor",
    confirmed: false,
  });

  await step("sign in before verifying", async () => {
    await page.goto("/sign-in");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill("unverified pass 1");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/sign-up\/verify\?email=/);
    await expect(page.getByLabel("Email", { exact: true })).toHaveValue(email);
  });
});

test("invited staff set a new password on first sign-in", async ({
  page,
  step,
}) => {
  const email = `invited.${Date.now()}@example.com`;
  await putCognitoUser({
    email,
    password: "temporary pass 1",
    name: "Invited Staffer",
    role: "staff-staffer",
    mustChangePassword: true,
  });

  await step("sign in with the temporary password", async () => {
    await page.goto("/sign-in");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill("temporary pass 1");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(
      page.getByText("Set a new password to finish signing in."),
    ).toBeVisible();
  });

  await step("a password without a number is rejected", async () => {
    await page.getByLabel("New password").fill("no digits in this one");
    await page
      .getByRole("button", { name: "Set password and continue" })
      .click();
    await expect(
      page.getByText(
        "Use at least 12 characters, including a lowercase letter and a number.",
      ),
    ).toBeVisible();
  });

  await step("set a new password and land on the dashboard", async () => {
    await page.getByLabel("New password").fill("brand new pass 1");
    await page
      .getByRole("button", { name: "Set password and continue" })
      .click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(page.getByText("Invited Staffer")).toBeVisible();
    await expect(page.getByText("Staffer", { exact: true })).toBeVisible();
  });
});
