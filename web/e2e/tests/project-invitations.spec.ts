import { createGrant } from "@flagnetic/api";
import { type Browser, expect, type Locator, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  pathOf,
  seedMember,
  seedProject,
  signIn,
} from "../support/console";
import { admin } from "../support/instance";
import { awaitMailTo, configureSmtp, removeSmtp } from "../support/mailpit";

const password = "harness-meadow-compass-3381";

/** An address of one test's own, so a retried run never meets its own member. */
function newEmail(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}@flagnetic.test`;
}

/** A page of its own browser context, so it holds a session of its own. */
async function freshPage(browser: Browser): Promise<Page> {
  const context = await browser.newContext();
  return context.newPage();
}

/** Signs in and opens one project's members. */
async function openMembers(page: Page, projectKey: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/members`);
  await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
}

/** Picks an option of one of the console's selects. */
async function choose(page: Page, scope: Locator, field: string, option: string) {
  await scope.getByRole("combobox", { name: field }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

/** Fills and sends the project's invite dialog. */
async function inviteToProject(page: Page, email: string, name: string, role: string) {
  await page.getByRole("button", { name: "Invite by email" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Invite to the project" });
  await dialog.getByLabel("Email").fill(email);
  await dialog.getByLabel("Display name").fill(name);
  await choose(page, dialog, "Role", role);
  await dialog.getByRole("button", { name: "Invite", exact: true }).click();
  await expect(dialog).toBeHidden();
}

/** The value of the one-time link a reveal dialog shows, closing it. */
async function revealed(page: Page, title: string): Promise<string> {
  const dialog = page.getByRole("dialog", { name: title });
  await expect(dialog).toBeVisible();
  const value = (await dialog.locator("code").textContent()) ?? "";
  await dialog.getByLabel("I have stored it somewhere safe").check();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
  return value;
}

/** Accepts the invitation at link in a browser of its own, with a password. */
async function accept(invitee: Page, link: string) {
  await invitee.goto(pathOf(link));
  await expect(invitee.getByRole("heading", { name: "Accept your invitation" })).toBeVisible();
  await invitee.getByLabel("Password", { exact: true }).fill(password);
  await invitee.getByLabel("Confirm the password").fill(password);
  await invitee.getByRole("button", { name: "Accept invitation" }).click();
}

const invitedTable = (page: Page) =>
  page.getByRole("table", { name: "Members invited to the project" });

const memberRow = (page: Page, name: string) =>
  page
    .getByRole("table", { name: "Members of the project" })
    .getByRole("row", { name: new RegExp(name) });

test.describe("invitations to a project, mailed", () => {
  // The SMTP server is the instance's: these specs configure it and leave
  // the instance without one, as every other spec expects it.
  test.describe.configure({ mode: "serial" });
  let projectKey: string;
  let olga: Credentials & { id: string };
  test.beforeAll(async () => {
    // Seeded before the server is configured: a seeded member accepts the
    // link their invitation answers, which a mailed invitation does not.
    projectKey = await seedProject("invite-mail");
    olga = await seedMember("olga-inv", "Olga Owner", "harness-harbour-anchor-2024");
    must(
      "granting owner",
      await createGrant({
        client: managementClient(),
        path: { project: projectKey },
        body: { member: olga.id, role: "owner" },
      }),
    );
    await configureSmtp();
  });
  test.afterAll(removeSmtp);

  test("an owner invites, the invitation is mailed, accepted and the grant shown", async ({
    page,
    browser,
  }) => {
    const email = newEmail("ada");
    const name = "Ada Byron";
    await openMembers(page, projectKey, olga);

    await page.getByRole("button", { name: "Invite by email" }).first().click();
    const dialog = page.getByRole("dialog", { name: "Invite to the project" });
    await expect(dialog).toBeVisible();
    await expectNoSeriousViolations(page, "the project invite dialog");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await inviteToProject(page, email, name, "Developer");

    const mailed = page.getByRole("dialog", { name: `${name} is invited` });
    await expect(mailed).toContainText(`The invitation is mailed to ${email}.`);
    await expect(mailed.locator("code")).toHaveCount(0);
    await expectNoSeriousViolations(page, "the mailed invitation dialog");
    await mailed.getByRole("button", { name: "Done" }).click();

    const row = invitedTable(page).getByRole("row", { name: new RegExp(name) });
    await expect(row).toContainText("Developer");
    await expect(row).toContainText("Olga Owner");
    await expect(row).toContainText("Works until");
    await expectNoSeriousViolations(page, "the members with an invited member");

    const mail = await awaitMailTo(email);
    expect(mail.text).toContain("in the project Project invite-mail");
    const link = /https?:\/\/\S+\/invitations\/\S+/.exec(mail.text)?.[0] ?? "";
    expect(link).not.toBe("");

    const invitee = await freshPage(browser);
    await invitee.goto(pathOf(link));
    await expect(
      invitee.getByText(
        "Olga Owner invites you to the project Project invite-mail as Developer. Accepting grants you that role there.",
      ),
    ).toBeVisible();
    await expectNoSeriousViolations(invitee, "the invitation page of a project");
    await accept(invitee, link);
    await expect(invitee.getByRole("button", { name, exact: true })).toBeVisible();

    await page.reload();
    await expect(memberRow(page, name)).toContainText("Developer");
    await expect(memberRow(page, name)).toContainText("Manual, granted by Olga Owner on");
    await expect(invitedTable(page)).toHaveCount(0);
  });
});

test.describe("invitations to a project, shown", () => {
  test("without SMTP the link is shown once, and inviting again replaces it", async ({
    page,
    browser,
  }) => {
    const projectKey = await seedProject("invite-link");
    const email = newEmail("grace");
    const name = "Grace Brewster";
    await openMembers(page, projectKey);

    await inviteToProject(page, email, name, "Viewer");
    const reveal = page.getByRole("dialog", { name: `${name} is invited` });
    await expect(reveal).toContainText(`pass this link to ${name} yourself`);
    const first = await revealed(page, `${name} is invited`);

    const row = invitedTable(page).getByRole("row", { name: new RegExp(name) });
    await expect(row).toContainText("Viewer");
    await row.getByRole("button", { name: `Invite ${name} again` }).click();
    const confirm = page.getByRole("dialog", { name: `Invite ${name} again` });
    await expect(confirm).toContainText(`${name} gets a fresh invitation as Viewer`);
    await confirm.getByRole("button", { name: "Invite again" }).click();
    const second = await revealed(page, `A new invitation link for ${name}`);
    expect(second).not.toBe(first);

    const invitee = await freshPage(browser);
    await invitee.goto(pathOf(first));
    await expect(
      invitee.getByRole("heading", { name: "This invitation no longer works" }),
    ).toBeVisible();
    await accept(invitee, second);
    await expect(invitee.getByRole("button", { name, exact: true })).toBeVisible();

    await page.reload();
    await expect(memberRow(page, name)).toContainText("Viewer");
  });

  test("an instance admin invites to the instance with a role in a project", async ({
    page,
    browser,
  }) => {
    const projectKey = await seedProject("invite-admin");
    const email = newEmail("hedy");
    const name = "Hedy Kiesler";
    await signIn(page);
    await page.goto("/admin/members");
    await page.getByRole("button", { name: "Invite member" }).click();
    const dialog = page.getByRole("dialog", { name: "Invite a member" });
    await dialog.getByLabel("Email").fill(email);
    await dialog.getByLabel("Display name").fill(name);
    await expect(dialog.getByRole("combobox", { name: "Role" })).toBeDisabled();
    await choose(page, dialog, "Project", "Project invite-admin");
    await choose(page, dialog, "Role", "Maintainer");
    await expectNoSeriousViolations(page, "the instance invite dialog with a project");
    await dialog.getByRole("button", { name: "Invite", exact: true }).click();
    const link = await revealed(page, `${name} is invited`);

    const invitee = await freshPage(browser);
    await invitee.goto(pathOf(link));
    await expect(
      invitee.getByText(/invites you to the project Project invite-admin as Maintainer\./),
    ).toBeVisible();
    await accept(invitee, link);
    await expect(invitee.getByRole("button", { name, exact: true })).toBeVisible();

    await page.goto(`/projects/${projectKey}/members`);
    await expect(memberRow(page, name)).toContainText("Maintainer");
  });
});
