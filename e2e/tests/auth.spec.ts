import { expect, test } from "../fixtures";
import { seedUsers } from "../support/seed";

test("advisor signs in through the form and signs out", async ({
  page,
  step,
}) => {
  const advisor = seedUsers.advisor;

  await step("open sign-in", async () => {
    await page.goto("/sign-in");
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  });

  await step("submit seed advisor id", async () => {
    // Dev-bypass mode takes the seed user id in the email field.
    await page.getByLabel("Email").fill(advisor.id);
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
