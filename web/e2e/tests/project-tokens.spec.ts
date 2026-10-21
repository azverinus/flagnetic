import { createClient, createConfig, createGrant, getMe, listEnvironments } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, seedProject, signIn } from "../support/console";
import { admin, readState, seededProject, viewer } from "../support/instance";

/** The management API with an API token. */
function withToken(token: string) {
  return createClient(
    createConfig({
      baseUrl: `${readState().baseURL}/api/v1`,
      headers: { Authorization: `Bearer ${token}` },
    }),
  );
}

async function openTokens(page: Page, projectKey: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/tokens`);
  await expect(page.getByRole("heading", { name: "Project tokens", level: 1 })).toBeVisible();
}

const tokenRow = (page: Page, name: string) =>
  page.getByRole("table", { name: "Project tokens" }).getByRole("row", { name: new RegExp(name) });

test.describe("project tokens", () => {
  test("an owner creates a token holding a role, sees it act for the project only, and revokes it", async ({
    page,
  }) => {
    const projectKey = await seedProject("project-tokens");
    await openTokens(page, projectKey);
    await expect(page.getByText("This project has no token yet.")).toBeVisible();

    await page.getByRole("button", { name: "New project token" }).click();
    const create = page.getByRole("dialog", { name: "New project token" });
    await create.getByLabel("Name").fill("deploy pipeline");
    await expect(create.getByRole("checkbox", { name: /instance:admin/ })).toHaveCount(0);
    await create.getByRole("button", { name: "Create token" }).click();
    await expect(create.getByRole("combobox", { name: "Role" })).toHaveAccessibleDescription(
      "Choose a role.",
    );
    await create.getByRole("combobox", { name: "Role" }).click();
    await page.getByRole("option", { name: "Developer", exact: true }).click();
    await create.getByRole("checkbox", { name: /^flags:write/ }).check();
    await create.getByRole("combobox", { name: "Expires" }).click();
    await page.getByRole("option", { name: "7 days" }).click();
    await create.getByRole("button", { name: "Create token" }).click();

    const reveal = page.getByRole("dialog", { name: "The new project token" });
    const value = (await reveal.locator("code").textContent()) ?? "";
    expect(value).toMatch(/^fn_prj_[A-Za-z0-9]{40}$/);
    await expectNoSeriousViolations(page, "the new project token's value");
    await reveal.getByLabel("I have stored it somewhere safe").check();
    await reveal.getByRole("button", { name: "Close" }).click();
    await expect(reveal).toBeHidden();
    await expect(page.getByText(value)).toHaveCount(0);

    const row = tokenRow(page, "deploy pipeline");
    await expect(row).toContainText(value.slice(0, "fn_prj_".length + 4));
    await expect(row).toContainText("Developer");
    await expect(row).toContainText("Ada Lovelace");
    await expect(row).toContainText("flags:write");
    await expect(row).toContainText("Active");

    // The token acts for its project with its role, and never for another
    // project nor on who has access.
    const pipeline = withToken(value);
    const me = await getMe({ client: pipeline });
    expect(me.data?.credential).toMatchObject({ project: projectKey, role: "developer" });
    expect(
      (await listEnvironments({ client: pipeline, path: { project: projectKey } })).response
        ?.status,
    ).toBe(200);
    expect(
      (await listEnvironments({ client: pipeline, path: { project: seededProject.key } })).response
        ?.status,
    ).not.toBe(200);
    const granted = await createGrant({
      client: pipeline,
      path: { project: projectKey },
      body: { member: "00000000-0000-0000-0000-000000000000", role: "viewer" },
    });
    expect(granted.response?.status).toBe(403);

    await row.getByRole("button", { name: "Revoke deploy pipeline" }).click();
    const revoke = page.getByRole("dialog", { name: "Revoke deploy pipeline" });
    await revoke.getByRole("button", { name: "Revoke token" }).click();
    await expect(revoke).toBeHidden();
    await expect(row).toContainText("Revoked");
    await expect(row.getByRole("button")).toHaveCount(0);

    const refused = await getMe({ client: pipeline });
    expect(refused.response?.status).toBe(401);
    expect(refused.error?.code).toBe("token_revoked");
  });

  test("are closed to a member without the permission, who is told what it takes", async ({
    page,
  }) => {
    await signIn(page, viewer);
    await page.goto(`/projects/${seededProject.key}/members`);
    const navigation = page.getByRole("navigation", { name: "Project" });
    await expect(navigation.getByRole("link", { name: "Members" })).toBeVisible();
    await expect(navigation.getByRole("link", { name: "Project tokens" })).toHaveCount(0);

    await page.goto(`/projects/${seededProject.key}/tokens`);
    await expect(page.getByRole("heading", { name: "Project tokens", level: 1 })).toBeVisible();
    await expect(page.getByRole("alert")).toContainText("You cannot open this screen");
    await expect(page.getByRole("alert")).toContainText(
      "Needs the permission to manage project tokens, held by Owner.",
    );
    await expect(page.getByRole("button", { name: "New project token" })).toHaveCount(0);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`has no serious accessibility violations on the tokens and the new token dialog (${theme})`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await openTokens(page, seededProject.key);
      await expectNoSeriousViolations(page, `the project tokens (${theme})`);
      await page.getByRole("button", { name: "New project token" }).click();
      await expect(page.getByRole("dialog", { name: "New project token" })).toBeVisible();
      await expectNoSeriousViolations(page, `the new project token dialog (${theme})`);
    });
  }
});
