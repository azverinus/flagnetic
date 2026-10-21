import {
  createClient,
  createConfig,
  createMyToken,
  deactivateMember,
  getMe,
  listProjects,
  signIn as signInThroughTheApi,
} from "@flagnetic/api";
import { type Browser, expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import {
  managementClient,
  must,
  pathOf,
  seedInvitation,
  seedMember,
  signIn,
} from "../support/console";
import { admin, readState, viewer } from "../support/instance";

const password = "harness-orchard-pepper-6620";

/** A page of its own browser context, so it holds a session of its own. */
async function freshPage(browser: Browser): Promise<Page> {
  const context = await browser.newContext();
  return context.newPage();
}

/** The members table's row of one member, found by the name it shows. */
const rowOf = (page: Page, name: string) =>
  page.getByRole("row").filter({ has: page.getByRole("link", { name, exact: true }) });

/** The value of the one-time secret a reveal dialog shows. */
async function revealed(page: Page, title: string): Promise<string> {
  const dialog = page.getByRole("dialog", { name: title });
  await expect(dialog).toBeVisible();
  const value = (await dialog.locator("code").textContent()) ?? "";
  await dialog.getByLabel("I have stored it somewhere safe").check();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
  return value;
}

async function openMember(page: Page, id: string, name: string) {
  await page.goto(`/admin/members/${id}`);
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}

test.describe("instance members", () => {
  test("invites someone, who accepts, signs in, is deactivated and signed out, then reactivated", async ({
    page,
    browser,
    context,
  }) => {
    const run = Date.now().toString(36);
    const email = `grace-${run}@flagnetic.test`;
    const name = `Grace Hopper ${run}`;
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await signIn(page);
    await page.goto("/admin/members");
    await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
    await expect(
      page.getByRole("navigation", { name: "Instance" }).getByRole("link", { name: "Members" }),
    ).toBeVisible();

    await page.getByRole("button", { name: "Invite member" }).click();
    const invite = page.getByRole("dialog", { name: "Invite a member" });
    await invite.getByLabel("Email").fill(email);
    await invite.getByLabel("Display name").fill(name);
    await invite.getByRole("button", { name: "Invite" }).click();

    const reveal = page.getByRole("dialog", { name: `${name} is invited` });
    await expect(reveal).toContainText("This instance sends no mail");
    const shown = (await reveal.locator("code").textContent()) ?? "";
    expect(shown).toMatch(new RegExp(`^${readState().baseURL}/invitations/[^/]+$`));
    await reveal.getByRole("button", { name: "Copy" }).click();
    await expect(reveal.getByText("Copied to the clipboard.")).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(shown);
    // The link is shown this once: the dialog stays until it is stored.
    await page.keyboard.press("Escape");
    await expect(reveal).toBeVisible();
    const link = await revealed(page, `${name} is invited`);
    expect(link).toBe(shown);
    await expect(page.locator("body")).not.toContainText(link);

    await page.getByLabel("Search").fill(email);
    await expect(page).toHaveURL(new RegExp(`q=${encodeURIComponent(email)}`));
    await expect(rowOf(page, name)).toContainText("Invited");
    await expect(rowOf(page, name)).toContainText("Never");
    await expect(page.getByRole("row")).toHaveCount(2);

    const invitee = await freshPage(browser);
    await invitee.goto(pathOf(link));
    await expect(invitee.getByRole("heading", { name: "Accept your invitation" })).toBeVisible();
    await invitee.getByLabel("Password", { exact: true }).fill(password);
    await invitee.getByLabel("Confirm the password").fill(password);
    await invitee.getByRole("button", { name: "Accept invitation" }).click();
    await expect(invitee.getByRole("button", { name, exact: true })).toBeVisible();

    await page.reload();
    await expect(rowOf(page, name)).toContainText("Active");
    await expect(rowOf(page, name).locator("time")).toBeVisible();
    await expectNoSeriousViolations(page, "the members screen");

    await rowOf(page, name).getByRole("link", { name }).click();
    await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
    await page.getByRole("button", { name: "Deactivate", exact: true }).click();
    const deactivate = page.getByRole("dialog", { name: `Deactivate ${name}` });
    await expect(deactivate).toContainText(
      `Deactivation ends every session and API token of ${name} at once`,
    );
    await deactivate.getByRole("button", { name: "Deactivate" }).click();
    await expect(deactivate).toBeHidden();
    await expect(page.getByText(`${name} is deactivated.`)).toBeVisible();
    await expect(page.getByRole("definition").filter({ hasText: "Deactivated" })).toBeVisible();

    // Deactivation revoked the invitee's session.
    await invitee.reload();
    await expect(invitee).toHaveURL(/\/sign-in/);
    await expect(invitee.getByRole("heading", { name: "Sign in" })).toBeVisible();

    await page.getByRole("button", { name: "Reactivate" }).click();
    const reactivate = page.getByRole("dialog", { name: `Reactivate ${name}` });
    await expect(reactivate).toContainText("What deactivation revoked stays revoked");
    await reactivate.getByRole("button", { name: "Reactivate" }).click();
    await expect(reactivate).toBeHidden();
    await expect(page.getByRole("definition").filter({ hasText: "Active" })).toBeVisible();

    // The status filter lives in the address, like the search.
    await page.goto(`/admin/members?q=${encodeURIComponent(email)}`);
    await page.getByRole("combobox", { name: "Status" }).click();
    await page.getByRole("option", { name: "Active" }).click();
    await expect(page).toHaveURL(/status=active/);
    await expect(rowOf(page, name)).toBeVisible();
    await page.getByRole("combobox", { name: "Status" }).click();
    await page.getByRole("option", { name: "Deactivated" }).click();
    await expect(page).toHaveURL(/status=deactivated/);
    await expect(rowOf(page, name)).toBeHidden();
    await expect(
      page.getByText("No member matches the search and the status filter."),
    ).toBeVisible();
    // Following the sidebar's link drops the search and the filter, and the
    // field follows.
    await page
      .getByRole("navigation", { name: "Instance" })
      .getByRole("link", { name: "Members" })
      .click();
    await expect(page).toHaveURL(/\/admin\/members$/);
    await expect(page.getByLabel("Search")).toHaveValue("");
    await expect(rowOf(page, name)).toBeVisible();
    await invitee.context().close();
  });

  test("edits a member, and makes them instance admin and back", async ({ page }) => {
    const member = await seedMember("edith", "Edith Clarke", password);
    await signIn(page);
    await openMember(page, member.id, member.name);

    await page.getByRole("button", { name: "Edit" }).click();
    const edit = page.getByRole("dialog", { name: `Edit ${member.name}` });
    await edit.getByLabel("Email").fill(admin.email);
    await edit.getByRole("button", { name: "Save" }).click();
    await expect(edit.getByText("Another member already has this email address.")).toBeVisible();
    await edit.getByLabel("Email").fill(member.email);
    await edit.getByLabel("Display name").fill("Edith Clarke Renamed");
    await edit.getByRole("button", { name: "Save" }).click();
    await expect(edit).toBeHidden();
    await expect(
      page.getByRole("heading", { name: "Edith Clarke Renamed", level: 1 }),
    ).toBeVisible();

    await page.getByRole("button", { name: "Make instance admin" }).click();
    await page
      .getByRole("dialog", { name: "Make Edith Clarke Renamed an instance admin" })
      .getByRole("button", { name: "Make instance admin" })
      .click();
    await expect(page.getByText("Edith Clarke Renamed is an instance admin.")).toBeVisible();
    await page.goto(`/admin/members?q=${encodeURIComponent(member.email)}`);
    await expect(rowOf(page, "Edith Clarke Renamed")).toContainText("Instance admin");

    await openMember(page, member.id, "Edith Clarke Renamed");
    await page.getByRole("button", { name: "Revoke instance admin" }).click();
    await page
      .getByRole("dialog", { name: "Revoke instance admin from Edith Clarke Renamed" })
      .getByRole("button", { name: "Revoke instance admin" })
      .click();
    await expect(
      page.getByText("Edith Clarke Renamed is no longer an instance admin."),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Make instance admin" })).toBeVisible();
  });

  test("refuses to take instance admin from the last one, saying why", async ({ page }) => {
    await signIn(page);
    await page.goto(`/admin/members?q=${encodeURIComponent(admin.email)}`);
    await rowOf(page, admin.name).getByRole("link", { name: admin.name }).click();
    await expect(page.getByRole("heading", { name: admin.name, level: 1 })).toBeVisible();

    await page.getByRole("button", { name: "Revoke instance admin" }).click();
    const dialog = page.getByRole("dialog", { name: `Revoke instance admin from ${admin.name}` });
    await expect(dialog).toContainText("lose the instance's screens at once");
    await dialog.getByRole("button", { name: "Revoke instance admin" }).click();
    await expect(dialog.getByRole("alert")).toHaveText(
      "This is the instance's last active instance admin. Make another member instance admin first.",
    );

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await page.getByRole("button", { name: "Deactivate", exact: true }).click();
    const deactivate = page.getByRole("dialog", { name: `Deactivate ${admin.name}` });
    await deactivate.getByRole("button", { name: "Deactivate" }).click();
    await expect(deactivate.getByRole("alert")).toContainText("last active instance admin");
  });

  test("replaces an invited member's link, and only the new one opens", async ({
    page,
    browser,
  }) => {
    const invited = await seedInvitation("ida", "Ida Rhodes");
    await signIn(page);
    await openMember(page, invited.id, invited.name);
    await expect(page.getByRole("definition").filter({ hasText: "Invited" })).toBeVisible();

    await page.getByRole("button", { name: "Issue a new invitation link" }).click();
    const confirm = page.getByRole("dialog", {
      name: `Issue a new invitation link for ${invited.name}`,
    });
    await confirm.getByRole("button", { name: "Issue link" }).click();
    const fresh = await revealed(page, `A new invitation link for ${invited.name}`);
    expect(fresh).not.toBe(invited.link);

    const other = await freshPage(browser);
    await other.goto(pathOf(invited.link));
    await expect(
      other.getByRole("heading", { name: "This invitation no longer works" }),
    ).toBeVisible();
    await other.goto(pathOf(fresh));
    await expect(other.getByRole("heading", { name: "Accept your invitation" })).toBeVisible();
    await other.context().close();
  });

  test("shows the link a reactivation issues to a member who never set a password", async ({
    page,
  }) => {
    const invited = await seedInvitation("joan", "Joan Clarke");
    must(
      "deactivating",
      await deactivateMember({ client: managementClient(), path: { member: invited.id } }),
    );
    await signIn(page);
    await openMember(page, invited.id, invited.name);

    await expect(page.getByRole("button", { name: "Make instance admin" })).toHaveCount(0);
    await page.getByRole("button", { name: "Reactivate" }).click();
    await page
      .getByRole("dialog", { name: `Reactivate ${invited.name}` })
      .getByRole("button", { name: "Reactivate" })
      .click();
    const dialog = page.getByRole("dialog", { name: `${invited.name} is invited again` });
    await expect(dialog).toContainText("never set a password");
    const link = await revealed(page, `${invited.name} is invited again`);
    expect(link).toContain("/invitations/");
    await expect(page.getByRole("definition").filter({ hasText: "Invited" })).toBeVisible();
  });

  test("lists a member's sessions and tokens, and revokes them", async ({ page, browser }) => {
    const member = await seedMember("hedy", "Hedy Lamarr", password);
    // One device through the browser, one through the API holding a token.
    const device = await freshPage(browser);
    await signIn(device, member);
    const anonymous = createClient(createConfig({ baseUrl: `${readState().baseURL}/api/v1` }));
    const opened = await signInThroughTheApi({
      client: anonymous,
      body: { email: member.email, password: member.password },
    });
    const cookie = opened.response?.headers.get("set-cookie")?.split(";")[0] ?? "";
    const created = must(
      "creating a token",
      await createMyToken({
        client: anonymous,
        headers: { Cookie: cookie },
        body: { name: "deploy-bot", scopes: ["read"] },
      }),
    );

    await signIn(page);
    await openMember(page, member.id, member.name);
    const sessions = page
      .getByRole("table", { name: `The devices ${member.name} is signed in on` })
      .getByRole("row")
      .filter({ has: page.getByRole("cell") });
    await expect(sessions).toHaveCount(2);
    await expectNoSeriousViolations(page, "a member's page");

    await page.getByRole("button", { name: "Revoke all sessions" }).click();
    const dialog = page.getByRole("dialog", { name: `Sign ${member.name} out everywhere` });
    await dialog.getByRole("button", { name: "Revoke 2 sessions" }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText("Revoked 2 sessions.")).toBeVisible();
    await expect(page.getByText(`${member.name} is not signed in anywhere.`)).toBeVisible();
    await device.reload();
    await expect(device).toHaveURL(/\/sign-in/);

    await page.getByRole("tab", { name: "API tokens" }).click();
    await expect(page).toHaveURL(/tab=tokens/);
    const tokens = page.getByRole("table", { name: `The API tokens of ${member.name}` });
    const row = tokens.getByRole("row").filter({ hasText: "deploy-bot" });
    await expect(row).toContainText(created.token.hint);
    await expect(row).toContainText("Active");
    await row.getByRole("button", { name: "Revoke deploy-bot" }).click();
    await page
      .getByRole("dialog", { name: "Revoke deploy-bot" })
      .getByRole("button", { name: "Revoke token" })
      .click();
    await expect(row).toContainText("Revoked");
    await expect(row.getByRole("button")).toHaveCount(0);

    const refused = await getMe({
      client: createClient(
        createConfig({
          baseUrl: `${readState().baseURL}/api/v1`,
          headers: { Authorization: `Bearer ${created.value}` },
        }),
      ),
    });
    expect(refused.response?.status).toBe(401);
    expect((refused.error as { code?: string }).code).toBe("token_revoked");
    await device.context().close();
  });

  test("counts the members of each project on the projects screen", async ({ page }) => {
    await signIn(page);
    const projects = must(
      "listing the projects",
      await listProjects({ client: managementClient() }),
    );
    const counted = projects.items.find((project) => project.key === "checkout")?.member_count;
    expect(counted).toBeGreaterThan(0);

    await page.goto("/admin/projects");
    const table = page.getByRole("table", { name: "All projects" });
    const checkout = table.getByRole("row").filter({ hasText: "checkout" });
    await expect(checkout).toBeVisible();
    const headers = await table.getByRole("columnheader").allTextContents();
    await expect(checkout.getByRole("cell").nth(headers.indexOf("Members"))).toHaveText(
      String(counted),
    );
  });

  test("is absent from the sidebar and closed to a member who is not an instance admin", async ({
    page,
  }) => {
    await signIn(page, viewer);
    for (const screen of ["/admin/members", "/admin/auth-settings"]) {
      await page.goto(screen);
      await expect(page.getByText("You cannot open this screen")).toBeVisible();
      await expect(page.getByRole("navigation", { name: "Instance" })).toHaveCount(0);
    }
    await expect(page.getByRole("button", { name: "Invite member" })).toHaveCount(0);
  });

  test("says a member nobody has is not found, inside the shell", async ({ page }) => {
    await signIn(page);
    await page.goto("/admin/members/0192f0c4-0000-7000-8000-00000000dead");
    await expect(page.getByRole("heading", { name: "Member not found" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Instance" })).toBeVisible();
  });

  for (const theme of ["light", "dark"] as const) {
    test(`the members screen and the invitation dialog have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto("/admin/members");
      await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
      await expect(page.getByRole("row").nth(1)).toBeVisible();
      await expectNoSeriousViolations(page, `the members screen (${theme})`);
      await page.getByRole("button", { name: "Invite member" }).click();
      await expect(page.getByRole("dialog", { name: "Invite a member" })).toBeVisible();
      await expectNoSeriousViolations(page, `the invitation dialog (${theme})`);
    });

    test(`a member's page has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto(`/admin/members?q=${encodeURIComponent(viewer.email)}`);
      await rowOf(page, viewer.name).getByRole("link", { name: viewer.name }).click();
      await expect(page.getByRole("heading", { name: viewer.name, level: 1 })).toBeVisible();
      await expectNoSeriousViolations(page, `a member's sessions (${theme})`);
      await page.getByRole("tab", { name: "API tokens" }).click();
      await expect(
        page.getByRole("table", { name: `The API tokens of ${viewer.name}` }),
      ).toBeAttached();
      await expectNoSeriousViolations(page, `a member's tokens (${theme})`);
    });
  }
});
