import { createClient, createConfig, getMe, listProjects, updateMe } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, seedMember, signIn } from "../support/console";
import { readState } from "../support/instance";

/** The management API with a personal access token. */
function withToken(token: string) {
  return createClient(
    createConfig({
      baseUrl: `${readState().baseURL}/api/v1`,
      headers: { Authorization: `Bearer ${token}` },
    }),
  );
}

const tokenRow = (page: Page, name: string) =>
  page.getByRole("table", { name: "Your API tokens" }).getByRole("row", { name: new RegExp(name) });

async function openTokens(page: Page) {
  await page.goto("/settings/tokens");
  await expect(page.getByRole("heading", { name: "Personal settings", level: 1 })).toBeVisible();
}

test.describe("personal access tokens", () => {
  test.describe.configure({ mode: "serial" });

  let margaret: Credentials;
  test.beforeAll(async () => {
    margaret = await seedMember("margaret", "Margaret Hamilton", "harness-orbit-lunar-1969");
  });

  test("creates a read token, uses it, sees its last use, and revokes it", async ({ page }) => {
    // The last use is written by a worker once a minute.
    test.setTimeout(150_000);
    await signIn(page, margaret);
    await openTokens(page);
    await expect(page.getByText("You have no API token yet.")).toBeVisible();

    await page.getByRole("button", { name: "New API token" }).click();
    const create = page.getByRole("dialog", { name: "New API token" });
    await create.getByLabel("Name").fill("reporting script");
    await expect(create.getByRole("checkbox", { name: /^read/ })).toBeChecked();
    await expect(create.getByRole("checkbox", { name: /instance:admin/ })).toHaveCount(0);
    await expect(create.getByRole("combobox", { name: "Expires" })).toHaveText("30 days");
    await create.getByRole("button", { name: "Create token" }).click();

    const reveal = page.getByRole("dialog", { name: "Your new API token" });
    const value = (await reveal.locator("code").textContent()) ?? "";
    expect(value).toMatch(/^fn_pat_[A-Za-z0-9]{40}$/);
    await expectNoSeriousViolations(page, "the new token's value");
    await expect(reveal.getByRole("button", { name: "Close" })).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(reveal).toBeVisible();
    await reveal.getByLabel("I have stored it somewhere safe").check();
    await reveal.getByRole("button", { name: "Close" }).click();
    await expect(reveal).toBeHidden();
    await expect(page.getByText(value)).toHaveCount(0);

    const row = tokenRow(page, "reporting script");
    await expect(row).toContainText(value.slice(0, "fn_pat_".length + 4));
    await expect(row).toContainText("read");
    await expect(row).toContainText("Active");
    await expect(row.getByRole("cell").nth(4)).toHaveText("Never");

    const script = withToken(value);
    expect((await listProjects({ client: script })).response?.status).toBe(200);
    const renamed = await updateMe({ client: script, body: { display_name: "By a token" } });
    expect(renamed.response?.status).toBe(403);
    expect(renamed.error?.code).toBe("session_required");

    await expect(async () => {
      await page.reload();
      await expect(row.getByRole("cell").nth(4)).not.toHaveText("Never", { timeout: 1_000 });
    }).toPass({ timeout: 90_000, intervals: [5_000] });
    await expect(row.getByRole("cell").nth(4)).toContainText("127.0.0.1");

    await row.getByRole("button", { name: "Revoke reporting script" }).click();
    const revoke = page.getByRole("dialog", { name: "Revoke reporting script" });
    await revoke.getByRole("button", { name: "Revoke token" }).click();
    await expect(revoke).toBeHidden();
    await expect(row).toContainText("Revoked");
    await expect(row.getByRole("button")).toHaveCount(0);

    const refused = await getMe({ client: script });
    expect(refused.response?.status).toBe(401);
    expect(refused.error?.code).toBe("token_revoked");
  });

  test("gives a write scope read too, and creates a token that never expires", async ({ page }) => {
    await signIn(page, margaret);
    await openTokens(page);
    await page.getByRole("button", { name: "New API token" }).click();
    const create = page.getByRole("dialog", { name: "New API token" });
    await create.getByLabel("Name").fill("deploy pipeline");
    await create.getByRole("checkbox", { name: /^flags:write/ }).check();
    await expect(create.getByRole("checkbox", { name: /^read/ })).toBeDisabled();
    await expect(create.getByRole("checkbox", { name: /^read/ })).toBeChecked();
    await create.getByRole("combobox", { name: "Expires" }).click();
    await page.getByRole("option", { name: "Never" }).click();
    await create.getByRole("button", { name: "Create token" }).click();

    const reveal = page.getByRole("dialog", { name: "Your new API token" });
    await reveal.getByLabel("I have stored it somewhere safe").check();
    await reveal.getByRole("button", { name: "Close" }).click();
    const row = tokenRow(page, "deploy pipeline");
    await expect(row).toContainText("flags:write");
    await expect(row.getByRole("cell").nth(2)).toHaveText("Never");
  });

  for (const theme of ["light", "dark"] as const) {
    test(`has no serious accessibility violations on the tokens and the new token dialog (${theme})`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page, margaret);
      await openTokens(page);
      await expect(tokenRow(page, "reporting script")).toContainText("Revoked");
      await expectNoSeriousViolations(page, `the tokens (${theme})`);
      await page.getByRole("button", { name: "New API token" }).click();
      await expect(page.getByRole("dialog", { name: "New API token" })).toBeVisible();
      await expectNoSeriousViolations(page, `the new token dialog (${theme})`);
    });
  }
});
