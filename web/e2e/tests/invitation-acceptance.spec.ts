import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { pathOf, seedInvitation } from "../support/console";
import { advanceClock } from "../support/instance";

const password = "harness-saffron-glacier-7713";

/** The masked email the invitation shows: its first character, then the domain. */
function masked(email: string): string {
  const [local = "", domain = ""] = email.split("@");
  return `${local.charAt(0)}***@${domain}`;
}

async function choose(page: Page, value: string, confirm = value) {
  await page.getByLabel("Password", { exact: true }).fill(value);
  await page.getByLabel("Confirm the password").fill(confirm);
  await page.getByRole("button", { name: "Accept invitation" }).click();
}

test.describe("accepting an invitation", () => {
  test("says whose invitation it is, signs the new member in, and is a dead end afterwards", async ({
    page,
    browser,
  }) => {
    const invited = await seedInvitation("ivy", "Ivy Hollins");
    await page.goto(pathOf(invited.link));
    await expect(page.getByRole("heading", { name: "Accept your invitation" })).toBeVisible();
    await expect(page.getByText(`as Ivy Hollins (${masked(invited.email)})`)).toBeVisible();
    await expect(page.locator("body")).not.toContainText(invited.email);

    await choose(page, password);
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("button", { name: "Ivy Hollins", exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: "Ivy Hollins", exact: true })).toBeVisible();

    const elsewhere = await browser.newContext();
    const other = await elsewhere.newPage();
    await other.goto(pathOf(invited.link));
    await expect(
      other.getByRole("heading", { name: "This invitation no longer works" }),
    ).toBeVisible();
    await expect(other.getByLabel("Password", { exact: true })).toHaveCount(0);
    await other.getByRole("link", { name: "Sign in" }).click();
    await other.getByLabel("Email").fill(invited.email);
    await other.getByLabel("Password").fill(password);
    await other.getByRole("button", { name: "Sign in" }).click();
    await expect(other.getByRole("button", { name: "Ivy Hollins", exact: true })).toBeVisible();
    await elsewhere.close();
  });

  test("says each refusal of the password policy under the password, then accepts", async ({
    page,
  }) => {
    const invited = await seedInvitation("kay", "Kay McNulty");
    await page.goto(pathOf(invited.link));

    await choose(page, "too-short");
    await expect(page.getByText("Use 12 to 128 characters.")).toBeVisible();
    await expect(page.getByLabel("Password", { exact: true })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await choose(page, "123123123123");
    await expect(page.getByText("This password is too common. Choose another.")).toBeVisible();
    const local = invited.email.split("@")[0] ?? "";
    await choose(page, `${local}-and-more`);
    await expect(page.getByText("The password must not contain your email address.")).toBeVisible();
    await choose(page, password, `${password}-not-the-same`);
    await expect(page.getByText("The passwords do not match.")).toBeVisible();

    await choose(page, password);
    await expect(page.getByRole("button", { name: "Kay McNulty", exact: true })).toBeVisible();
  });

  test("explains a link that never opened anything", async ({ page }) => {
    await page.goto("/invitations/not-a-secret-anyone-issued");
    await expect(
      page.getByRole("heading", { name: "This invitation no longer works" }),
    ).toBeVisible();
  });

  test("explains a link that expired after seven days", async ({ page }) => {
    const invited = await seedInvitation("betty", "Betty Holberton");
    try {
      advanceClock("169h");
      await page.goto(pathOf(invited.link));
      await expect(
        page.getByRole("heading", { name: "This invitation no longer works" }),
      ).toBeVisible();
    } finally {
      advanceClock("0s");
    }
  });

  test("reads in Russian for a Russian browser", async ({ browser }) => {
    const invited = await seedInvitation("marlyn", "Marlyn Meltzer");
    const context = await browser.newContext({ locale: "ru-RU" });
    const page = await context.newPage();
    await page.goto(pathOf(invited.link));
    await expect(page.getByRole("heading", { name: "Примите приглашение" })).toBeVisible();
    await page.getByLabel("Пароль", { exact: true }).fill("коротко");
    await page.getByLabel("Повторите пароль").fill("коротко");
    await page.getByRole("button", { name: "Принять приглашение" }).click();
    await expect(page.getByText("Используйте от 12 до 128 символов.")).toBeVisible();
    await page.goto("/invitations/not-a-secret-anyone-issued");
    await expect(
      page.getByRole("heading", { name: "Это приглашение больше не действует" }),
    ).toBeVisible();
    await context.close();
  });

  for (const theme of ["light", "dark"] as const) {
    test(`the invitation pages have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      const invited = await seedInvitation("ada-k", "Adele Goldstine");
      await page.goto(pathOf(invited.link));
      await expect(page.getByRole("heading", { name: "Accept your invitation" })).toBeVisible();
      await expectNoSeriousViolations(page, `the invitation (${theme})`);
      await choose(page, "too-short");
      await expect(page.getByText("Use 12 to 128 characters.")).toBeVisible();
      await expectNoSeriousViolations(page, `the invitation with a refusal (${theme})`);
      await page.goto("/invitations/not-a-secret-anyone-issued");
      await expect(
        page.getByRole("heading", { name: "This invitation no longer works" }),
      ).toBeVisible();
      await expectNoSeriousViolations(page, `a gone invitation (${theme})`);
    });
  }
});
