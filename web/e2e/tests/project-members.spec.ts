import { createGrant, deactivateMember, listProjectMembers } from "@flagnetic/api";
import { expect, type Locator, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  seedMember,
  seedProject,
  signIn,
} from "../support/console";
import { admin, seededProject, viewer } from "../support/instance";

/** Signs in and opens one project's members. */
async function openMembers(page: Page, projectKey: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/members`);
  await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
}

/** The row of one member in the members table, by their name. */
const memberRow = (page: Page, name: string) =>
  page
    .getByRole("table", { name: "Members of the project" })
    .getByRole("row", { name: new RegExp(name) });

/** Picks an option of one of the console's selects. */
async function choose(page: Page, scope: Locator, field: string, option: string) {
  await scope.getByRole("combobox", { name: field }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

/** Grants a role through the management API, as the instance admin. */
async function grant(project: string, member: string, role: "owner" | "viewer") {
  must(
    `granting ${role} to ${member}`,
    await createGrant({ client: managementClient(), path: { project }, body: { member, role } }),
  );
}

test.describe("project members and their grants", () => {
  test("an owner grants a member roles side by side and removes one of them", async ({ page }) => {
    const projectKey = await seedProject("members-grants");
    const linus = await seedMember("linus", "Linus Torvalds", "harness-penguin-kernel-1991");
    await openMembers(page, projectKey);
    await expect(page.getByText("Nobody holds a role in this project yet.")).toBeVisible();

    await page.getByRole("button", { name: "Grant a role", exact: true }).first().click();
    const dialog = page.getByRole("dialog", { name: "Grant a role", exact: true });
    await choose(page, dialog, "Member", `Linus Torvalds (${linus.email})`);
    await choose(page, dialog, "Role", "Developer");
    await expect(dialog.getByRole("combobox", { name: "Role" })).toHaveAccessibleDescription(
      "Lets the holder view the project, edit flags, change flag configurations, switch flags off with the kill switch, and edit segments.",
    );
    await dialog.getByRole("button", { name: "Grant role" }).click();
    await expect(dialog).toBeHidden();

    const row = memberRow(page, "Linus Torvalds");
    await expect(row).toContainText(linus.email);
    await expect(row).toContainText("Developer");
    await expect(row).toContainText("Manual, granted by Ada Lovelace on");

    // A second role from the member's row: the member is chosen already.
    await row.getByRole("button", { name: "Grant a role to Linus Torvalds" }).click();
    const more = page.getByRole("dialog", { name: "Grant a role to Linus Torvalds" });
    await expect(more.getByRole("combobox", { name: "Member" })).toContainText("Linus Torvalds");
    await choose(page, more, "Role", "Maintainer");
    await more.getByRole("button", { name: "Grant role" }).click();
    await expect(more).toBeHidden();
    await expect(row.getByRole("listitem")).toHaveCount(2);
    await expect(row.getByRole("listitem").nth(1)).toContainText("Maintainer");
    await expectNoSeriousViolations(page, "a member holding two roles");

    // The same role twice is refused in the dialog, in the member's words.
    await row.getByRole("button", { name: "Grant a role to Linus Torvalds" }).click();
    await choose(page, more, "Role", "Developer");
    await more.getByRole("button", { name: "Grant role" }).click();
    await expect(more.getByRole("alert")).toContainText(
      "The member already holds this role in the project.",
    );
    await more.getByRole("button", { name: "Cancel" }).click();

    await row.getByRole("button", { name: "Remove Developer from Linus Torvalds" }).click();
    const removal = page.getByRole("dialog", { name: "Remove Developer from Linus Torvalds" });
    await expect(removal).toContainText("keeps their other roles here");
    await removal.getByRole("button", { name: "Remove grant" }).click();
    await expect(removal).toBeHidden();
    await expect(row.getByRole("listitem")).toHaveCount(1);
    await expect(row).toContainText("Maintainer");
    await expect(row).not.toContainText("Developer");

    const members = must(
      "listing the members",
      await listProjectMembers({ client: managementClient(), path: { project: projectKey } }),
    );
    expect(members.items.map((member) => member.grants.map((g) => g.role))).toEqual([
      ["maintainer"],
    ]);

    // Removing the last grant takes the member out of the project's list.
    await row.getByRole("button", { name: "Remove Maintainer from Linus Torvalds" }).click();
    const last = page.getByRole("dialog", { name: "Remove Maintainer from Linus Torvalds" });
    await expect(last).toContainText("They stay a member of the instance.");
    await last.getByRole("button", { name: "Remove grant" }).click();
    await expect(page.getByText("Nobody holds a role in this project yet.")).toBeVisible();
  });

  test("refuses a role to a deactivated member, in the member's words", async ({ page }) => {
    const projectKey = await seedProject("members-deactivated");
    const ken = await seedMember("ken", "Ken Thompson", "harness-unix-bell-labs-1969");
    await grant(projectKey, ken.id, "viewer");
    must(
      "deactivating Ken",
      await deactivateMember({ client: managementClient(), path: { member: ken.id } }),
    );
    await openMembers(page, projectKey);

    const row = memberRow(page, "Ken Thompson");
    await expect(row).toContainText("Deactivated");
    await row.getByRole("button", { name: "Grant a role to Ken Thompson" }).click();
    const dialog = page.getByRole("dialog", { name: "Grant a role to Ken Thompson" });
    await choose(page, dialog, "Role", "Developer");
    await dialog.getByRole("button", { name: "Grant role" }).click();
    await expect(dialog.getByRole("alert")).toContainText(
      "The member is deactivated. Reactivate them first.",
    );
  });

  test("an owner who is not an instance admin grants further roles to the project's members only", async ({
    page,
  }) => {
    const projectKey = await seedProject("members-owner");
    const olga = await seedMember("olga", "Olga Owner", "harness-harbour-anchor-2024");
    const linus = await seedMember("linus-o", "Linus Torvalds", "harness-penguin-kernel-1991");
    const outsider = await seedMember("outsider", "Otto Outsider", "harness-outside-window-2025");
    await grant(projectKey, olga.id, "owner");
    await grant(projectKey, linus.id, "viewer");
    await openMembers(page, projectKey, olga);

    // The instance admin who made the grants holds none here, which Olga cannot read past.
    await expect(memberRow(page, "Linus Torvalds")).toContainText(
      "by a member without a grant here",
    );

    await page.getByRole("button", { name: "Grant a role", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Grant a role", exact: true });
    await expect(dialog.getByRole("combobox", { name: "Member" })).toHaveAccessibleDescription(
      /grant further roles to the members of this project/,
    );
    await dialog.getByRole("combobox", { name: "Member" }).click();
    await expect(page.getByRole("option")).toHaveText([
      `Linus Torvalds (${linus.email})`,
      `Olga Owner (${olga.email})`,
    ]);
    await expect(page.getByRole("option", { name: new RegExp(outsider.name) })).toHaveCount(0);
    await page.getByRole("option", { name: `Linus Torvalds (${linus.email})` }).click();
    await choose(page, dialog, "Role", "Developer");
    await dialog.getByRole("button", { name: "Grant role" }).click();
    await expect(dialog).toBeHidden();
    await expect(memberRow(page, "Linus Torvalds")).toContainText(
      "Manual, granted by Olga Owner on",
    );
  });

  test("a viewer sees who holds what, and why they cannot change it", async ({ page }) => {
    await openMembers(page, seededProject.key, viewer);
    await expect(memberRow(page, viewer.name)).toContainText("Viewer");

    const grantRole = page.getByRole("button", { name: "Grant a role", exact: true });
    await expect(grantRole).toHaveAttribute("aria-disabled", "true");
    await expect(grantRole).toHaveAccessibleDescription(
      "Needs the permission to manage who has access, held by Owner.",
    );
    await grantRole.click({ force: true });
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(memberRow(page, viewer.name).getByRole("button")).toHaveCount(0);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`has no serious accessibility violations on the members and the grant dialog (${theme})`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await openMembers(page, seededProject.key);
      await expect(memberRow(page, viewer.name)).toBeVisible();
      await expectNoSeriousViolations(page, `the members (${theme})`);
      await page.getByRole("button", { name: "Grant a role", exact: true }).click();
      await expect(page.getByRole("dialog", { name: "Grant a role", exact: true })).toBeVisible();
      await expectNoSeriousViolations(page, `the grant dialog (${theme})`);
    });
  }
});
