import {
  type AuthSettings,
  createAuthProvider,
  disableAuthProvider,
  enableAuthProvider,
  getAuthProvider,
  getAuthSettings,
  inviteMember,
  updateAuthProvider,
  updateAuthSettings,
} from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, signIn, type SignInWords } from "../support/console";
import { dexSlug, dexUsers, enforcedDomain, readDex, signInAtDex } from "../support/dex";
import { admin, readState } from "../support/instance";

/** The domain the harness's own members have addresses in; Dex vouches for it too. */
const memberDomain = "flagnetic.test";

const personalMenu = (page: Page, name: string) => page.getByRole("button", { name });
const domainRow = (page: Page, number: number) => ({
  domain: page.getByRole("combobox", { name: `Domain ${number}` }),
  provider: page.getByRole("combobox", { name: `Provider ${number}` }),
});

/**
 * Makes Dex a provider that vouches for `enforcedDomain` and enabled, however
 * an earlier spec left it: that spec creates and later disables it, this one
 * may run alone.
 */
async function prepareDex(): Promise<void> {
  const client = managementClient();
  const dex = readDex();
  const domains = [memberDomain, enforcedDomain];
  const existing = await getAuthProvider({ client, path: { provider: dexSlug } });
  if (existing.data) {
    must(
      "updating Dex",
      await updateAuthProvider({
        client,
        path: { provider: dexSlug },
        body: { display_name: "Dex", domains, link_by_email: true },
        headers: { "If-Match": `"${existing.data.version}"` },
      }),
    );
    if (!existing.data.enabled) {
      must("enabling Dex", await enableAuthProvider({ client, path: { provider: dexSlug } }));
    }
  } else {
    must(
      "adding Dex",
      await createAuthProvider({
        client,
        body: {
          slug: dexSlug,
          kind: "oidc",
          display_name: "Dex",
          enabled: true,
          domains,
          issuer: dex.issuer,
          client_id: dex.clientId,
          client_secret: dex.clientSecret,
          link_by_email: true,
        },
      }),
    );
  }
}

