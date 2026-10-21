import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { admin, advanceClock } from "../support/instance";

async function signIn(page: Page, password = admin.password) {
  await page.getByLabel("Email").fill(admin.email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

const signInHeading = (page: Page) => page.getByRole("heading", { name: "Sign in" });
const shellHeading = (page: Page) =>
  page.getByRole("heading", { level: 1, name: `Welcome, ${admin.name}` });
const personalMenu = (page: Page) => page.getByRole("button", { name: admin.name });

test.describe("signing in and the shell", () => {
  test("offers the sign-in methods this instance has enabled", async ({ page }) => {
    await page.goto("/sign-in");

    await expect(signInHeading(page)).toBeVisible();
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    // No identity provider is configured on this instance, so none is offered.
    await expect(page.getByRole("button")).toHaveCount(1);
  });

  test("answers a wrong password with a message that names no member", async ({ page }) => {
    await page.goto("/sign-in");

    await signIn(page, "not-the-password-at-all");

    const alert = page.getByRole("alert");
    await expect(alert).toHaveText("The email or the password is not right.");
    await expect(alert).not.toContainText(admin.email);
    await expect(page).toHaveURL(/\/sign-in/);
  });

  test("signs a member in to the shell, with the breadcrumb and the personal menu", async ({
    page,
  }) => {
    await page.goto("/sign-in");

    await signIn(page);

    await expect(shellHeading(page)).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toHaveText("Home");
    await expect(personalMenu(page)).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
  });

  test("keeps the session across a reload", async ({ page }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();

    await page.reload();

    await expect(shellHeading(page)).toBeVisible();
  });

  test("signs the member out, back to sign-in", async ({ page }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();

    await personalMenu(page).click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();

    await expect(signInHeading(page)).toBeVisible();
    await expect(page).toHaveURL(/\/sign-in$/);
    // The session is gone, not merely forgotten by the page.
    await page.goto("/");
    await expect(signInHeading(page)).toBeVisible();
  });

  test("sends a deep link to sign-in and back to it afterwards", async ({ page }) => {
    await page.goto("/projects/checkout?tab=environments");

    await expect(signInHeading(page)).toBeVisible();
    await expect(page).toHaveURL(/returnTo=%2Fprojects%2Fcheckout%3Ftab%3Denvironments/);

    await signIn(page);

    await expect(page).toHaveURL(/\/projects\/checkout\?tab=environments$/);
    await expect(personalMenu(page)).toBeVisible();
  });

  test("renders an unknown address inside the shell", async ({ page }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();

    await page.goto("/no/such/page");

    await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
    await expect(page.getByRole("complementary", { name: "Sidebar" })).toBeVisible();
    await expect(personalMenu(page)).toBeVisible();
  });

  test("keeps the chosen theme across a reload", async ({ page }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();

    await personalMenu(page).click();
    await page.getByRole("menuitemradio", { name: "Dark" }).click();
    await page.keyboard.press("Escape");
    await expect(page.locator("html")).toHaveClass(/dark/);

    await page.reload();

    await expect(page.locator("html")).toHaveClass(/dark/);
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  });

  test("returns focus to the trigger after the personal menu closes", async ({ page }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();

    await personalMenu(page).click();
    await expect(page.getByRole("menu")).toBeVisible();
    await page.keyboard.press("Escape");

    await expect(personalMenu(page)).toBeFocused();
  });
});

test.describe("an ended session", () => {
  // The server's clock moves past the idle timeout; every later spec
  // shares the instance, so it moves back however the spec ends.
  test.afterEach(() => advanceClock("0s"));

  test("sends a page that is refused back to sign-in, saying so and keeping the address", async ({
    page,
  }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();
    await page.goto("/projects/checkout");
    await expect(personalMenu(page)).toBeVisible();

    advanceClock("25h");
    await page.reload();

    await expect(signInHeading(page)).toBeVisible();
    await expect(page.getByRole("status")).toHaveText(
      "Your session has ended. Sign in again to continue.",
    );
    await expect(page).toHaveURL(/returnTo=%2Fprojects%2Fcheckout/);
  });

  test("sends a refused request from anywhere back to sign-in", async ({ page }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();

    // The menu opens while the session is still good, so the sign-out is
    // the request the ended session refuses.
    await personalMenu(page).click();
    await expect(page.getByRole("menu")).toBeVisible();
    advanceClock("25h");
    await page.getByRole("menuitem", { name: "Sign out" }).click();

    await expect(signInHeading(page)).toBeVisible();
    await expect(page.getByRole("status")).toHaveText(
      "Your session has ended. Sign in again to continue.",
    );
  });
});

test.describe("a request from another origin", () => {
  test("is refused outright, and the console shows the refusal without trying again", async ({
    page,
  }) => {
    await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();

    // What a page on another site sends with the member's cookie. A browser
    // will not let a page forge these headers, so the request is made
    // beside the page, sharing its cookies.
    const refused = await page.request.post("/api/v1/auth/sign-out", {
      headers: { Origin: "https://elsewhere.example", "Sec-Fetch-Site": "cross-site" },
    });
    expect(refused.status()).toBe(403);
    const problem = (await refused.json()) as { code: string };
    expect(problem.code).toBe("cross_origin");

    // The console, given that refusal, explains it once and sends nothing more.
    const sent: string[] = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.startsWith("/api/v1/")) sent.push(`${request.method()} ${url.pathname}`);
    });
    await page.route("**/api/v1/auth/sign-out", (route) =>
      route.fulfill({
        status: 403,
        contentType: "application/problem+json",
        body: JSON.stringify(problem),
      }),
    );
    await personalMenu(page).click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();

    await expect(
      page.getByText("The request came from another site and was refused."),
    ).toBeVisible();
    await expect(page.getByText(/^Request ID: /)).toBeVisible();
    expect(sent).toEqual(["POST /api/v1/auth/sign-out"]);

    // The refused request ended nothing.
    await page.unroute("**/api/v1/auth/sign-out");
    await page.reload();
    await expect(shellHeading(page)).toBeVisible();
  });
});

test.describe("the content security policy", () => {
  test("keeps the page on its own origin without breaking it", async ({ page }) => {
    const complaints: string[] = [];
    page.on("console", (message) => {
      if (message.type() === "error") complaints.push(message.text());
    });

    const response = await page.goto("/sign-in");
    await signIn(page);
    await expect(shellHeading(page)).toBeVisible();
    await personalMenu(page).click();
    await expect(page.getByRole("menu")).toBeVisible();

    const policy = response?.headers()["content-security-policy"] ?? "";
    expect(policy).toContain("default-src 'self'");
    expect(policy).toContain("frame-ancestors 'none'");
    expect(policy).not.toContain("unsafe-eval");
    expect(complaints.filter((text) => /Content Security Policy/i.test(text))).toEqual([]);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`sign-in has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      await page.goto("/sign-in");
      await expect(signInHeading(page)).toBeVisible();

      await expectNoSeriousViolations(page, `sign-in (${theme})`);
    });

    test(`the shell has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      await page.goto("/sign-in");
      await signIn(page);
      await expect(shellHeading(page)).toBeVisible();

      await expectNoSeriousViolations(page, `the shell (${theme})`);
    });
  }
});
