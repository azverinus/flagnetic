import { readFileSync } from "node:fs";

import { disableAuthProvider } from "@flagnetic/api";
import { type Browser, expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, signIn } from "../support/console";
import { readState } from "../support/instance";
import {
  keycloakDomain,
  keycloakSlug,
  keycloakUsers,
  readKeycloak,
  signInAtKeycloak,
} from "../support/keycloak";

const heading = (page: Page, name: string) => page.getByRole("heading", { level: 1, name });
const certificates = (page: Page) => page.getByRole("list", { name: "SP certificates" });

/**
 * Signs Kai in through Keycloak in a browser of their own, from a page
 * that asks for a session, and checks they land on it signed in through
 * the SAML provider.
 */
async function signInAsKai(browser: Browser): Promise<void> {
  const context = await browser.newContext();
  try {
    const page = await context.newPage();
    await page.goto("/settings/sessions");
    await expect(page).toHaveURL(/\/sign-in\?returnTo=/);
    await page.getByRole("link", { name: "Sign in with Keycloak" }).click();
    await signInAtKeycloak(page, keycloakUsers.kai);

    await expect(page).toHaveURL(/\/settings\/sessions$/);
    const row = page.getByRole("row", { name: /This device/ });
    await expect(row).toContainText("SAML");
    await expect(row).toContainText(keycloakSlug);
  } finally {
    await context.close();
  }
}

test.describe("SAML providers", () => {
  // The provider is the instance's: the specs share it in order, and the
  // last one leaves it disabled, since the sign-in page of every spec
  // after this one offers what is enabled.
  test.describe.configure({ mode: "serial" });

  test("adds a SAML provider from Keycloak's metadata and shows what to register there", async ({
    page,
  }) => {
    const keycloak = readKeycloak();
    const { baseURL } = readState();
    await signIn(page);
    await page.goto("/admin/auth-providers");

    await page.getByRole("button", { name: "New provider" }).click();
    const dialog = page.getByRole("dialog", { name: "New auth provider" });
    await dialog.getByRole("radio", { name: "SAML" }).check();
    await dialog.getByLabel("Name", { exact: true }).fill("Keycloak");
    await dialog.getByLabel("Slug").fill(keycloakSlug);
    await dialog.getByLabel("Metadata URL").fill(keycloak.metadataURL);
    await dialog.getByLabel("Domains").fill(keycloakDomain);
    // Kai has no member yet: the first sign-in makes one.
    await dialog.getByRole("switch", { name: "Create members on first sign-in" }).click();
    await expectNoSeriousViolations(page, "the new SAML provider dialog");
    await dialog.getByRole("button", { name: "Add provider" }).click();

    await expect(heading(page, "Keycloak")).toBeVisible();
    await expect(page.getByText(`${baseURL}/sso/${keycloakSlug}/acs`)).toBeVisible();
    await expect(page.getByText(`${baseURL}/sso/${keycloakSlug}/metadata`)).toHaveCount(2);
    const idp = page.getByRole("region", { name: "Identity provider", exact: true });
    await expect(idp).toContainText(keycloak.metadataURL);
    await expect(idp).toContainText(keycloak.entityID);
    await expect(idp).toContainText("read again every day");
    await expect(certificates(page).getByRole("listitem")).toHaveCount(1);
    await expect(page.getByLabel("Metadata URL", { exact: true })).toHaveValue(
      keycloak.metadataURL,
    );
  });

  test("downloads the SP metadata to import at the identity provider", async ({ page }) => {
    await signIn(page);
    await page.goto(`/admin/auth-providers/${keycloakSlug}`);

    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.getByRole("link", { name: "Download SP metadata" }).click(),
    ]);

    expect(download.suggestedFilename()).toBe(`${keycloakSlug}-sp-metadata.xml`);
    const metadata = readFileSync((await download.path()) ?? "", "utf8");
    expect(metadata).toContain("EntityDescriptor");
    expect(metadata).toContain(`${readState().baseURL}/sso/${keycloakSlug}/acs`);
  });

  test("tests the provider and shows what Keycloak's metadata says", async ({ page }) => {
    await signIn(page);
    await page.goto(`/admin/auth-providers/${keycloakSlug}`);

    await page.getByRole("button", { name: "Test" }).click();

    const answer = page.getByRole("region", { name: "The identity provider answered" });
    await expect(answer).toContainText(readKeycloak().entityID);
    await expect(answer).toContainText("/protocol/saml");
  });

  test("signs a person in through Keycloak and lands on the page asked", async ({ browser }) => {
    await signInAsKai(browser);
  });

  test("rotates the SP certificate in two steps, signing in through it meanwhile", async ({
    page,
    browser,
  }) => {
    await signIn(page);
    await page.goto(`/admin/auth-providers/${keycloakSlug}`);
    await expect(certificates(page).getByRole("listitem")).toHaveCount(1);
    const first = (await certificates(page).getByRole("listitem").textContent()) ?? "";

    await page.getByRole("button", { name: "Start certificate rotation" }).click();
    const start = page.getByRole("dialog", { name: "Start certificate rotation" });
    await expectNoSeriousViolations(page, "the start of a certificate rotation");
    await start.getByRole("button", { name: "Generate next certificate" }).click();
    // A toast is checked once it has faded in: halfway, its text is paler.
    await expect(
      page.locator("[data-sonner-toast]").filter({ hasText: "next certificate of Keycloak" }),
    ).toHaveCSS("opacity", "1");

    await expect(certificates(page).getByRole("listitem")).toHaveCount(2);
    await expect(certificates(page).getByRole("listitem").nth(1)).toContainText("Next");
    await expect(
      page.getByText(/Import the SP metadata at the identity provider again/),
    ).toBeVisible();
    // Sign-in keeps working while the identity provider catches up.
    await signInAsKai(browser);

    await page.getByRole("button", { name: "Complete rotation" }).click();
    const complete = page.getByRole("dialog", { name: "Complete certificate rotation" });
    await expect(complete).toContainText("the old key is destroyed");
    await complete.getByRole("button", { name: "Complete rotation" }).click();

    await expect(certificates(page).getByRole("listitem")).toHaveCount(1);
    await expect(certificates(page).getByRole("listitem")).not.toHaveText(first);
    await expect(page.getByRole("button", { name: "Start certificate rotation" })).toBeVisible();
    await signInAsKai(browser);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`a SAML provider's page has no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto(`/admin/auth-providers/${keycloakSlug}`);
      await page.getByRole("button", { name: "Test" }).click();
      await expect(
        page.getByRole("region", { name: "The identity provider answered" }),
      ).toBeVisible();
      await expectNoSeriousViolations(page, `a SAML provider's page (${theme})`);
    });
  }

  test.afterAll(async () => {
    // Disabled, so the sign-in page of every spec after this one is as it
    // was. A run that failed before the provider was added has none to
    // disable, and says why in its own failure.
    await disableAuthProvider({ client: managementClient(), path: { provider: keycloakSlug } });
  });
});
