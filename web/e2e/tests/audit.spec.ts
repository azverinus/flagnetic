import {
  createEnvironment,
  createUnitType,
  deleteEnvironment,
  updateEnvironment,
} from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { managementClient, must, seedProject, signIn } from "../support/console";
import { admin, seededProject, viewer } from "../support/instance";

/**
 * A project with a history: staging renamed, an environment created and
 * deleted, and enough unit types declared to fill more than one page.
 */
async function seedHistory(): Promise<string> {
  const client = managementClient();
  const project = await seedProject("audit");
  must(
    "renaming staging",
    await updateEnvironment({
      client,
      path: { project, environment: "staging" },
      body: { name: "Pre-production" },
    }),
  );
  must(
    "creating sandbox",
    await createEnvironment({
      client,
      path: { project },
      body: { key: "sandbox", name: "Sandbox" },
    }),
  );
  must(
    "deleting sandbox",
    await deleteEnvironment({ client, path: { project, environment: "sandbox" } }),
  );
  for (let i = 0; i < 50; i++) {
    must(
      `declaring kind_${i}`,
      await createUnitType({
        client,
        path: { project },
        body: { key: `kind_${i}`, name: `Kind ${i}` },
      }),
    );
  }
  return project;
}

const entries = (page: Page) =>
  page.getByRole("table", { name: "Audit entries, newest first" }).getByRole("row");

