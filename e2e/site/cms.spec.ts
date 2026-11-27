import { expect, test } from "../fixtures";

// The Decap CMS bundle loads from unpkg, so this needs network access (CI has
// it). The OAuth round trip with GitHub itself is unit-tested in
// api/internal/cmsoauth; here we stop at the hand-off.

test("the CMS loads its config and hands sign-in to GitHub", async ({
  page,
  context,
  step,
}) => {
  const configErrors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error" && /config/i.test(m.text())) {
      configErrors.push(m.text());
    }
  });

  await step("the login screen renders", async () => {
    await page.goto("/cms/");
    await expect(
      page.getByRole("button", { name: /Login with GitHub/i }),
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      page.getByText(/Error loading the CMS configuration/i),
    ).toHaveCount(0);
    expect(configErrors).toEqual([]);
  });

  await step("Login goes through our OAuth proxy to GitHub", async () => {
    // Intercept the proxy's response (Playwright doesn't route redirect
    // targets) and keep the popup off GitHub.
    let location = "";
    await context.route("**/cms-oauth/auth**", async (route) => {
      const resp = await route.fetch({ maxRedirects: 0 });
      expect(resp.status()).toBe(302);
      location = resp.headers()["location"] ?? "";
      await route.fulfill({ status: 200, body: "Redirecting to GitHub…" });
    });
    const [popup] = await Promise.all([
      page.waitForEvent("popup"),
      page.getByRole("button", { name: /Login with GitHub/i }).click(),
    ]);
    await expect.poll(() => location).not.toBe("");
    const url = new URL(location);
    expect(url.origin + url.pathname).toBe(
      "https://github.com/login/oauth/authorize",
    );
    expect(url.searchParams.get("client_id")).toBe("e2e-cms-client");
    expect(url.searchParams.get("scope")).toBe("repo");
    expect(url.searchParams.get("redirect_uri")).toMatch(
      /\/cms-oauth\/callback$/,
    );
    expect(url.searchParams.get("state")).toBeTruthy();
    await popup.close();
  });
});
