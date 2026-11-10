import {
  createAuthProvider,
  disableAuthProvider,
  getAuthProvider,
  updateAuthProvider,
} from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, signIn } from "../support/console";
import { dexSlug, dexUsers, readDex, signInAtDex } from "../support/dex";
import { readState } from "../support/instance";

const heading = (page: Page, name: string) => page.getByRole("heading", { level: 1, name });
const personalMenu = (page: Page, name: string) => page.getByRole("button", { name });

/** The domain Dex's people have addresses in; the harness's own members share it. */
const domain = "flagnetic.test";

test.describe("auth providers", () => {
  // The providers are the instance's: the specs share them in order, and the
  // last one leaves every provider disabled, since the sign-in page of every
  // spec after this one offers what is enabled.
  test.describe.configure({ mode: "serial" });

  test("asks for a slug the server would accept before sending anything", async ({ page }) => {
    await signIn(page);
    await page.goto("/admin/auth-providers");
    await expect(heading(page, "Auth providers")).toBeVisible();

    await page.getByRole("button", { name: "New provider" }).click();
    const dialog = page.getByRole("dialog", { name: "New auth provider" });
    await dialog.getByLabel("Slug").fill("complete");
    await dialog.getByRole("button", { name: "Add provider" }).click();

    await expect(dialog.getByText("This slug is reserved.")).toBeVisible();
    await expect(dialog.getByText("Enter the issuer's URL.")).toBeVisible();
    await expect(dialog.getByText("Enter the client secret.")).toBeVisible();
    await expect(dialog.getByText("Enter at least one email domain.")).toBeVisible();
    await expectNoSeriousViolations(page, "the new auth provider dialog with its errors");
  });

  test("adds an OIDC provider and shows the redirect URI to register at Dex", async ({ page }) => {
    const dex = readDex();
    await signIn(page);
    await page.goto("/admin/auth-providers");

    await page.getByRole("button", { name: "New provider" }).click();
    const dialog = page.getByRole("dialog", { name: "New auth provider" });
    await dialog.getByLabel("Name", { exact: true }).fill("Dex");
    await dialog.getByLabel("Slug").fill(dexSlug);
    await dialog.getByLabel("Issuer").fill(dex.issuer);
    await dialog.getByLabel("Client ID").fill(dex.clientId);
    await dialog.getByLabel("Client secret").fill(dex.clientSecret);
    await dialog.getByLabel("Domains").fill(domain);
    // Linking is what lets Vera, who exists, sign in as herself.
    await dialog.getByRole("switch", { name: "Link members by email" }).click();
    await dialog.getByRole("button", { name: "Add provider" }).click();

    await expect(heading(page, "Dex")).toBeVisible();
    await expect(page.getByText(`${readState().baseURL}/sso/${dexSlug}/callback`)).toBeVisible();
    // The secret is write-only: the form says one is stored, and shows none.
    await expect(page.getByLabel("Client secret")).toHaveValue("");
    await expect(page.getByText(/A secret is stored and never shown again/)).toBeVisible();
    await expect(page.getByLabel("Slug")).toHaveAttribute("readonly", "");
    await expect(page.getByText(dex.clientSecret)).toHaveCount(0);
  });

  test("tests the provider and shows what Dex answered", async ({ page }) => {
    const dex = readDex();
    await signIn(page);
    await page.goto(`/admin/auth-providers/${dexSlug}`);

    await page.getByRole("button", { name: "Test" }).click();

    await expect(page.getByText("The identity provider answered")).toBeVisible();
    await expect(page.getByText(`${dex.issuer}/auth`, { exact: true })).toBeVisible();
    await expect(page.getByText(/does not list the S256/)).toHaveCount(0);
  });

  test("explains an identity provider that cannot be reached, then deletes it", async ({
    page,
  }) => {
    must(
      "adding the unreachable provider",
      await createAuthProvider({
        client: managementClient(),
        body: {
          slug: "nowhere",
          kind: "oidc",
          display_name: "Nowhere",
          enabled: false,
          domains: ["nowhere.test"],
          issuer: "http://127.0.0.1:9/dex",
          client_id: "flagnetic",
          client_secret: "unused",
        },
      }),
    );
    await signIn(page);
    await page.goto("/admin/auth-providers/nowhere");

    await page.getByRole("button", { name: "Test" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "could not be reached" })).toBeVisible();

    // Nothing depends on it, so it goes.
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    await page
      .getByRole("dialog", { name: "Delete Nowhere" })
      .getByRole("button", { name: "Delete provider" })
      .click();
    await expect(heading(page, "Auth providers")).toBeVisible();
    await expect(page.getByRole("link", { name: "Nowhere" })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Dex", exact: true })).toBeVisible();
  });

  test("keeps a member's edits apart from a newer version someone else saved", async ({ page }) => {
    await signIn(page);
    await page.goto(`/admin/auth-providers/${dexSlug}`);
    await expect(heading(page, "Dex")).toBeVisible();

    // Someone else saves first.
    const client = managementClient();
    const current = must(
      "reading the provider",
      await getAuthProvider({ client, path: { provider: dexSlug } }),
    );
    must(
      "renaming the provider",
      await updateAuthProvider({
        client,
        path: { provider: dexSlug },
        body: { display_name: "Dex (theirs)" },
        headers: { "If-Match": `"${current.version}"` },
      }),
    );

    await page.getByLabel("Name", { exact: true }).fill("Dex (mine)");
    await page.getByRole("button", { name: "Save", exact: true }).click();

    await expect(page.getByRole("alert")).toContainText("Someone else changed this");
    await page.getByRole("button", { name: "Load their version" }).click();
    await expect(heading(page, "Dex (theirs)")).toBeVisible();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Dex (theirs)");

    // Saving from their version works, and names the provider as it is now.
    await page.getByLabel("Name", { exact: true }).fill("Dex");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(heading(page, "Dex")).toBeVisible();
  });

  test("signs a member in through Dex and lands on the page asked", async ({ page }) => {
    await page.goto("/projects/checkout?tab=environments");
    await expect(page).toHaveURL(/\/sign-in\?returnTo=/);

    const button = page.getByRole("link", { name: "Sign in with Dex" });
    await expect(button).toBeVisible();
    await expectNoSeriousViolations(page, "the sign-in page with a provider");
    await button.click();
    await signInAtDex(page, dexUsers.vera);

    await expect(page).toHaveURL(/\/projects\/checkout\?tab=environments$/);
    await expect(personalMenu(page, dexUsers.vera.name)).toBeVisible();

    // The session says how it was opened.
    await page.goto("/settings/sessions");
    const row = page.getByRole("row", { name: /This device/ });
    await expect(row).toContainText("OpenID Connect");
    await expect(row).toContainText(dexSlug);
  });

  test("explains a sign-in that no member matches", async ({ page }) => {
    await page.goto("/sign-in");
    await page.getByRole("link", { name: "Sign in with Dex" }).click();
    await signInAtDex(page, dexUsers.stranger);

    await expect(page).toHaveURL(/\/sign-in\?error=member_not_found/);
    await expect(page.getByRole("alert")).toHaveText(
      "No member of this instance matches that account. Ask an instance admin to invite you.",
    );
    // Still signed out, asked for the email first.
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByRole("button", { name: "Continue" })).toBeVisible();
    await expectNoSeriousViolations(page, "the sign-in page with an error");
  });

  test("explains each error a failed sign-in can send the member back with", async ({ page }) => {
    for (const [code, text] of [
      ["attempt_mismatch", "That sign-in was started in another browser or window"],
      ["sso_failed", "Signing in through the identity provider failed."],
      ["idp_error", "The identity provider refused the sign-in."],
      ["member_deactivated", "Your account here is deactivated."],
      ["sso_required", "Your email address signs in through Dex only."],
    ] as const) {
      await page.goto(
        `/sign-in?error=${code}${code === "sso_required" ? `&provider=${dexSlug}` : ""}`,
      );
      await expect(page.getByRole("alert")).toContainText(text);
    }
  });

  test("keeps a provider that members signed in through, and disables it instead", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/admin/auth-providers");

    // Vera signed in, so the provider has an identity: it cannot be deleted.
    await page.getByRole("button", { name: "Delete Dex" }).click();
    const dialog = page.getByRole("dialog", { name: "Delete Dex" });
    await dialog.getByRole("button", { name: "Delete provider" }).click();
    await expect(dialog.getByRole("alert")).toContainText("still used by linked identities");
    await dialog.getByRole("button", { name: "Cancel" }).click();

    await page.getByRole("button", { name: "Disable Dex" }).click();
    await expect(page.getByRole("row", { name: /Dex/ })).toContainText("Disabled");

    // The sign-in page stops offering it, and its address answers 404.
    const signedOut = await page.context().browser()!.newContext();
    const visitor = await signedOut.newPage();
    await visitor.goto("/sign-in");
    await expect(visitor.getByRole("heading", { name: "Sign in" })).toBeVisible();
    await expect(visitor.getByRole("link", { name: /Sign in with/ })).toHaveCount(0);
    const refused = await signedOut.request.get(`${readState().baseURL}/sso/${dexSlug}/start`, {
      maxRedirects: 0,
    });
    expect(refused.status()).toBe(404);
    await signedOut.close();

    await page.getByRole("button", { name: "Enable Dex" }).click();
    await expect(page.getByRole("row", { name: /Dex/ })).toContainText("Enabled");
  });

  for (const theme of ["light", "dark"] as const) {
    test(`the list and a provider's page have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto("/admin/auth-providers");
      await expect(page.getByRole("link", { name: "Dex", exact: true })).toBeVisible();
      await expectNoSeriousViolations(page, `the auth providers (${theme})`);

      await page.goto(`/admin/auth-providers/${dexSlug}`);
      await page.getByRole("button", { name: "Test" }).click();
      await expect(page.getByText("The identity provider answered")).toBeVisible();
      await expectNoSeriousViolations(page, `an auth provider's page (${theme})`);
    });
  }

  test.afterAll(async () => {
    // Disabled, so the sign-in page of every spec after this one is as it
    // was. A run that failed before the provider was added has none to
    // disable, and says why in its own failure.
    await disableAuthProvider({ client: managementClient(), path: { provider: dexSlug } });
  });
});