async function choose(page: Page, label: string, option: string) {
  await page.getByLabel(label, { exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

test("project audit log: filters, the entry with its before and after, and paging", async ({
  page,
}) => {
  const project = await seedHistory();
  await signIn(page);
  await page.goto(`/projects/${project}/audit`);
  await expect(page.getByRole("heading", { name: "Audit log", level: 1 })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toContainText("Audit log");

  // A full page, newest first, and the way to the older ones.
  await expect(entries(page)).toHaveCount(51);
  await expect(entries(page).nth(1)).toContainText("unit_type.created");
  await expectNoSeriousViolations(page, "project audit log");
  await page.getByRole("button", { name: "Older entries" }).click();
  await expect(page).toHaveURL(/cursor=/);
  await expect(entries(page).filter({ hasText: "project.created" })).toHaveCount(1);
  await page.getByRole("button", { name: "Newest entries" }).click();
  await expect(page).not.toHaveURL(/cursor=/);

  // The action filter, applied to the address.
  await choose(page, "Action", "Environment updated");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/action=environment\.updated/);
  await expect(entries(page)).toHaveCount(2);

  await entries(page)
    .getByRole("link", { name: /Environment updated/ })
    .click();
  const sheet = page.getByRole("dialog", { name: "Audit entry" });
  await expect(sheet).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/projects/${project}/audit/[0-9a-f-]{36}\\?`));
  await expect(sheet).toContainText("Environment updated");
  await expect(sheet).toContainText("environment.updated");
  // The history was written through the bootstrap token, which the entry names.
  await expect(sheet).toContainText("bootstrap (API token)");
  const name = sheet.getByRole("row", { name: /^name/ });
  await expect(name).toContainText("changed");
  await expect(name).toContainText('"Staging"');
  await expect(name).toContainText('"Pre-production"');
  await expectNoSeriousViolations(page, "audit entry");

  // The entry's address is shareable: a reload opens it again, filters kept.
  await page.reload();
  await expect(page.getByRole("dialog", { name: "Audit entry" })).toContainText('"Pre-production"');
  await expect(page.getByLabel("Action", { exact: true })).toHaveText("Environment updated");
  await page.getByRole("button", { name: "Close" }).click();
  await expect(page).toHaveURL(new RegExp(`/projects/${project}/audit\\?action=`));

  // Free text over the action, the entity's key and the actor's name.
  await page.getByRole("button", { name: "Clear filters" }).first().click();
  await expect(page).not.toHaveURL(/action=/);
  await page.getByLabel("Search").fill("sandbox");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/q=sandbox/);
  await expect(entries(page)).toHaveCount(3);
  await expect(entries(page).nth(1)).toContainText("Environment deleted");
  await expect(entries(page).nth(2)).toContainText("Environment created");
});

test("the actor filters: an instance admin with no grant, and the kind of actor", async ({
  page,
}) => {
  const project = await seedHistory();
  await signIn(page);
  await page.goto(`/projects/${project}/audit`);
  await expect(entries(page)).toHaveCount(51);

  // The history was written by the instance admin's token; the admin holds
  // no grant in the project and is still there to filter by.
  await choose(page, "Member", admin.name);
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/actor=[0-9a-f-]{36}/);
  await expect(page.getByLabel("Member", { exact: true })).toHaveText(admin.name);
  await expect(entries(page)).toHaveCount(51);
  await expect(entries(page).nth(1)).toContainText("bootstrap");

  await page.getByRole("button", { name: "Clear filters" }).first().click();
  await choose(page, "Actor kind", "API token");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/actor_kind=token/);
  await expect(entries(page).nth(1)).toContainText("API token");

  await choose(page, "Actor kind", "Command line");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/actor_kind=cli/);
  await expect(page.getByText("No entry matches these filters.")).toBeVisible();
});

test("a viewer reads the project's audit log and filters by whoever acted in it", async ({
  page,
}) => {
  await signIn(page, viewer);
  await page.goto(`/projects/${seededProject.key}/audit`);

  await expect(entries(page).filter({ hasText: "project.created" })).toHaveCount(1);

  // The viewer may not read the instance's members; the instance admin
  // acted here, through the harness's bootstrap token only, so they are
  // offered all the same, as that token's member.
  await choose(page, "Member", "The member whose API token is bootstrap");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/actor=[0-9a-f-]{36}/);
  await expect(entries(page).filter({ hasText: "project.created" })).toHaveCount(1);
});

test("the overview's recent activity shows a change made in the console, without a reload", async ({
  page,
}) => {
  const project = await seedProject("recent-activity");
  await signIn(page);
  await page.goto(`/projects/${project}`);
  const recent = page.getByRole("table", { name: "The most recent changes in this project" });
  await expect(recent).toContainText("project.created");
  await expect(recent).not.toContainText("environment.updated");
  // A reload would drop this mark.
  await page.evaluate(() => Object.assign(window, { sameDocument: true }));

  const navigation = page.getByRole("navigation", { name: "Project" });
  await navigation.getByRole("link", { name: "Environments" }).click();
  const staging = page
    .getByRole("row")
    .filter({ has: page.getByRole("cell", { name: "staging", exact: true }) });
  await staging.getByRole("button", { name: "Rename staging" }).click();
  const rename = page.getByRole("dialog", { name: "Rename staging" });
  await rename.getByLabel("Name").fill("Pre-production");
  await rename.getByRole("button", { name: "Save" }).click();
  await expect(rename).toBeHidden();
  await navigation.getByRole("link", { name: "Overview" }).click();

  await expect(recent.getByRole("row").nth(1)).toContainText("environment.updated");
  expect(await page.evaluate(() => "sameDocument" in window)).toBe(true);
});

test("instance audit log: entries of no project and the project filter", async ({ page }) => {
  await signIn(page);
  await page.goto("/admin/audit");
  await expect(page.getByRole("heading", { name: "Audit log", level: 1 })).toBeVisible();

  // Sign-ins belong to no project.
  await choose(page, "Action", "Signed in");
  await page.getByRole("button", { name: "Apply filters" }).click();
  const signedIn = entries(page).nth(1);
  await expect(signedIn).toContainText("auth.signed_in");
  await expect(signedIn).toContainText("Instance");
  await signedIn.getByRole("link").click();
  await expect(page.getByRole("dialog", { name: "Audit entry" })).toContainText("Signed in");
  await expectNoSeriousViolations(page, "instance audit entry");
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "Clear filters" }).first().click();
  await choose(page, "Project", seededProject.name);
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/project=/);
  const created = entries(page).filter({ hasText: "project.created" });
  await expect(created).toHaveCount(1);
  for (const row of await entries(page).all()) {
    if ((await row.getByRole("cell").count()) > 0) {
      await expect(row).toContainText(seededProject.name);
    }
  }
  await expectNoSeriousViolations(page, "instance audit log");

  // An entry of a project is read on its own: a shared link to it opens it
  // whatever page of the log it is on now.
  const listings: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/v1/audit") listings.push(request.url());
  });
  await created.getByRole("link").click();
  await expect(page).toHaveURL(new RegExp(`project_key=${seededProject.key}`));
  await expect(page.getByRole("dialog", { name: "Audit entry" })).toContainText("Project created");
  // The open entry's project key is no filter: the log is not read again
  // for it.
  expect(listings).toEqual([]);
  const id = new URL(page.url()).pathname.split("/").pop() ?? "";
  await page.goto(`/admin/audit/${id}?project_key=${seededProject.key}`);
  const sheet = page.getByRole("dialog", { name: "Audit entry" });
  await expect(sheet).toContainText("Project created");
  await expect(sheet).toContainText(seededProject.name);
  await page.getByRole("button", { name: "Close" }).click();
  await expect(page).toHaveURL(/\/admin\/audit$/);
});

test("instance audit log: a project's entries are not linked while the projects cannot be read", async ({
  page,
}) => {
  let refuse = true;
  await page.route("**/api/v1/projects", async (route) => {
    if (!refuse || route.request().method() !== "GET") return route.continue();
    return route.fulfill({
      status: 500,
      contentType: "application/problem+json",
      body: JSON.stringify({
        type: "about:blank",
        title: "Internal Server Error",
        status: 500,
        code: "internal",
        request_id: "req-e2e-3",
      }),
    });
  });
  await signIn(page);
  await page.goto("/admin/audit");
  await choose(page, "Action", "Project created");
  await page.getByRole("button", { name: "Apply filters" }).click();

  // Without the projects' keys a project's entry would open without one.
  const failure = page.getByRole("alert").filter({ hasText: "req-e2e-3" }).first();
  await expect(failure).toBeVisible({ timeout: 15_000 });
  const created = entries(page).nth(1);
  await expect(created).toContainText("project.created");
  await expect(created.getByRole("link")).toHaveCount(0);

  refuse = false;
  await failure.getByRole("button", { name: "Try again" }).click();

  await expect(created.getByRole("link")).toHaveCount(1);
  await expect(page.getByRole("alert").filter({ hasText: "req-e2e-3" })).toHaveCount(0);
});

test("the instance audit log is closed to a member who is not an instance admin", async ({
  page,
}) => {
  await signIn(page, viewer);
  await page.goto("/admin/audit");

  await expect(page.getByText("You cannot open this screen")).toBeVisible();
  await expect(page.getByRole("table")).toHaveCount(0);
});
