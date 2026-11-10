import { createAuthProvider, disableAuthProvider } from "@flagnetic/api";
import { type Browser, expect, type Locator, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedProject, signIn, type SignInWords } from "../support/console";
import { dexGroup, dexGroupsSlug, dexUsers, readDex, signInAtDex } from "../support/dex";
import { admin } from "../support/instance";

const providerName = "Dex groups";
const heading = (page: Page, name: string) => page.getByRole("heading", { level: 1, name });
const mappings = (page: Page) =>
  page.getByRole("region", { name: /Group mappings|Соответствия групп/ });
const memberRow = (page: Page, name: string) =>
  page
    .getByRole("table", { name: /Members of the project|Участники проекта/ })
    .getByRole("row", { name: new RegExp(name) });

/** Picks an option of one of the console's selects, inside one part of the page. */
async function choose(page: Page, scope: Locator, field: string, option: string) {
  await scope.getByRole("combobox", { name: field }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

/** Signs Vera in through Dex in a browser of her own, and leaves it signed in. */
async function signInAsVera(browser: Browser): Promise<void> {
  const context = await browser.newContext();
  try {
    const page = await context.newPage();
    await page.goto("/sign-in");
    await page.getByRole("link", { name: `Sign in with ${providerName}` }).click();
    await signInAtDex(page, dexUsers.vera);
    await expect(page).not.toHaveURL(/\/sign-in/);
    await expect(page.getByRole("button", { name: dexUsers.vera.name })).toBeVisible();
  } finally {
    await context.close();
  }
}

test.describe("group mappings and where grants come from", () => {
  // One provider, one project and one member's grants, changed step by step.
  test.describe.configure({ mode: "serial" });

  let project: string;
  let projectLabel: string;
  test.beforeAll(async () => {
    project = await seedProject("mappings");
    projectLabel = `Project mappings (${project})`;
    must(
      "adding the provider that asks Dex for groups",
      await createAuthProvider({
        client: managementClient(),
        body: {
          slug: dexGroupsSlug,
          kind: "oidc",
          display_name: providerName,
          enabled: true,
          domains: ["flagnetic.test"],
          link_by_email: true,
          issuer: readDex().issuer,
          client_id: readDex().clientId,
          client_secret: readDex().clientSecret,
          scopes: ["openid", "email", "profile", "groups"],
        },
      }),
    );
  });

  test("maps a Dex group to a role in a project", async ({ page }) => {
    await signIn(page);
    await page.goto(`/admin/auth-providers/${dexGroupsSlug}`);
    await expect(heading(page, providerName)).toBeVisible();
    await expect(page.getByText("This provider's groups grant nothing yet.")).toBeVisible();

    // A mapping with nothing filled in is refused before anything is sent.
    await page.getByRole("button", { name: "Add a mapping" }).click();
    const first = page.getByRole("group", { name: "Mapping 1" });
    await page.getByRole("button", { name: "Save mappings" }).click();
    await expect(first.getByText("Enter the directory group.")).toBeVisible();
    await expect(first.getByText("Choose a project.")).toBeVisible();
    await expect(first.getByText("Choose a role.")).toBeVisible();

    await first.getByLabel("Directory group").fill(dexGroup);
    await choose(page, first, "Project", projectLabel);
    await choose(page, first, "Role", "Developer");
    await page.getByRole("button", { name: "Save mappings" }).click();

    // Nobody has signed in through the provider yet, so the run did nothing.
    await expect(mappings(page).getByRole("status")).toHaveText("Granted 0, removed 0.");
    await expect(page.getByRole("button", { name: "Save mappings" })).toBeDisabled();

    // The mappings are the server's: a reload shows them as saved.
    await page.reload();
    await expect(page.getByLabel("Directory group")).toHaveValue(dexGroup);
    await expect(
      page.getByRole("group", { name: "Mapping 1" }).getByRole("combobox", { name: "Project" }),
    ).toHaveText(projectLabel);
  });

  test("grants the role to a member who signs in through Dex, naming the provider", async ({
    page,
    browser,
  }) => {
    await signInAsVera(browser);

    await signIn(page);
    await page.goto(`/projects/${project}/members`);
    const row = memberRow(page, dexUsers.vera.name);
    await expect(row).toContainText("Developer");
    await expect(row.getByText(`SSO: ${dexGroupsSlug}`)).toBeVisible();
    await expect(row).toContainText(`From the group mappings of ${dexGroupsSlug}`);
  });

  test("refuses removing the grant by hand, saying why, and grants another role beside it", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto(`/projects/${project}/members`);
    const row = memberRow(page, dexUsers.vera.name);

    const remove = row.getByRole("button", { name: `Remove Developer from ${dexUsers.vera.name}` });
    await expect(remove).toHaveAttribute("aria-disabled", "true");
    await expect(remove).toHaveAccessibleDescription(
      `The group mappings of ${dexGroupsSlug} made this grant, so only they remove it. An instance admin changes them on the provider's page.`,
    );
    // It does nothing when activated.
    await remove.click({ force: true });
    await expect(page.getByRole("dialog")).toHaveCount(0);

    await row.getByRole("button", { name: `Grant a role to ${dexUsers.vera.name}` }).click();
    const dialog = page.getByRole("dialog", { name: `Grant a role to ${dexUsers.vera.name}` });
    await choose(page, dialog, "Role", "Viewer");
    await dialog.getByRole("button", { name: "Grant role" }).click();
    await expect(dialog).toBeHidden();

    await expect(row).toContainText("Viewer");
    await expect(row).toContainText("Manual");
    await expect(row).toContainText("Developer");
    // The manual grant is removed by hand as any is; the other stays.
    await row.getByRole("button", { name: `Remove Viewer from ${dexUsers.vera.name}` }).click();
    await page
      .getByRole("dialog", { name: `Remove Viewer from ${dexUsers.vera.name}` })
      .getByRole("button", { name: "Remove grant" })
      .click();
    await expect(row).not.toContainText("Manual");
    await expect(row).toContainText(`SSO: ${dexGroupsSlug}`);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`the mappings and the grants they made have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto(`/admin/auth-providers/${dexGroupsSlug}`);
      await expect(page.getByLabel("Directory group")).toHaveValue(dexGroup);
      await expectNoSeriousViolations(page, `a provider's group mappings (${theme})`);

      await page.getByRole("button", { name: "Add a mapping" }).click();
      await page.getByRole("button", { name: "Save mappings" }).click();
      await expectNoSeriousViolations(page, `group mappings with errors (${theme})`);

      await page.goto(`/projects/${project}/members`);
      await expect(memberRow(page, dexUsers.vera.name)).toContainText(`SSO: ${dexGroupsSlug}`);
      await expectNoSeriousViolations(page, `grants with their source (${theme})`);
    });
  }

  test.describe("in Russian", () => {
    test.use({ locale: "ru-RU" });
    const russian: SignInWords = {
      email: "Электронная почта",
      password: "Пароль",
      submit: "Войти",
    };

    test("reads the mappings and the source of a grant", async ({ page }) => {
      await signIn(page, admin, russian);
      await page.goto(`/admin/auth-providers/${dexGroupsSlug}`);
      await expect(
        mappings(page).getByRole("heading", { name: "Соответствия групп" }),
      ).toBeVisible();
      await expect(page.getByLabel("Группа каталога")).toHaveValue(dexGroup);

      await page.getByRole("button", { name: "Добавить соответствие" }).click();
      await page.getByRole("button", { name: "Сохранить соответствия" }).click();
      await expect(page.getByText("Укажите группу каталога.")).toBeVisible();
      await expect(page.getByText("Выберите проект.")).toBeVisible();
      await expectNoSeriousViolations(page, "group mappings with errors in Russian");

      await page.goto(`/projects/${project}/members`);
      const row = memberRow(page, dexUsers.vera.name);
      await expect(row).toContainText(`SSO: ${dexGroupsSlug}`);
      await expect(row).toContainText(`Выдана по соответствиям групп провайдера ${dexGroupsSlug}`);
      await expect(
        row.getByRole("button", {
          name: `Отозвать роль «Разработчик» у участника ${dexUsers.vera.name}`,
        }),
      ).toHaveAttribute("aria-disabled", "true");
      await expectNoSeriousViolations(page, "the grants with their source in Russian");
    });
  });

  test("removing the mapping takes the grant away at once", async ({ page }) => {
    await signIn(page);
    await page.goto(`/admin/auth-providers/${dexGroupsSlug}`);
    await expect(page.getByLabel("Directory group")).toHaveValue(dexGroup);

    await page.getByRole("button", { name: "Remove mapping 1" }).click();
    await page.getByRole("button", { name: "Save mappings" }).click();
    await expect(mappings(page).getByRole("status")).toHaveText("Granted 0, removed 1.");

    await page.goto(`/projects/${project}/members`);
    await expect(page.getByText("Nobody holds a role in this project yet.")).toBeVisible();
  });

  test.afterAll(async () => {
    // Disabled, so the sign-in page of every spec after this one is as it was.
    await disableAuthProvider({ client: managementClient(), path: { provider: dexGroupsSlug } });
  });
});
