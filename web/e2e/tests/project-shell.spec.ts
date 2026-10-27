import { updateProject } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, managementClient, must, seedProject, signIn } from "../support/console";
import { admin, secondProject, seededProject, viewer } from "../support/instance";

/** Signs in and lands on one project's overview. */
async function openProject(page: Page, project = seededProject, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${project.key}`);
  await expect(page.getByRole("heading", { name: project.name, level: 1 })).toBeVisible();
}

const switcher = (page: Page) => page.getByRole("button", { name: /^Project:/ });
const projectNavigation = (page: Page) => page.getByRole("navigation", { name: "Project" });

const environmentsReason =
  "Needs the permission to manage environments, held by Owner and Maintainer.";

test.describe("the instance projects screen", () => {
  test("creates a project that opens with its three environments", async ({ page }) => {
    const key = `warehouse-${Date.now().toString(36)}`;
    await signIn(page);
    await page.goto("/admin/projects");

    await expect(page.getByRole("heading", { name: "Projects", level: 1 })).toBeVisible();
    await page.getByLabel("Key").fill(key);
    await page.getByLabel("Name").fill("Warehouse");
    await page.getByRole("button", { name: "Create project" }).click();

    await expect(page).toHaveURL(new RegExp(`/projects/${key}$`));
    await expect(page.getByRole("heading", { name: "Warehouse", level: 1 })).toBeVisible();
    const environments = page.getByRole("list", { name: "Environments" }).getByRole("listitem");
    await expect(environments).toHaveCount(3);
    await expect(environments.nth(0)).toContainText("development");
    await expect(environments.nth(1)).toContainText("staging");
    await expect(environments.nth(2)).toContainText("production");
  });

  test("refuses a key the instance already uses, on the field", async ({ page }) => {
    await signIn(page);
    await page.goto("/admin/projects");

    await page.getByLabel("Key").fill(seededProject.key);
    await page.getByLabel("Name").fill("Checkout again");
    await page.getByRole("button", { name: "Create project" }).click();

    await expect(page.getByLabel("Key")).toHaveAccessibleDescription(
      /This instance already has a project with that key\./,
    );
    await expect(page.getByLabel("Key")).toHaveAttribute("aria-invalid", "true");
    await expect(page).toHaveURL(/\/admin\/projects$/);
  });

  test("refuses a key the schema's pattern does not allow, before asking the server", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/admin/projects");
    const creations: string[] = [];
    page.on("request", (request) => {
      if (request.method() === "POST" && request.url().endsWith("/api/v1/projects")) {
        creations.push(request.url());
      }
    });

    await page.getByLabel("Key").fill("Not A Key");
    await page.getByLabel("Name").fill("Nope");
    await page.getByRole("button", { name: "Create project" }).click();

    await expect(page.getByLabel("Key")).toHaveAttribute("aria-invalid", "true");
    await expect(page.getByLabel("Key")).toHaveAccessibleDescription(
      /Start with a lowercase letter/,
    );
    await expect(page.getByRole("button", { name: "Create project" })).toBeEnabled();
    expect(creations).toEqual([]);
  });

  test("is absent from the sidebar and closed to a member who is not an instance admin", async ({
    page,
  }) => {
    await openProject(page, seededProject, viewer);

    await expect(page.getByRole("navigation", { name: "Instance" })).toHaveCount(0);

    await page.goto("/admin/projects");
    await expect(page.getByText("You cannot open this screen")).toBeVisible();
    await expect(page.getByLabel("Key")).toHaveCount(0);
  });
});

test.describe("the project shell", () => {
  test("switches between projects and reopens the last one", async ({ page }) => {
    await openProject(page, seededProject);

    await switcher(page).click();
    await page.getByRole("link", { name: new RegExp(secondProject.name) }).click();
    await expect(page.getByRole("heading", { name: secondProject.name, level: 1 })).toBeVisible();

    // The start page reopens where the member left off.
    await page.goto("/");

    await expect(page).toHaveURL(new RegExp(`/projects/${secondProject.key}$`));
    await expect(page.getByRole("heading", { name: secondProject.name, level: 1 })).toBeVisible();
  });

  test("does not reopen a project the member holds no grant in any more", async ({ page }) => {
    await signIn(page, viewer);
    await page.evaluate(
      (key) => window.localStorage.setItem("flagnetic.last-project", key),
      secondProject.key,
    );

    await page.goto("/");

    await expect(page.getByRole("heading", { name: "Projects", level: 1 })).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
  });

  test("finds a project by key in the switcher's search", async ({ page }) => {
    await openProject(page, seededProject);

    await switcher(page).click();
    await page.getByLabel("Search projects").fill(secondProject.key);

    await expect(page.getByRole("link", { name: new RegExp(seededProject.name) })).toHaveCount(0);
    await page.getByRole("link", { name: new RegExp(secondProject.name) }).click();

    await expect(page).toHaveURL(new RegExp(`/projects/${secondProject.key}$`));
  });

  test("still lists every project behind “All projects”", async ({ page }) => {
    await openProject(page, seededProject);

    await switcher(page).click();
    await page.getByRole("link", { name: "All projects" }).click();

    await expect(page.getByRole("heading", { name: "Projects", level: 1 })).toBeVisible();
    await expect(page).toHaveURL(/\/\?all=true$/);
    await expect(page.getByRole("link", { name: seededProject.name })).toBeVisible();
    await expect(page.getByRole("link", { name: secondProject.name })).toBeVisible();
  });

  test("collapses the sidebar to its icon rail and remembers it", async ({ page }) => {
    await openProject(page, seededProject);

    await page.getByRole("button", { name: "Collapse the sidebar" }).click();
    // The screens keep their names for a screen reader, and lose them on screen.
    const environments = projectNavigation(page).getByRole("link", { name: "Environments" });
    await expect(environments).toBeVisible();
    await expect(environments.getByText("Environments")).toHaveClass(/sr-only/);

    await page.reload();

    await expect(page.getByRole("button", { name: "Expand the sidebar" })).toBeVisible();
  });

  test("names where the member is, from the route tree", async ({ page }) => {
    await openProject(page, seededProject);

    await projectNavigation(page).getByRole("link", { name: "Environments" }).click();

    const breadcrumb = page.getByRole("navigation", { name: "Breadcrumb" });
    await expect(breadcrumb).toContainText(seededProject.name);
    await expect(breadcrumb).toContainText("Environments");
  });

  test("renders not found inside the shell for a project key nobody has", async ({ page }) => {
    await signIn(page);

    await page.goto("/projects/no-such-project");

    await expect(page.getByRole("heading", { name: "Project not found" })).toBeVisible();
    // Inside the shell: the switcher is still there to choose another one.
    await expect(switcher(page)).toBeVisible();
  });

  test("renders forbidden for a project the member holds no role in", async ({ page }) => {
    await signIn(page, viewer);

    await page.goto(`/projects/${secondProject.key}`);

    await expect(page.getByRole("heading", { name: "You cannot open this project" })).toBeVisible();
    await expect(page.getByText("You need the permission to view the project.")).toBeVisible();
    await expect(switcher(page)).toBeVisible();
  });
});

test.describe("what a role may do", () => {
  test("hides the screens a viewer could obtain nothing from", async ({ page }) => {
    await openProject(page, seededProject, viewer);
    const nav = projectNavigation(page);

    await expect(nav.getByRole("link", { name: "Environments" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Attributes" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Settings" })).toHaveCount(0);
  });

  test("shows those screens to an instance admin", async ({ page }) => {
    await openProject(page, seededProject);

    await expect(projectNavigation(page).getByRole("link", { name: "Settings" })).toBeVisible();
  });

  test("disables an action a viewer cannot take and names what it needs", async ({ page }) => {
    await openProject(page, seededProject, viewer);
    await page.goto(`/projects/${seededProject.key}/environments`);

    const create = page.getByRole("button", { name: "New environment" });
    await expect(create).toHaveAttribute("aria-disabled", "true");
    await expect(create).toHaveAccessibleDescription(environmentsReason);

    await create.click({ force: true });
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });

  test("shows the same reason to whoever is looking, by pointer and by keyboard", async ({
    page,
  }) => {
    await openProject(page, seededProject, viewer);
    await page.goto(`/projects/${seededProject.key}/environments`);
    const create = page.getByRole("button", { name: "New environment" });
    const tooltip = page.getByRole("tooltip");
    await expect(tooltip).toBeHidden();

    // The action takes no pointer events; the wrapper around it does.
    await create.hover({ force: true });
    await expect(tooltip).toHaveText(environmentsReason);

    await page.mouse.move(0, 0);
    await expect(tooltip).toBeHidden();

    await create.focus();
    await expect(tooltip).toHaveText(environmentsReason);
    await page.keyboard.press("Escape");
    await expect(tooltip).toBeHidden();
  });

  test("leaves the same action alone for a member who may take it", async ({ page }) => {
    await openProject(page, seededProject);
    await page.goto(`/projects/${seededProject.key}/environments`);

    const create = page.getByRole("button", { name: "New environment" });
    await expect(create).not.toHaveAttribute("aria-disabled", "true");

    await create.click();
    await expect(page.getByRole("dialog", { name: "New environment" })).toBeVisible();
  });

  test("says what a screen would take rather than failing on a refusal", async ({ page }) => {
    await openProject(page, seededProject, viewer);

    await page.goto(`/projects/${seededProject.key}/settings`);

    await expect(page.getByText("You cannot open this screen")).toBeVisible();
    await expect(
      page.getByText("Needs the permission to edit the project, held by Owner."),
    ).toBeVisible();
    await expect(page.getByLabel("Name")).toHaveCount(0);
  });
});

test.describe("the overview", () => {
  const recentActivity = (page: Page) =>
    page.getByRole("table", { name: "The most recent changes in this project" });

  test("lists what has changed in the project", async ({ page }) => {
    await openProject(page, seededProject);

    await expect(page.getByRole("heading", { name: "Recent activity" })).toBeVisible();
    await expect(recentActivity(page)).toContainText("project.created");
    // The harness seeded the project through the bootstrap token.
    await expect(recentActivity(page)).toContainText("bootstrap");

    await recentActivity(page)
      .getByRole("link", { name: /project\.created/ })
      .click();
    await expect(page).toHaveURL(new RegExp(`/projects/${seededProject.key}/audit/[0-9a-f-]{36}`));
    await expect(page.getByRole("dialog", { name: "Audit entry" })).toContainText(
      "Project created",
    );

    // Closing it goes back to where it was opened.
    await page.getByRole("button", { name: "Close" }).click();
    await expect(page).toHaveURL(new RegExp(`/projects/${seededProject.key}$`));
    await expect(recentActivity(page)).toContainText("project.created");
  });

  test("renders a failed query where it belongs, with a way to ask again", async ({ page }) => {
    let refuse = true;
    await page.route(`**/api/v1/projects/${seededProject.key}/audit*`, async (route) => {
      if (!refuse) return route.continue();
      return route.fulfill({
        status: 500,
        contentType: "application/problem+json",
        body: JSON.stringify({
          type: "about:blank",
          title: "Internal Server Error",
          status: 500,
          code: "internal",
          request_id: "req-e2e-1",
        }),
      });
    });
    await openProject(page, seededProject);

    // Only the failed part of the screen says so; the rest is still there.
    const failure = page.getByRole("alert").filter({ hasText: "req-e2e-1" });
    await expect(failure).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole("heading", { name: "Recent activity" })).toBeVisible();
    await expect(page.getByRole("list", { name: "Environments" })).toBeVisible();
    await expect(recentActivity(page)).toHaveCount(0);

    refuse = false;
    await failure.getByRole("button", { name: "Try again" }).click();

    await expect(recentActivity(page)).toContainText("project.created");
    await expect(failure).toHaveCount(0);
  });
});

test.describe("the project navigation", () => {
  test("says a failed read of the caller's permissions once, and reads its screens again", async ({
    page,
  }) => {
    let refuse = true;
    await page.route(`**/api/v1/projects/${seededProject.key}/me`, async (route) => {
      if (!refuse) return route.continue();
      return route.fulfill({
        status: 500,
        contentType: "application/problem+json",
        body: JSON.stringify({
          type: "about:blank",
          title: "Internal Server Error",
          status: 500,
          code: "internal",
          request_id: "req-e2e-2",
        }),
      });
    });
    await signIn(page);
    await page.goto(`/projects/${seededProject.key}`);

    await expect(page.getByRole("alert")).toHaveCount(1);
    await expect(page.getByRole("alert")).toContainText("req-e2e-2");
    const navigation = projectNavigation(page);
    await expect(navigation).toContainText("The project's screens could not be read.");
    await expect(navigation.getByRole("link")).toHaveCount(0);

    refuse = false;
    await navigation.getByRole("button", { name: "Try again" }).click();

    await expect(projectNavigation(page).getByRole("link", { name: "Environments" })).toBeVisible();
    await expect(page.getByRole("heading", { name: seededProject.name, level: 1 })).toBeVisible();
    await expect(page.getByRole("alert")).toHaveCount(0);
  });
});

test.describe("the environment search parameter", () => {
  test("preselects the first environment by sort order and puts it in the URL", async ({
    page,
  }) => {
    await openProject(page, seededProject);

    await projectNavigation(page).getByRole("link", { name: "Flags", exact: true }).click();

    await expect(page).toHaveURL(/\/flags\?environment=development$/);
  });

  test("preselects the environment this project showed last", async ({ page }) => {
    await openProject(page, seededProject);
    await page.goto(`/projects/${seededProject.key}/flags?environment=staging`);
    await expect(page.getByRole("combobox", { name: "Environment" })).toHaveText("Staging");

    await page.goto(`/projects/${seededProject.key}/flags`);

    await expect(page).toHaveURL(/\/flags\?environment=staging$/);
  });

  test("keeps the environments of two projects apart", async ({ page }) => {
    await openProject(page, seededProject);
    await page.goto(`/projects/${seededProject.key}/flags?environment=production`);
    await expect(page).toHaveURL(/environment=production$/);

    await page.goto(`/projects/${secondProject.key}/flags`);

    await expect(page).toHaveURL(/\/flags\?environment=development$/);
  });

  test("drops an environment the project does not have", async ({ page }) => {
    await openProject(page, seededProject);

    await page.goto(`/projects/${seededProject.key}/flags?environment=sandbox`);

    await expect(page).toHaveURL(/\/flags\?environment=development$/);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`every screen in the project navigation has no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await openProject(page, seededProject);
      const screens = await projectNavigation(page).getByRole("link").allTextContents();
      expect(screens.length).toBeGreaterThan(1);

      for (const screen of screens) {
        const link = projectNavigation(page).getByRole("link", { name: screen, exact: true });
        await link.click();
        await expect(link).toHaveAttribute("data-status", "active");
        await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
        await expect(page.locator("[aria-busy=true]")).toHaveCount(0);
        await expectNoSeriousViolations(page, `${screen} (${theme})`);
      }
    });

    test(`the project overview has no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await openProject(page, seededProject);
      await expect(page.getByRole("list", { name: "Environments" })).toBeVisible();

      await expectNoSeriousViolations(page, `project overview (${theme})`);
    });

    test(`the projects list has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto("/?all=true");
      await expect(page.getByRole("link", { name: seededProject.name })).toBeVisible();

      await expectNoSeriousViolations(page, `projects list (${theme})`);
    });

    test(`the instance projects screen has no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto("/admin/projects");
      await expect(page.getByRole("heading", { name: "Projects", level: 1 })).toBeVisible();

      await expectNoSeriousViolations(page, `instance projects (${theme})`);
    });

    test(`the project settings have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await openProject(page, seededProject);
      await page.goto(`/projects/${seededProject.key}/settings`);
      await expect(page.getByLabel("Name")).toHaveValue(seededProject.name);

      await expectNoSeriousViolations(page, `project settings (${theme})`);
    });

    test(`a project nobody has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      await signIn(page);
      await page.goto("/projects/no-such-project");
      await expect(page.getByRole("heading", { name: "Project not found" })).toBeVisible();

      await expectNoSeriousViolations(page, `project not found (${theme})`);
    });

    test(`a disabled action has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      await openProject(page, seededProject, viewer);
      await page.goto(`/projects/${seededProject.key}/environments`);
      await page.getByRole("button", { name: "New environment" }).hover({ force: true });
      await expect(page.getByRole("tooltip")).toBeVisible();

      await expectNoSeriousViolations(page, `a disabled action (${theme})`);
    });

    test(`the project switcher has no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await openProject(page, seededProject);
      await switcher(page).click();
      await expect(page.getByLabel("Search projects")).toBeVisible();

      await expectNoSeriousViolations(page, `project switcher (${theme})`);
    });
  }

  test("returns focus to the switcher after it closes", async ({ page }) => {
    await openProject(page, seededProject);

    await switcher(page).click();
    await expect(page.getByLabel("Search projects")).toBeVisible();
    await page.keyboard.press("Escape");

    await expect(switcher(page)).toBeFocused();
  });
});

test.describe("renaming a project", () => {
  test("renames it while its key stays", async ({ page }) => {
    const key = `rename-${Date.now().toString(36)}`;
    await signIn(page);
    await page.goto("/admin/projects");
    await page.getByLabel("Key").fill(key);
    await page.getByLabel("Name").fill("Before");
    await page.getByRole("button", { name: "Create project" }).click();
    await expect(page.getByRole("heading", { name: "Before", level: 1 })).toBeVisible();

    await projectNavigation(page).getByRole("link", { name: "Settings" }).click();
    await expect(page.getByLabel("Key")).toBeDisabled();
    await expect(page.getByLabel("Key")).toHaveValue(key);
    await page.getByLabel("Name").fill("After");
    await expect(page.getByText("You have unsaved changes")).toBeVisible();
    await page.getByRole("button", { name: "Save" }).click();

    await expect(page.getByText("The project is saved.")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toContainText("After");
    await expect(switcher(page)).toHaveAccessibleName("Project: After");
  });
  test("shows who changed it meanwhile and what, and keeps the member's edits over it", async ({
    page,
  }) => {
    const key = await seedProject("conflict");
    await signIn(page);
    await page.goto(`/projects/${key}/settings`);
    await page.getByLabel("Description").fill("Where shoppers pay.");

    must(
      "renaming it elsewhere",
      await updateProject({
        client: managementClient(),
        path: { project: key },
        body: { name: "Theirs" },
        headers: { "If-Match": '"1"' },
      }),
    );
    await page.getByRole("button", { name: "Save" }).click();

    const banner = page.getByRole("alert").filter({ hasText: "after you opened this" });
    // The suite's seeding token made the change, so the entry names it.
    await expect(banner).toContainText("bootstrap saved a change on");
    await expect(banner.getByRole("list", { name: "Their changes" })).toHaveText(
      "Name changed from “Project conflict” to “Theirs”.",
    );
    await expectNoSeriousViolations(page, "the project settings conflict banner");
    await banner.getByRole("button", { name: "Keep my edits" }).click();

    await expect(page.getByText("The project is saved.")).toBeVisible();
    await expect(banner).toHaveCount(0);
    // Only the description was the member's edit: the other change stays.
    await expect(page.getByLabel("Name")).toHaveValue("Theirs");
    await page.reload();
    await expect(page.getByLabel("Name")).toHaveValue("Theirs");
    await expect(page.getByLabel("Description")).toHaveValue("Where shoppers pay.");
  });

  test("reloads what someone else saved meanwhile, losing the member's edits, on request", async ({
    page,
  }) => {
    const key = await seedProject("conflict-reload");
    await signIn(page);
    await page.goto(`/projects/${key}/settings`);
    await page.getByLabel("Name").fill("Mine");

    must(
      "renaming it elsewhere",
      await updateProject({
        client: managementClient(),
        path: { project: key },
        body: { name: "Theirs" },
        headers: { "If-Match": '"1"' },
      }),
    );
    await page.getByRole("button", { name: "Save" }).click();
    await page.getByRole("button", { name: "Reload and lose my edits" }).click();

    await expect(page.getByRole("alert").filter({ hasText: "after you opened this" })).toHaveCount(
      0,
    );
    await expect(page.getByLabel("Name")).toHaveValue("Theirs");
    await expect(page.getByText("You have unsaved changes")).toHaveCount(0);
  });
});
