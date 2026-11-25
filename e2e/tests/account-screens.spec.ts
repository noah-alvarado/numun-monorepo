import { expect, test } from "../fixtures";

// Dev-bypass short-circuits Cognito, so these cover the portal's own
// validation and screen-to-screen wiring, not Cognito itself.

test("sign-up validates the phone and walks through verification", async ({
  page,
  step,
}) => {
  const email = "new.advisor@example.com";

  await step("invalid phone is flagged", async () => {
    await page.goto("/sign-in");
    await page.getByRole("link", { name: "Create account" }).click();
    await expect(
      page.getByRole("heading", { name: "Create your account" }),
    ).toBeVisible();
    await page.getByLabel("Full name").fill("New Advisor");
    await page.getByLabel("Email", { exact: true }).fill(email);
    await page.getByLabel("Phone").fill("123");
    await page.getByLabel(/Password/).fill("correct horse 1battery");
    await page.getByRole("button", { name: "Sign up" }).click();
    await expect(page.getByLabel("Phone")).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await expect(page.locator("#phone-error")).toBeVisible();
  });

  await step("valid phone submits", async () => {
    await page.getByLabel("Phone").fill("8472462882");
    await page.getByRole("button", { name: "Sign up" }).click();
    await expect(
      page.getByText(/Check your email for a verification code/),
    ).toBeVisible();
  });

  await step("verify with the emailed code", async () => {
    await page.getByRole("button", { name: "I have my code" }).click();
    await expect(page).toHaveURL(/\/sign-up\/verify\?email=/);
    await expect(page.getByLabel("Email", { exact: true })).toHaveValue(email);
    await page.getByLabel("Verification code").fill("123456");
    await page.getByRole("button", { name: "Verify" }).click();
    await expect(
      page.getByText("Verified. Redirecting to sign-in…"),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/sign-in$/);
  });
});

test("forgot password leads to reset and back to sign-in", async ({
  page,
  step,
}) => {
  const email = "advisor@seed.numun.local";

  await step("request a reset code", async () => {
    await page.goto("/sign-in");
    await page.getByRole("link", { name: "Forgot password?" }).click();
    await page.getByLabel("Email", { exact: true }).fill(email);
    await page.getByRole("button", { name: "Send code" }).click();
    await expect(page).toHaveURL(/\/sign-in\/reset\?email=/);
    await expect(page.getByLabel("Email", { exact: true })).toHaveValue(email);
  });

  await step("set a new password", async () => {
    await page.getByLabel("Code from email").fill("123456");
    await page
      .getByLabel(/New password|Password/)
      .fill("another 1good passphrase");
    await page.getByRole("button", { name: "Reset password" }).click();
    await expect(page).toHaveURL(/\/sign-in$/);
  });
});
