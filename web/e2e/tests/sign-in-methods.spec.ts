import { inviteMember } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  pathOf,
  seedMember,
  signIn,
  submitSignIn,
} from "../support/console";
import {
  type DexUser,
  disableDexProvider,
  dexUsers,
  enableDexProvider,
  signInAtDex,
} from "../support/dex";
import { seededProject } from "../support/instance";

const heading = (page: Page, name: string) => page.getByRole("heading", { level: 1, name });
const section = (page: Page, name: string) => page.getByRole("heading", { level: 2, name });
const personalMenu = (page: Page, name: string) => page.getByRole("button", { name, exact: true });
const identityRows = (page: Page) =>
  page.getByRole("table", { name: "The identities you linked" }).getByRole("row");

type Member = Credentials & { id: string; name: string };

/** Signs a member in through Dex from the sign-in page, whose button reads `button` in their language. */
async function signInThroughDex(page: Page, person: DexUser, button: string): Promise<void> {
  await page.goto("/sign-in?returnTo=%2Fsettings%2Fsign-in-methods");
  await page.getByRole("link", { name: button }).click();
  await signInAtDex(page, person);
  await expect(page).toHaveURL(/\/settings\/sign-in-methods$/);
}

/** Links the signed-in member to a person at Dex, from the sign-in methods, and comes back. */
async function linkDex(page: Page, person: DexUser): Promise<void> {
  await page.goto("/settings/sign-in-methods");
  await page.getByRole("button", { name: "Link Dex", exact: true }).click();
  await signInAtDex(page, person);
}

