import { type AuthSettings, getAuthSettings, updateAuthSettings } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, pathOf, seedInvitation, signIn } from "../support/console";

/** The number of a duration field and the unit chosen beside it. */
function duration(page: Page, label: string) {
  return {
    amount: page.getByLabel(label, { exact: true }),
    unit: page.getByRole("combobox", { name: `Unit of the ${label.toLowerCase()}` }),
  };
}

/**
 * How the form shows a number of seconds: in the largest unit that holds
 * it whole, the unit agreeing with the number.
 */
function shown(seconds: number): { amount: string; unit: string } {
  for (const [unit, size] of [
    ["day", 86_400],
    ["hour", 3_600],
    ["minute", 60],
  ] as const) {
    if (seconds % size === 0) {
      const amount = seconds / size;
      return { amount: String(amount), unit: amount === 1 ? unit : `${unit}s` };
    }
  }
  return { amount: String(seconds), unit: seconds === 1 ? "second" : "seconds" };
}

async function openSettings(page: Page) {
  await page.goto("/admin/auth-settings");
  await expect(page.getByRole("heading", { name: "Auth settings", level: 1 })).toBeVisible();
  await expect(page.getByLabel("Password minimum length")).not.toHaveValue("");
}

test.describe("instance auth settings", () => {
  // The settings are the instance's: every spec after this one meets them,
  // so they are put back as they were.
  let before: AuthSettings;
  test.beforeAll(async () => {
    before = must(
      "reading the auth settings",
      await getAuthSettings({ client: managementClient() }),
    );
  });
  test.afterAll(async () => {
    must(
      "putting the auth settings back",
      await updateAuthSettings({ client: managementClient(), body: before }),
    );
  });

  test("shows the settings in force, each duration in the unit that holds it whole", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/admin/members");
    await page
      .getByRole("navigation", { name: "Instance" })
      .getByRole("link", { name: "Auth settings" })
      .click();
    await expect(page.getByRole("heading", { name: "Auth settings", level: 1 })).toBeVisible();

    for (const [label, seconds] of [
      ["Session idle timeout", before.session_idle_timeout_seconds],
      ["Session absolute timeout", before.session_absolute_timeout_seconds],
      ["Maximum token lifetime", before.token_max_lifetime_seconds],
    ] as const) {
      const field = duration(page, label);
      await expect(field.amount).toHaveValue(shown(seconds).amount);
      await expect(field.unit).toHaveText(shown(seconds).unit);
    }
    const expiry = page.getByRole("switch", { name: "Token expiry required" });
    await expect(expiry).toBeChecked({ checked: before.token_expiry_required });
    await expect(page.getByLabel("Password minimum length")).toHaveValue(
      String(before.password_min_length),
    );
  });

  test("refuses what breaks a rule under its field, saving nothing", async ({ page }) => {
    await signIn(page);
    await openSettings(page);

    await duration(page, "Session idle timeout").amount.fill("31");
    await duration(page, "Maximum token lifetime").amount.fill("0");
    await page.getByLabel("Password minimum length").fill("11");
    await page.getByRole("button", { name: "Save", exact: true }).first().click();

    await expect(
      page.getByText("The idle timeout cannot be longer than the absolute timeout."),
    ).toBeVisible();
    await expect(page.getByText("Enter a whole number.")).toBeVisible();
    await expect(page.getByText("Enter a value from 12 to 128.")).toBeVisible();
    await expect(page.getByLabel("Password minimum length")).toHaveAttribute(
      "aria-invalid",
      "true",
    );

    await duration(page, "Maximum token lifetime").amount.fill("3651");
    await expect(page.getByText("Enter a duration from 1 minute to 3,650 days.")).toBeVisible();

    await page
      .getByRole("region", { name: "Unsaved changes" })
      .getByRole("button", {
        name: "Discard",
      })
      .click();
    await expect(page.getByLabel("Password minimum length")).toHaveValue("12");
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toHaveCount(0);
  });

  test("asks before leaving unsaved changes behind", async ({ page }) => {
    await signIn(page);
    await openSettings(page);

    await page.getByLabel("Password minimum length").fill("14");
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
    await page
      .getByRole("navigation", { name: "Instance" })
      .getByRole("link", { name: "Members" })
      .click();
    const leave = page.getByRole("dialog", { name: "Leave without saving?" });
    await expect(leave).toBeVisible();
    await leave.getByRole("button", { name: "Cancel" }).click();
    await expect(page).toHaveURL(/\/admin\/auth-settings$/);
    await expect(page.getByLabel("Password minimum length")).toHaveValue("14");

    await page
      .getByRole("navigation", { name: "Instance" })
      .getByRole("link", { name: "Members" })
      .click();
    await page
      .getByRole("dialog", { name: "Leave without saving?" })
      .getByRole("button", { name: "Leave" })
      .click();
    await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
  });

  test("saves a longer password minimum, which accepting an invitation then applies", async ({
    page,
    browser,
  }) => {
    await signIn(page);
    await openSettings(page);

    await page.getByLabel("Password minimum length").fill("16");
    const idle = duration(page, "Session idle timeout");
    await idle.amount.fill("12");
    await idle.unit.click();
    await page.getByRole("option", { name: "hours" }).click();
    await page.getByRole("region", { name: "Unsaved changes" }).waitFor();
    await page.getByRole("button", { name: "Save", exact: true }).first().click();
    await expect(page.getByText("The auth settings are saved.")).toBeVisible();
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toHaveCount(0);

    await page.reload();
    await expect(page.getByLabel("Password minimum length")).toHaveValue("16");
    await expect(idle.amount).toHaveValue("12");
    await expect(idle.unit).toHaveText("hours");
    const saved = must(
      "reading the auth settings",
      await getAuthSettings({ client: managementClient() }),
    );
    expect(saved.session_idle_timeout_seconds).toBe(12 * 60 * 60);
    expect(saved.password_min_length).toBe(16);

    const invited = await seedInvitation("lise", "Lise Meitner");
    const context = await browser.newContext();
    const invitee = await context.newPage();
    await invitee.goto(pathOf(invited.link));
    await invitee.getByLabel("Password", { exact: true }).fill("violet-harbour1");
    await invitee.getByLabel("Confirm the password").fill("violet-harbour1");
    await invitee.getByRole("button", { name: "Accept invitation" }).click();
    await expect(invitee.getByText("Use 16 to 128 characters.")).toBeVisible();
    await context.close();
  });

  for (const theme of ["light", "dark"] as const) {
    test(`the auth settings have no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      await signIn(page);
      await openSettings(page);
      await expectNoSeriousViolations(page, `the auth settings (${theme})`);
      await page.getByLabel("Password minimum length").fill("200");
      await page.getByRole("button", { name: "Save", exact: true }).first().click();
      await expect(page.getByText("Enter a value from 12 to 128.")).toBeVisible();
      await expectNoSeriousViolations(page, `the auth settings with errors (${theme})`);
    });
  }
});
