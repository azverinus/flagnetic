import { randomUUID } from "node:crypto";

import { createAuthProvider, disableAuthProvider } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { managementClient, must, signIn, type SignInWords } from "../support/console";
import { admin, readState } from "../support/instance";
import { scimRequest as scim } from "../support/scim";

const heading = (page: Page, name: string) => page.getByRole("heading", { level: 1, name });

/** The sign-in page as it reads in Russian. */
const russian: SignInWords = {
  email: "Электронная почта",
  password: "Пароль",
  submit: "Войти",
  next: "Продолжить",
};

const stamp = randomUUID().replaceAll("-", "").slice(0, 10);
const slug = `scim-${stamp}`;
const providerName = `Directory ${stamp}`;
const userEmail = `lena-${stamp}@scim.test`;
const userName = `Elena Directory ${stamp}`;
const groupName = `platform-${stamp}`;

test.describe("SCIM tokens and provisioned members", () => {
  // The provider and its token are the instance's: the specs share them in
  // order, and the last one leaves the provider disabled, since the
  // sign-in page of every spec after this one offers what is enabled.
  test.describe.configure({ mode: "serial" });

  let token = "";
  let memberId = "";

  test.beforeAll(async () => {
    must(
      "adding a provider",
      await createAuthProvider({
        client: managementClient(),
        body: {
          slug,
          kind: "oidc",
          display_name: providerName,
          enabled: true,
          domains: ["scim.test"],
          issuer: "https://login.scim.test",
          client_id: "flagnetic",
          client_secret: "unused",
        },
      }),
    );
  });

  test.afterAll(async () => {
    await disableAuthProvider({ client: managementClient(), path: { provider: slug } });
  });

  test("creates a token for a provider and shows its value once", async ({ page }) => {
    await signIn(page);
    await page.goto("/admin/scim-tokens");
    await expect(heading(page, "SCIM tokens")).toBeVisible();
    await expect(page.getByText(`${readState().baseURL}/scim/v2`, { exact: true })).toBeVisible();
    await expectNoSeriousViolations(page, "the SCIM tokens screen");

    await page.getByRole("button", { name: "New SCIM token" }).click();
    const dialog = page.getByRole("dialog", { name: "New SCIM token" });
    await dialog.getByRole("button", { name: "Create token" }).click();
    await expect(dialog.getByText("Choose an auth provider.")).toBeVisible();
    await dialog.getByRole("combobox", { name: "Auth provider" }).click();
    await page.getByRole("option", { name: providerName }).click();
    await expectNoSeriousViolations(page, "the new SCIM token dialog");
    await dialog.getByRole("button", { name: "Create token" }).click();

    const reveal = page.getByRole("dialog", { name: "Your new SCIM token" });
    token = (await reveal.locator("code").innerText()).trim();
    expect(token).toMatch(/^fn_scim_[0-9A-Za-z]{40}$/);
    await expectNoSeriousViolations(page, "the SCIM token's value");
    await expect(reveal.getByRole("button", { name: "Close" })).toBeDisabled();
    await reveal.getByLabel("I have stored it somewhere safe").check();
    await reveal.getByRole("button", { name: "Close" }).click();

    // The value is gone for good: the list knows the token by its hint.
    await expect(page.getByText(token)).toHaveCount(0);
    const row = page.getByRole("row", { name: new RegExp(token.slice(0, 12)) });
    await expect(row).toContainText(providerName);
    await expect(row).toContainText("Active");
    await expect(row).toContainText("Never");
  });

  test("shows the member and the group the directory provisions with it", async ({ page }) => {
    const created = await scim(token, "POST", "/Users", {
      schemas: ["urn:ietf:params:scim:schemas:core:2.0:User"],
      userName: userEmail,
      externalId: `ext-${stamp}`,
      displayName: userName,
      active: true,
    });
    expect(created.status).toBe(201);
    memberId = created.body?.id ?? "";
    const group = await scim(token, "POST", "/Groups", {
      schemas: ["urn:ietf:params:scim:schemas:core:2.0:Group"],
      displayName: groupName,
      members: [{ value: memberId }],
    });
    expect(group.status).toBe(201);

    await signIn(page);
    await page.goto(`/admin/members?q=${encodeURIComponent(userEmail)}`);
    const row = page.getByRole("row", { name: new RegExp(userName) });
    await expect(row).toContainText(`Provisioned by ${slug}`);
    await expectNoSeriousViolations(page, "the members, one of them provisioned");

    await row.getByRole("link", { name: userName }).click();
    await expect(heading(page, userName)).toBeVisible();
    await expect(page.getByRole("note")).toContainText(`Provisioned by ${slug}`);
    for (const name of ["Edit", "Deactivate"]) {
      await expect(page.getByRole("button", { name })).toBeDisabled();
      await expect(page.getByRole("button", { name })).toHaveAccessibleDescription(
        /its directory decides their email, name and whether they are active/,
      );
    }
    await expect(page.getByRole("button", { name: "Make instance admin" })).toBeEnabled();
    await expectNoSeriousViolations(page, "a provisioned member's page");

    await page.goto(`/admin/auth-providers/${slug}`);
    const groups = page.getByRole("region", { name: "SCIM groups" });
    const groupRow = groups.getByRole("row", { name: new RegExp(groupName) });
    await expect(groupRow).toContainText("1");
    await expect(groups.getByRole("button")).toHaveCount(0);
    await expectNoSeriousViolations(page, "a provider's SCIM groups");

    // The token's use is recorded.
    await page.goto("/admin/scim-tokens");
    const tokenRow = page.getByRole("row", { name: new RegExp(token.slice(0, 12)) });
    await expect(tokenRow.getByRole("cell").nth(3)).not.toHaveText("Never");
  });

  test.describe("in Russian", () => {
    test.use({ locale: "ru-RU" });

    test("reads the tokens, the provisioned member and the groups in Russian", async ({ page }) => {
      await signIn(page, admin, russian);
      await page.goto("/admin/scim-tokens");
      await expect(heading(page, "Токены SCIM")).toBeVisible();
      await expect(page.getByRole("row", { name: new RegExp(token.slice(0, 12)) })).toContainText(
        "Действует",
      );
      await expectNoSeriousViolations(page, "the SCIM tokens in Russian");

      await page.getByRole("button", { name: "Новый токен SCIM" }).click();
      const dialog = page.getByRole("dialog", { name: "Новый токен SCIM" });
      await dialog.getByRole("button", { name: "Создать токен" }).click();
      await expect(dialog.getByText("Выберите провайдера входа.")).toBeVisible();
      await expectNoSeriousViolations(page, "the new SCIM token dialog in Russian");
      await dialog.getByRole("button", { name: "Отмена" }).click();

      await page.goto(`/admin/members/${memberId}`);
      await expect(heading(page, userName)).toBeVisible();
      await expect(page.getByRole("note")).toContainText(`Заведён провайдером ${slug}`);
      await expect(page.getByRole("button", { name: "Деактивировать" })).toBeDisabled();
      await expectNoSeriousViolations(page, "a provisioned member in Russian");

      await page.goto(`/admin/auth-providers/${slug}`);
      await expect(page.getByRole("heading", { name: "Группы SCIM" })).toBeVisible();
      await expect(
        page.getByRole("region", { name: "Группы SCIM" }).getByRole("row", {
          name: new RegExp(groupName),
        }),
      ).toBeVisible();
      await expectNoSeriousViolations(page, "a provider's SCIM groups in Russian");
    });
  });

  test("revokes the token, after which the directory is refused", async ({ page }) => {
    await signIn(page);
    await page.goto("/admin/scim-tokens");
    const hint = token.slice(0, 12);
    await page.getByRole("button", { name: `Revoke ${hint}` }).click();
    const dialog = page.getByRole("dialog", { name: `Revoke ${hint}` });
    await expectNoSeriousViolations(page, "the revoke SCIM token dialog");
    await dialog.getByRole("button", { name: "Revoke token" }).click();

    const row = page.getByRole("row", { name: new RegExp(hint) });
    await expect(row).toContainText("Revoked");
    await expect(row.getByRole("button")).toHaveCount(0);
    expect((await scim(token, "GET", "/Users")).status).toBe(401);
  });
});