test.describe("sign-in methods", () => {
  // The members and the provider are shared in order: the second test meets
  // the identity the first one linked, and the provider is the instance's,
  // so the last hook leaves it disabled for every spec after this one.
  test.describe.configure({ mode: "serial" });

  let lena: Member;
  test.beforeAll(async () => {
    await enableDexProvider();
    lena = await seedMember("lena", "Lena Linker", "harness-pewter-compass-3281");
  });
  test.afterAll(async () => {
    await disableDexProvider();
  });

  test("links Dex, signs in with it, removes the password, and cannot unlink the last way in", async ({
    page,
    browser,
  }) => {
    await signIn(page, lena);
    await page.goto("/settings/sign-in-methods");
    await expect(section(page, "Password")).toBeVisible();
    await expect(page.getByText(/Your password was set on/)).toBeVisible();
    await expect(page.getByText("You have linked no identity.")).toBeVisible();
    await expectNoSeriousViolations(page, "the sign-in methods before linking");

    // Linking leaves for Dex and returns to the page it started from.
    await linkDex(page, dexUsers.lena);
    await expect(page).toHaveURL(/\/settings\/sign-in-methods$/);
    await expect(identityRows(page)).toHaveCount(2);
    const row = identityRows(page).nth(1);
    await expect(row).toContainText("Dex");
    await expect(row).toContainText("OpenID Connect");
    await expect(page.getByRole("button", { name: "Link Dex", exact: true })).toHaveCount(0);
    await expectNoSeriousViolations(page, "the sign-in methods with an identity");

    // Signing in through the identity opens the same member.
    const elsewhere = await browser.newContext();
    const other = await elsewhere.newPage();
    await other.goto("/sign-in");
    await other.getByRole("link", { name: "Sign in with Dex" }).click();
    await signInAtDex(other, dexUsers.lena);
    await expect(personalMenu(other, lena.name)).toBeVisible();
    await elsewhere.close();
    await page.reload();
    await expect(identityRows(page).nth(1)).not.toContainText("Never");

    // The password goes while an identity remains; then it is not a way in.
    await page.getByRole("button", { name: "Remove password" }).click();
    const removal = page.getByRole("dialog", { name: "Remove your password" });
    await expectNoSeriousViolations(page, "the removal of the password");
    await removal.getByRole("button", { name: "Remove password" }).click();
    await expect(page.getByText("Your password is removed.")).toBeVisible();
    await expect(page.getByText(/You have no password/)).toBeVisible();
    await expect(page.getByLabel("Current password")).toHaveCount(0);
    const refused = await browser.newContext();
    const attempt = await refused.newPage();
    await submitSignIn(attempt, lena);
    await expect(attempt.getByRole("alert")).toHaveText("The email or the password is not right.");
    await refused.close();

    // The identity is now the only way in, so it stays.
    await page.getByRole("button", { name: "Unlink Dex" }).click();
    const unlinking = page.getByRole("dialog", { name: "Unlink Dex" });
    await unlinking.getByRole("button", { name: "Unlink", exact: true }).click();
    await expect(unlinking.getByRole("alert")).toContainText(
      "That is the only usable way to sign in left",
    );
    await expectNoSeriousViolations(page, "a refused unlink");
    await unlinking.getByRole("button", { name: "Cancel" }).click();
    await expect(identityRows(page)).toHaveCount(2);
  });

  test("explains an account another member already holds, and links nothing", async ({ page }) => {
    const nina = await seedMember("nina", "Nina Nonlinker", "harness-cedar-lantern-5512");
    await signIn(page, nina);
    await linkDex(page, dexUsers.lena);

    await expect(page).toHaveURL(/\/settings\/sign-in-methods\?error=identity_linked$/);
    await expect(page.getByRole("alert")).toContainText(
      "already linked to another member, so nothing was linked",
    );
    await expect(page.getByText("You have linked no identity.")).toBeVisible();
    await expectNoSeriousViolations(page, "the sign-in methods with a linking error");
  });

  test("shows an instance admin a member's identities and lets them unlink a wrong one", async ({
    page,
    browser,
  }) => {
    const wendy = await seedMember("wendy", "Wendy Wronglink", "harness-slate-meadow-7740");
    const theirs = await browser.newContext();
    const wendyPage = await theirs.newPage();
    await signIn(wendyPage, wendy);
    await linkDex(wendyPage, dexUsers.mara);
    await expect(identityRows(wendyPage)).toHaveCount(2);
    await theirs.close();

    await signIn(page);
    await page.goto(`/admin/members/${wendy.id}?tab=identities`);
    await expect(page.getByText(/Wendy Wronglink holds a password, set on/)).toBeVisible();
    const list = page.getByRole("table", { name: "The identities Wendy Wronglink linked" });
    await expect(list.getByRole("row")).toHaveCount(2);
    await expectNoSeriousViolations(page, "a member's sign-in methods");

    await page.getByRole("button", { name: "Unlink Dex" }).click();
    const dialog = page.getByRole("dialog", { name: "Unlink Dex" });
    await expect(dialog).toContainText("Wendy Wronglink will no longer be able to sign in");
    await dialog.getByRole("button", { name: "Unlink", exact: true }).click();
    await expect(page.getByText("Wendy Wronglink has linked no identity.")).toBeVisible();

    // Lena holds no password, so her one identity is her last way in.
    await page.goto(`/admin/members/${lena.id}?tab=identities`);
    await expect(page.getByText("Lena Linker holds no password.")).toBeVisible();
    await page.getByRole("button", { name: "Unlink Dex" }).click();
    const refusal = page.getByRole("dialog", { name: "Unlink Dex" });
    await refusal.getByRole("button", { name: "Unlink", exact: true }).click();
    await expect(refusal.getByRole("alert")).toContainText(
      "That is the only usable way to sign in left",
    );
  });

  test("accepts an invitation through Dex, with the grant it carries and no password", async ({
    page,
  }) => {
    const invited = must(
      "inviting Ines",
      await inviteMember({
        client: managementClient(),
        body: {
          email: dexUsers.ines.email,
          display_name: dexUsers.ines.name,
          grant: { project: seededProject.key, role: "viewer" },
        },
      }),
    );
    await page.goto(pathOf(invited.invitation?.url ?? ""));

    await expect(heading(page, "Accept your invitation")).toBeVisible();
    // The password is allowed beside the provider that vouches for the email.
    await expect(page.getByLabel("Password", { exact: true })).toBeVisible();
    const button = page.getByRole("link", { name: "Sign in with Dex" });
    await expect(button).toBeVisible();
    await expectNoSeriousViolations(page, "an invitation offering a provider");

    await button.click();
    await signInAtDex(page, dexUsers.ines);
    await expect(personalMenu(page, dexUsers.ines.name)).toBeVisible();
    await page.goto("/");
    await expect(page.getByRole("link", { name: seededProject.name })).toBeVisible();

    // The link was used up by signing in.
    await page.goto(pathOf(invited.invitation?.url ?? ""));
    await expect(heading(page, "This invitation no longer works")).toBeVisible();
  });

  test("reads in Russian for a Russian browser", async ({ browser }) => {
    const context = await browser.newContext({ locale: "ru-RU" });
    const page = await context.newPage();
    try {
      // Lena no longer holds a password: Dex is how she signs in.
      await signInThroughDex(page, dexUsers.lena, "Войти через Dex");
      await expect(section(page, "Привязанные учётные записи")).toBeVisible();
      await expect(page.getByRole("link", { name: "Способы входа" })).toBeVisible();
      await expect(page.getByText("У вас нет пароля", { exact: false })).toBeVisible();
      await expect(page.getByRole("button", { name: "Отвязать Dex" })).toBeVisible();
      await expectNoSeriousViolations(page, "the sign-in methods in Russian");

      await page.getByRole("button", { name: "Отвязать Dex" }).click();
      const dialog = page.getByRole("dialog", { name: "Отвязать Dex" });
      await dialog.getByRole("button", { name: "Отвязать", exact: true }).click();
      await expect(dialog.getByRole("alert")).toContainText("последний рабочий способ входа");

      await page.goto("/settings?error=identity_linked");
      await expect(page).toHaveURL(/\/settings\/sign-in-methods\?error=identity_linked$/);
      await expect(page.getByRole("alert")).toContainText("уже привязана к другому участнику");
    } finally {
      await context.close();
    }
  });

  for (const theme of ["light", "dark"] as const) {
    test(`has no serious accessibility violation on the sign-in methods in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signInThroughDex(page, dexUsers.lena, "Sign in with Dex");
      await expect(identityRows(page)).toHaveCount(2);
      await expectNoSeriousViolations(page, `the sign-in methods (${theme})`);
    });
  }
});
