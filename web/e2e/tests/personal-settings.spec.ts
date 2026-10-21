import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, seedMember, signIn } from "../support/console";

const password = "harness-meadow-copper-5512";
const changed = "harness-meadow-copper-5512-changed";

/** The table of the devices the member is signed in on, and its rows. */
const sessionRows = (page: Page) =>
  page
    .getByRole("table", { name: "The devices you are signed in on" })
    .getByRole("row")
    .filter({ has: page.getByRole("cell") });

/** The personal menu's button, named by the member's name. */
const personalMenu = (page: Page, name: string) => page.getByRole("button", { name, exact: true });

async function openSessions(page: Page) {
  await page.goto("/settings/sessions");
  await expect(page.getByRole("heading", { name: "Personal settings", level: 1 })).toBeVisible();
}

test.describe("personal settings", () => {
  // The specs of one member run in order: each starts from what the last left.
  test.describe.configure({ mode: "serial" });

  let grace: Credentials & { name: string };
  test.beforeAll(async () => {
    grace = await seedMember("grace", "Grace Hopper", password);
  });

  test("lists both devices, revokes the other, and sends that device to sign-in", async ({
    page,
    browser,
  }) => {
    await signIn(page, grace);
    await personalMenu(page, grace.name).click();
    await page.getByRole("menuitem", { name: "Personal settings" }).click();
    await expect(page).toHaveURL(/\/settings\/profile$/);
    await expect(page.getByRole("heading", { name: "Profile", level: 2 })).toBeVisible();
    await page
      .getByRole("navigation", { name: "Personal settings" })
      .getByRole("link", { name: "Sessions" })
      .click();
    await expect(sessionRows(page)).toHaveCount(1);
    await expect(page.getByText("You are signed in on this device only.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Revoke all other sessions" })).toBeDisabled();

    const otherContext = await browser.newContext();
    const other = await otherContext.newPage();
    await signIn(other, grace);
    await other.goto("/settings/profile");
    await expect(other.getByRole("heading", { name: "Profile", level: 2 })).toBeVisible();

    await page.reload();
    await expect(sessionRows(page)).toHaveCount(2);
    await expect(sessionRows(page).first()).toContainText("This device");
    await expect(sessionRows(page).first()).toContainText("Chrome");
    await expect(sessionRows(page).nth(1)).not.toContainText("This device");
    await expect(sessionRows(page).first().getByRole("button")).toHaveCount(0);

    await sessionRows(page)
      .nth(1)
      .getByRole("button", { name: /^Revoke the session on / })
      .click();
    const dialog = page.getByRole("dialog", { name: /^Revoke the session on / });
    await dialog.getByRole("button", { name: "Revoke session" }).click();
    await expect(dialog).toBeHidden();
    await expect(sessionRows(page)).toHaveCount(1);
    await expect(page.getByText("You are signed in on this device only.")).toBeVisible();

    await other.reload();
    await expect(other).toHaveURL(/\/sign-in\?/);
    await expect(other.getByRole("status")).toHaveText(
      "Your session has ended. Sign in again to continue.",
    );
    expect(new URL(other.url()).searchParams.get("returnTo")).toBe("/settings/profile");
    await other.getByLabel("Email").fill(grace.email);
    await other.getByLabel("Password").fill(grace.password);
    await other.getByRole("button", { name: "Sign in" }).click();
    await expect(other).toHaveURL(/\/settings\/profile$/);
    await otherContext.close();
  });

  test("revokes every other device at once", async ({ page, browser }) => {
    const others = await Promise.all([browser.newContext(), browser.newContext()]);
    for (const context of others) await signIn(await context.newPage(), grace);
    await signIn(page, grace);
    await openSessions(page);
    // The devices of the last spec are still signed in beside these three.
    await expect(sessionRows(page)).toHaveCount(5);

    await page.getByRole("button", { name: "Revoke all other sessions" }).click();
    const dialog = page.getByRole("dialog", { name: "Revoke all other sessions" });
    await dialog.getByRole("button", { name: "Revoke 4 sessions" }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText("Revoked 4 sessions.")).toBeVisible();
    await expect(sessionRows(page)).toHaveCount(1);
    for (const context of others) await context.close();
  });

  test("renames the member, and the personal menu says the new name", async ({ page }) => {
    await signIn(page, grace);
    await page.goto("/settings/profile");
    await expect(page.getByLabel("Email")).toHaveAttribute("readonly", "");
    await expect(page.getByLabel("Email")).toHaveValue(grace.email);

    await page.getByLabel("Display name").fill("Grace Brewster Hopper");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Your profile is saved.")).toBeVisible();
    await expect(personalMenu(page, "Grace Brewster Hopper")).toBeVisible();
    await page.reload();
    await expect(personalMenu(page, "Grace Brewster Hopper")).toBeVisible();
    await expect(page.getByLabel("Display name")).toHaveValue("Grace Brewster Hopper");
    grace.name = "Grace Brewster Hopper";
  });

  test("reads the console in the language the member chose, on every device, and back", async ({
    page,
    browser,
  }) => {
    await signIn(page, grace);
    await page.goto("/settings/profile");
    await page.getByRole("combobox", { name: "Language" }).click();
    await page.getByRole("option", { name: "Русский" }).click();
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("heading", { name: "Личные настройки", level: 1 })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "ru");
    await expect(page.getByText("Профиль сохранён.")).toBeVisible();

    // The choice is the member's: another browser reads it after signing in.
    const elsewhere = await browser.newContext({ locale: "en-US" });
    const other = await elsewhere.newPage();
    await signIn(other, grace);
    await other.goto("/settings/profile");
    await expect(other.getByRole("heading", { name: "Личные настройки", level: 1 })).toBeVisible();
    await elsewhere.close();

    // The personal menu chooses too: following the browser reads English here.
    await personalMenu(page, grace.name).click();
    await page.getByRole("menuitemradio", { name: "Как в браузере" }).click();
    await expect(page.getByRole("heading", { name: "Personal settings", level: 1 })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await page.reload();
    await expect(page.getByRole("combobox", { name: "Language" })).toHaveText("Follow the browser");
  });

  test("switches to dark on the profile page and keeps it after a reload", async ({ page }) => {
    await signIn(page, grace);
    await page.goto("/settings/profile");
    await page.getByRole("radio", { name: "Dark" }).check();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.reload();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await expect(page.getByRole("radio", { name: "Dark" })).toBeChecked();
    await personalMenu(page, grace.name).click();
    await expect(page.getByRole("menuitemradio", { name: "Dark" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await page.keyboard.press("Escape");
    await page.getByRole("radio", { name: "System" }).check();
  });

  test("refuses a weak or a wrong password, then changes it and signs in with the new one", async ({
    page,
    browser,
  }) => {
    const otherContext = await browser.newContext();
    const other = await otherContext.newPage();
    await signIn(other, grace);
    await signIn(page, grace);
    await page.goto("/settings/profile");

    const current = page.getByLabel("Current password");
    const next = page.getByLabel("New password", { exact: true });
    const confirm = page.getByLabel("Confirm the new password");
    await current.fill(grace.password);
    await next.fill("short");
    await confirm.fill("short");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(next).toHaveAttribute("aria-invalid", "true");
    await expect(page.getByText("Use 12 to 128 characters.")).toBeVisible();

    await current.fill("not-it-at-all-99");
    await next.fill(changed);
    await confirm.fill(changed);
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByText("That is not your current password.")).toBeVisible();
    await expect(current).toHaveAttribute("aria-invalid", "true");

    await current.fill(grace.password);
    await next.fill(changed);
    await confirm.fill(changed);
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(
      page.getByText("Your password is changed. Every other device is signed out."),
    ).toBeVisible();
    await expect(current).toHaveValue("");

    // This device stays; the other is signed out.
    await page.reload();
    await expect(page.getByRole("heading", { name: "Profile", level: 2 })).toBeVisible();
    await other.reload();
    await expect(other).toHaveURL(/\/sign-in\?/);
    await otherContext.close();

    await personalMenu(page, grace.name).click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    await expect(page.getByRole("heading", { name: "Sign in", level: 1 })).toBeVisible();
    await page.getByLabel("Email").fill(grace.email);
    await page.getByLabel("Password").fill(grace.password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("alert")).toHaveText("The email or the password is not right.");
    grace.password = changed;
    await signIn(page, grace);
    await expect(page).not.toHaveURL(/\/sign-in/);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`has no serious accessibility violations on the profile and the sessions (${theme})`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page, grace);
      await page.goto("/settings/profile");
      await expect(page.getByRole("heading", { name: "Password", level: 2 })).toBeVisible();
      await expectNoSeriousViolations(page, `the profile (${theme})`);
      await openSessions(page);
      await expect(sessionRows(page).first()).toContainText("This device");
      await expectNoSeriousViolations(page, `the sessions (${theme})`);
    });
  }
});