test.describe("SSO enforcement", () => {
  // The settings and the provider are the instance's: the specs share them in
  // order, and the last one puts them back as they were.
  test.describe.configure({ mode: "serial" });

  let before: AuthSettings;
  test.beforeAll(async () => {
    const client = managementClient();
    before = must("reading the auth settings", await getAuthSettings({ client }));
    await prepareDex();
    // Ines is a member Dex knows, in the domain this spec enforces. A retry
    // meets her already invited, which is what the spec needs.
    await inviteMember({
      client,
      body: { email: dexUsers.ines.email, display_name: dexUsers.ines.name },
    });
  });

  test.afterAll(async () => {
    const client = managementClient();
    // Enforcement first: a provider an enforced domain names cannot be disabled.
    must("putting the auth settings back", await updateAuthSettings({ client, body: before }));
    // Disabled, so the sign-in page of every spec after this one is as it was.
    await disableAuthProvider({ client, path: { provider: dexSlug } });
  });

  test("enforces a domain to a provider that vouches for it", async ({ page }) => {
    await signIn(page);
    await page.goto("/admin/auth-settings");
    await expect(page.getByRole("heading", { name: "Auth settings", level: 1 })).toBeVisible();
    await expect(page.getByText("No domain is enforced.")).toBeVisible();
    await expect(page.getByRole("switch", { name: "Local login" })).toBeChecked();

    await page.getByRole("button", { name: "Add enforced domain" }).click();
    // A row asks for both halves before anything is sent.
    await page.getByRole("button", { name: "Save", exact: true }).first().click();
    await expect(page.getByText("Choose a domain.")).toBeVisible();
    await expect(page.getByText("Choose a provider.")).toBeVisible();

    const row = domainRow(page, 1);
    await row.domain.click();
    // The domains offered are those the enabled providers vouch for.
    await expect(page.getByRole("option", { name: enforcedDomain, exact: true })).toBeVisible();
    await expect(page.getByRole("option", { name: memberDomain, exact: true })).toBeVisible();
    await page.getByRole("option", { name: enforcedDomain, exact: true }).click();
    await row.provider.click();
    await expect(page.getByRole("option")).toHaveCount(1);
    await page.getByRole("option", { name: "Dex" }).click();
    await page.getByRole("button", { name: "Save", exact: true }).first().click();

    await expect(page.getByText("The auth settings are saved.")).toBeVisible();
    await page.reload();
    await expect(row.domain).toHaveText(enforcedDomain);
    await expect(row.provider).toHaveText("Dex");
    const saved = must(
      "reading the auth settings",
      await getAuthSettings({ client: managementClient() }),
    );
    expect(saved.sso_enforced_domains).toEqual([{ domain: enforcedDomain, provider: dexSlug }]);
  });

  test("sends an email in the enforced domain through Dex, and any other asks for its password", async ({
    page,
  }) => {
    await page.goto("/sign-in");
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    // With providers the email comes first; no password is asked for yet.
    await expect(page.getByLabel("Password")).toHaveCount(0);
    await expectNoSeriousViolations(page, "the sign-in page asking for the email");

    await page.getByLabel("Email").fill(admin.email);
    await page.getByRole("button", { name: "Continue" }).click();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page.getByLabel("Email")).toHaveAttribute("readonly", "");
    await expectNoSeriousViolations(page, "the sign-in page asking for the password");
    await page.getByRole("button", { name: "Use another email" }).click();
    await expect(page.getByLabel("Password")).toHaveCount(0);

    await page.getByLabel("Email").fill(dexUsers.ines.email);
    await page.getByRole("button", { name: "Continue" }).click();
    await page.waitForURL(`${readDex().issuer}/**`);
    await signInAtDex(page, dexUsers.ines);

    await expect(personalMenu(page, dexUsers.ines.name)).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`^${readState().baseURL}/$`));
  });

  test("refuses a password for the enforced domain, naming the provider", async ({ request }) => {
    const refused = await request.post(`${readState().baseURL}/api/v1/auth/sign-in`, {
      data: { email: dexUsers.ines.email, password: "any-password-at-all-1234" },
    });
    expect(refused.status()).toBe(403);
    expect(await refused.json()).toMatchObject({ code: "sso_required", provider: dexSlug });
  });

  test("refuses to disable local login without an identity at an enabled provider", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/admin/auth-settings");
    const local = page.getByRole("switch", { name: "Local login" });
    await expect(local).toBeChecked();
    // The help text names the way back in.
    await expect(local).toHaveAccessibleDescription(/flagnetic admin unlock/);

    await local.click();
    await page.getByRole("button", { name: "Save", exact: true }).first().click();

    await expect(page.getByRole("alert")).toContainText(
      "an identity of your own at an enabled auth provider",
    );
    await page.reload();
    await expect(page.getByRole("switch", { name: "Local login" })).toBeChecked();
    const saved = must(
      "reading the auth settings",
      await getAuthSettings({ client: managementClient() }),
    );
    expect(saved.local_login_enabled).toBe(true);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`the auth settings with an enforced domain have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto("/admin/auth-settings");
      await expect(domainRow(page, 1).domain).toHaveText(enforcedDomain);
      await expectNoSeriousViolations(page, `the auth settings with an enforced domain (${theme})`);
      await page.getByRole("button", { name: "Add enforced domain" }).click();
      await page.getByRole("button", { name: "Save", exact: true }).first().click();
      await expect(page.getByText("Choose a domain.")).toBeVisible();
      await expectNoSeriousViolations(page, `the enforced domains with errors (${theme})`);
    });
  }

  test.describe("in Russian", () => {
    test.use({ locale: "ru-RU" });
    const russian: SignInWords = {
      email: "Электронная почта",
      password: "Пароль",
      submit: "Войти",
      next: "Продолжить",
    };

    test("reads the enforced domains and the email-first sign-in in Russian", async ({
      page,
      browser,
    }) => {
      await signIn(page, admin, russian);
      await page.goto("/admin/auth-settings");
      await expect(
        page.getByRole("heading", { name: "Настройки аутентификации", level: 1 }),
      ).toBeVisible();
      await expect(page.getByRole("switch", { name: "Локальный вход" })).toBeChecked();
      await expect(page.getByText("Домены с обязательным входом")).toBeVisible();
      await expect(page.getByRole("combobox", { name: "Домен 1" })).toHaveText(enforcedDomain);
      await page.getByRole("button", { name: "Добавить домен с обязательным входом" }).click();
      await page.getByRole("button", { name: "Сохранить", exact: true }).first().click();
      await expect(page.getByText("Выберите домен.")).toBeVisible();
      await expect(page.getByText("Выберите провайдера.")).toBeVisible();
      await expectNoSeriousViolations(page, "the enforced domains in Russian");

      const context = await browser.newContext({ locale: "ru-RU" });
      const visitor = await context.newPage();
      await visitor.goto("/sign-in");
      await expect(visitor.getByLabel(russian.email)).toBeVisible();
      await expect(visitor.getByLabel(russian.password)).toHaveCount(0);
      await expect(visitor.getByRole("link", { name: "Войти через Dex" })).toBeVisible();
      await visitor.getByLabel(russian.email).fill(admin.email);
      await visitor.getByRole("button", { name: russian.next }).click();
      await expect(visitor.getByLabel(russian.password)).toBeVisible();
      await expectNoSeriousViolations(visitor, "the sign-in page in Russian");
      await context.close();
    });
  });
});
