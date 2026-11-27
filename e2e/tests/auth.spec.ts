import { expect, test } from "../fixtures";
import { expireAccessToken } from "../support/ddb";
import {
  clearCognitoErrors,
  failNextCognitoCall,
  idToken,
  putCognitoUser,
  refreshCount,
  revokeRefreshTokens,
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

test("remember me decides whether a browser restart signs you out", async ({
  browser,
  page,
  step,
}) => {
  const advisor = seedUsers.advisor;

  // A restarted browser keeps persistent cookies and drops session ones.
  async function restartBrowser() {
    const kept = (await page.context().cookies()).filter((c) => c.expires > 0);
    const context = await browser.newContext({
      baseURL: "http://localhost:4173",
    });
    await context.addCookies(kept);
    return context.newPage();
  }

  async function signIn(remember: boolean) {
    await page.goto("/sign-in");
    await page.getByLabel("Email").fill(advisor.email);
    await page.getByLabel("Password").fill(seedPassword);
    await page.getByLabel("Remember me on this device").setChecked(remember);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByText(advisor.name)).toBeVisible();
  }

  await step("without it, a restart signs you out", async () => {
    await signIn(false);
    const restarted = await restartBrowser();
    await restarted.goto("/dashboard");
    await expect(restarted).toHaveURL(/\/sign-in$/);
    await restarted.context().close();
  });

  await step("with it, you stay signed in for 30 days", async () => {
    await page.context().clearCookies();
    await signIn(true);
    const session = (await page.context().cookies()).find(
      (c) => c.name === "numun_session",
    )!;
    const days = (session.expires - Date.now() / 1000) / 86_400;
    expect(days).toBeGreaterThan(29.9);
    const restarted = await restartBrowser();
    await restarted.goto("/dashboard");
    await expect(restarted.getByText(advisor.name)).toBeVisible();
    await restarted.context().close();
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

test("the API only accepts genuine ID tokens", async ({ page, step }) => {
  const email = `tokens.${Date.now()}@example.com`;
  await putCognitoUser({
    email,
    password: "token test pass 1",
    name: "Token Tester",
  });
  const exchange = async (token: string) =>
    page.request.post("/numun.v1.AuthService/Exchange", {
      headers: { "Connect-Protocol-Version": "1" },
      data: {
        idToken: token,
        accessToken: "access",
        refreshToken: "refresh",
        expiresIn: 3600,
      },
    });

  await step(
    "forged, expired and misaddressed tokens are refused",
    async () => {
      await page.goto("/sign-in");
      for (const variant of ["forged", "expired", "wrong-audience"] as const) {
        const resp = await exchange(await idToken(email, variant));
        expect(resp.status(), variant).toBe(401);
        expect(await resp.text(), variant).toContain("invalid id token");
      }
      const cookies = await page.context().cookies();
      expect(cookies.find((c) => c.name === "numun_session")).toBeUndefined();
    },
  );

  await step("a genuine token starts a session", async () => {
    const resp = await exchange(await idToken(email));
    expect(resp.status()).toBe(200);
    await page.goto("/dashboard");
    await expect(page.getByText("Token Tester")).toBeVisible();
  });
});

test("expired access tokens are refreshed until the session is revoked", async ({
  page,
  step,
}) => {
  const email = `refresh.${Date.now()}@example.com`;
  const password = "refresh test pass 1";
  await putCognitoUser({ email, password, name: "Refresh Tester" });
  const sessionId = async () =>
    (await page.context().cookies()).find((c) => c.name === "numun_session")!
      .value;

  await step("sign in", async () => {
    await page.goto("/sign-in");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill(password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByText("Refresh Tester")).toBeVisible();
    expect(await refreshCount(email)).toBe(0);
  });

  await step("an expired access token is refreshed silently", async () => {
    await expireAccessToken(await sessionId());
    await page.reload();
    await expect(page.getByText("Refresh Tester")).toBeVisible();
    expect(await refreshCount(email)).toBe(1);
    // The refreshed token is cached; the next request doesn't refresh again.
    await page.reload();
    await expect(page.getByText("Refresh Tester")).toBeVisible();
    expect(await refreshCount(email)).toBe(1);
  });

  await step("once Cognito revokes it, the user is signed out", async () => {
    await revokeRefreshTokens(email);
    await expireAccessToken(await sessionId());
    await page.reload();
    await expect(page).toHaveURL(/\/sign-in\?ended=1$/);
    await expect(
      page.getByText("Your session ended. Sign in again to continue."),
    ).toBeVisible();
    const cookies = await page.context().cookies();
    expect(cookies.find((c) => c.name === "numun_session")).toBeUndefined();
  });
});
