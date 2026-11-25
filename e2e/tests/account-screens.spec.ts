import { expect, test } from "../fixtures";
import {
  emailedCode,
  failNextCognitoCall,
  clearCognitoErrors,
  putCognitoUser,
} from "../support/fakes";

// Sign-up and password reset against the fake Cognito, which emails codes
// into its control API instead of an inbox.

test.afterEach(async () => {
  await clearCognitoErrors();
});

test("an advisor signs up, verifies their email and signs in", async ({
  page,
  step,
}) => {
  const email = `new.advisor.${Date.now()}@example.com`;
  const password = "correct horse 1battery";

  await step("invalid phone is flagged", async () => {
    await page.goto("/sign-in");
    await page.getByRole("link", { name: "Create account" }).click();
    await expect(
      page.getByRole("heading", { name: "Create your account" }),
    ).toBeVisible();
    await page.getByLabel("Full name").fill("New Advisor");
    await page.getByLabel("Email", { exact: true }).fill(email);
    await page.getByLabel("Phone").fill("123");
    await page.getByLabel(/Password/).fill(password);
    await page.getByRole("button", { name: "Sign up" }).click();
    await expect(page.getByLabel("Phone")).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await expect(page.locator("#phone-error")).toBeVisible();
  });

  await step("weak password is rejected by Cognito", async () => {
    await page.getByLabel("Phone").fill("8472462882");
    await page.getByLabel(/Password/).fill("ALLCAPSNODIGITS");
    await page.getByRole("button", { name: "Sign up" }).click();
    await expect(
      page.getByText(
        "Use at least 12 characters, including a lowercase letter and a number.",
      ),
    ).toBeVisible();
  });

  await step("valid details submit", async () => {
    await page.getByLabel(/Password/).fill(password);
    await page.getByRole("button", { name: "Sign up" }).click();
    await expect(
      page.getByText(/Check your email for a verification code/),
    ).toBeVisible();
  });

  await step("a wrong code is rejected", async () => {
    await page.getByRole("button", { name: "I have my code" }).click();
    await expect(page).toHaveURL(/\/sign-up\/verify\?email=/);
    await expect(page.getByLabel("Email", { exact: true })).toHaveValue(email);
    await page.getByLabel("Verification code").fill("000000");
    await page.getByRole("button", { name: "Verify" }).click();
    await expect(
      page.getByText(
        "That code doesn't match. Check the email we sent and try again.",
      ),
    ).toBeVisible();
  });

  await step("send a new code", async () => {
    const first = await emailedCode(email, "signup");
    await page.getByRole("button", { name: "Send a new code" }).click();
    await expect(
      page.getByText(`We sent a new code to ${email}.`),
    ).toBeVisible();
    expect(await emailedCode(email, "signup")).not.toBe(first);
  });

  await step("verify with the emailed code", async () => {
    await page
      .getByLabel("Verification code")
      .fill(await emailedCode(email, "signup"));
    await page.getByRole("button", { name: "Verify" }).click();
    await expect(
      page.getByText("Verified. Redirecting to sign-in…"),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/sign-in$/);
  });

  await step("sign in as the new advisor", async () => {
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill(password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(page.getByText("New Advisor")).toBeVisible();
    await expect(page.getByText("Advisor", { exact: true })).toBeVisible();
  });
});

test("signing up with a taken email looks the same as a new one", async ({
  page,
  step,
}) => {
  const email = `taken.${Date.now()}@example.com`;
  await putCognitoUser({ email, password: "existing pass 1" });

  await step("submit the taken email", async () => {
    await page.goto("/sign-up");
    await page.getByLabel("Full name").fill("Someone Else");
    await page.getByLabel("Email", { exact: true }).fill(email);
    await page.getByLabel("Phone").fill("8472462882");
    await page.getByLabel(/Password/).fill("another good pass 1");
    await page.getByRole("button", { name: "Sign up" }).click();
    await expect(
      page.getByText(/Check your email for a verification code/),
    ).toBeVisible();
  });
});

test("forgot password resets it and the new one signs in", async ({
  page,
  step,
}) => {
  const email = `forgetful.${Date.now()}@example.com`;
  await putCognitoUser({
    email,
    password: "original pass 1",
    name: "Forgetful Advisor",
  });

  await step("request a reset code", async () => {
    await page.goto("/sign-in");
    await page.getByRole("link", { name: "Forgot password?" }).click();
    await page.getByLabel("Email", { exact: true }).fill(email);
    await page.getByRole("button", { name: "Send code" }).click();
    await expect(page).toHaveURL(/\/sign-in\/reset\?email=/);
    await expect(page.getByLabel("Email", { exact: true })).toHaveValue(email);
  });

  await step("a wrong code is rejected", async () => {
    await page.getByLabel("Code from email").fill("000000");
    await page
      .getByLabel(/New password|Password/)
      .fill("another 1good passphrase");
    await page.getByRole("button", { name: "Reset password" }).click();
    await expect(
      page.getByText(
        "That code doesn't match. Check the email we sent and try again.",
      ),
    ).toBeVisible();
  });

  await step("an expired code asks for a new one", async () => {
    await failNextCognitoCall(
      "ConfirmForgotPassword",
      "ExpiredCodeException",
      "Invalid code provided, please request a code again.",
    );
    await page
      .getByLabel("Code from email")
      .fill(await emailedCode(email, "reset"));
    await page.getByRole("button", { name: "Reset password" }).click();
    await expect(
      page.getByText("That code has expired. Request a new one."),
    ).toBeVisible();
  });

  await step("set a new password with the emailed code", async () => {
    await page.getByRole("button", { name: "Reset password" }).click();
    await expect(page).toHaveURL(/\/sign-in\?reset=1$/);
    await expect(
      page.getByText("Password changed. Sign in with your new password."),
    ).toBeVisible();
  });

  await step("the old password no longer works", async () => {
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill("original pass 1");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByText("Incorrect email or password.")).toBeVisible();
  });

  await step("the new password signs in", async () => {
    await page.getByLabel("Password").fill("another 1good passphrase");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(page.getByText("Forgetful Advisor")).toBeVisible();
  });
});
