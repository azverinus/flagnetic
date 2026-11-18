import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { seedProject, signIn } from "../support/console";
import { admin } from "../support/instance";
import { seedFlag, stringFlag } from "../support/flags";

/**
 * Every screen renders through the page and its cards: content starts at
 * the same left edge on a list and on the object it opens, and a list
 * fills the width instead of sitting in a centred column.
 */

const russian = {
  email: "Электронная почта",
  password: "Пароль",
  submit: "Войти",
  next: "Продолжить",
};

const left = (page: Page) =>
  page
    .locator("main h1")
    .first()
    .evaluate((heading) => heading.getBoundingClientRect().left);

test.describe("the page and its cards", () => {
  let project: string;
  test.beforeAll(async () => {
    project = await seedProject("layout");
    await seedFlag(project, stringFlag("checkout-copy"));
  });

  test("a list fills the width and a flag starts at the same left edge", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await signIn(page);

    await page.goto(`/projects/${project}/flags`);
    const table = page.getByRole("table", { name: "Flags" });
    await expect(table).toBeVisible();
    expect((await table.boundingBox())?.width ?? 0).toBeGreaterThanOrEqual(900);
    const listLeft = await left(page);

    await page.goto(`/projects/${project}/flags/checkout-copy`);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    expect(await left(page)).toBe(listLeft);
  });

  test("an empty list shows its create action in the empty state only", async ({ page }) => {
    const empty = await seedProject("layout-empty");
    await signIn(page);
    await page.goto(`/projects/${empty}/flags`);

    await expect(page.getByText("This project has no flag yet.")).toBeVisible();
    await expect(page.getByRole("link", { name: "New flag" })).toHaveCount(1);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`has no serious accessibility violation in ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await useTheme(page, theme);
      await signIn(page);

      for (const path of ["flags", "flags/checkout-copy", "settings"]) {
        await page.goto(`/projects/${project}/${path}`);
        await expect(page.locator("main h1").first()).toBeVisible();
        await expectNoSeriousViolations(page, `${path} in ${theme}`);
      }
    });
  }

  test.describe("in Russian", () => {
    test.use({ locale: "ru-RU" });

    test("renders a list and its card", async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await signIn(page, admin, russian);
      await page.goto(`/projects/${project}/flags`);

      await expect(page.locator("html")).toHaveAttribute("lang", "ru");
      await expect(page.getByRole("heading", { level: 1, name: "Флаги" })).toBeVisible();
      await expect(page.getByRole("table")).toBeVisible();
      await expectNoSeriousViolations(page, "the flags list in Russian");
    });
  });
});
