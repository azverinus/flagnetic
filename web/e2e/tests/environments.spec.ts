import { getEnvironment, updateEnvironment } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  seedProject,
  signIn,
  unprotectProduction,
} from "../support/console";
import { admin, seededProject, viewer } from "../support/instance";

/** Signs in and opens one project's environments. */
async function openEnvironments(page: Page, projectKey: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/environments`);
  await expect(page.getByRole("heading", { name: "Environments", level: 1 })).toBeVisible();
  await expect(environmentRow(page, "staging")).toBeVisible();
}

/** The row of one environment in the environments table. */
const environmentRow = (page: Page, key: string) =>
  page.getByRole("row").filter({ has: page.getByRole("cell", { name: key, exact: true }) });

test.describe("environments", () => {
  test("renames staging while its key stays", async ({ page }) => {
    const projectKey = await seedProject("environments-rename");
    await openEnvironments(page, projectKey);

    const staging = environmentRow(page, "staging");
    await expect(staging).toContainText("Staging");

    await staging.getByRole("button", { name: "Rename staging" }).click();
    const rename = page.getByRole("dialog", { name: "Rename staging" });
    await rename.getByLabel("Name").fill("Pre-production");
    await rename.getByRole("button", { name: "Save" }).click();
    await expect(rename).toBeHidden();

    await expect(staging).toContainText("Pre-production");
    // The key is immutable, so it is still the same row.
    await expect(staging.getByRole("cell", { name: "staging", exact: true })).toBeVisible();
  });

  test("refuses a key the project already has, on the field", async ({ page }) => {
    const projectKey = await seedProject("environments-taken");
    await openEnvironments(page, projectKey);

    await page.getByRole("button", { name: "New environment" }).click();
    const create = page.getByRole("dialog", { name: "New environment" });
    await create.getByLabel("Key").fill("staging");
    await create.getByLabel("Name").fill("Another staging");
    await create.getByRole("button", { name: "Create environment" }).click();

    await expect(create.getByLabel("Key")).toHaveAccessibleDescription(
      /This project already has an environment with that key\./,
    );
    await expect(create).toBeVisible();
  });

  test("adds an environment and refuses to delete the last one, in the member's words", async ({
    page,
  }) => {
    const projectKey = await seedProject("environments-delete");
    // A protected environment is never deleted, so production goes unprotected.
    await unprotectProduction(projectKey);
    await openEnvironments(page, projectKey);

    await page.getByRole("button", { name: "New environment" }).click();
    const create = page.getByRole("dialog", { name: "New environment" });
    await create.getByLabel("Key").fill("sandbox");
    await create.getByLabel("Name").fill("Sandbox");
    await create.getByRole("button", { name: "Create environment" }).click();

    await expect(environmentRow(page, "sandbox")).toBeVisible();

    // Down to one, so the last delete meets the server's refusal.
    for (const doomed of ["development", "staging", "production"]) {
      await environmentRow(page, doomed)
        .getByRole("button", { name: `Delete ${doomed}` })
        .click();
      const confirm = page.getByRole("dialog", { name: `Delete ${doomed}` });
      // Deleting an environment cannot be taken back, so it asks for the key.
      await expect(confirm.getByRole("button", { name: "Delete environment" })).toBeDisabled();
      await confirm.getByLabel(`Type ${doomed} to confirm`).fill(doomed);
      await confirm.getByRole("button", { name: "Delete environment" }).click();
      await expect(confirm).toBeHidden();
      await expect(environmentRow(page, doomed)).toHaveCount(0);
    }

    await environmentRow(page, "sandbox").getByRole("button", { name: "Delete sandbox" }).click();
    const last = page.getByRole("dialog", { name: "Delete sandbox" });
    await last.getByLabel("Type sandbox to confirm").fill("sandbox");
    await last.getByRole("button", { name: "Delete environment" }).click();

    // The refusal is explained where the action was taken.
    await expect(last.getByRole("alert")).toHaveText(
      "This is the project's last environment. A project always keeps one to serve from.",
    );
    await page.keyboard.press("Escape");
    await expect(environmentRow(page, "sandbox")).toBeVisible();
  });

  test("refuses to delete a protected environment, in the member's words", async ({ page }) => {
    const projectKey = await seedProject("environments-protected");
    await openEnvironments(page, projectKey);

    await environmentRow(page, "production")
      .getByRole("button", { name: "Delete production" })
      .click();
    const confirm = page.getByRole("dialog", { name: "Delete production" });
    await confirm.getByLabel("Type production to confirm").fill("production");
    await confirm.getByRole("button", { name: "Delete environment" }).click();

    await expect(confirm.getByRole("alert")).toHaveText(
      "A protected environment cannot be deleted. Stop protecting it on its page first.",
    );
    await page.keyboard.press("Escape");
    await expect(environmentRow(page, "production")).toBeVisible();
  });
});

test.describe("an environment someone else changed meanwhile", () => {
  /** Renames an environment through the management API, as a colleague saving first would. */
  async function renameElsewhere(project: string, environment: string, name: string) {
    const current = must(
      `reading ${environment}`,
      await getEnvironment({ client: managementClient(), path: { project, environment } }),
    );
    must(
      `renaming ${environment}`,
      await updateEnvironment({
        client: managementClient(),
        path: { project, environment },
        body: { name },
        headers: { "If-Match": `"${current.version}"` },
      }),
    );
  }

  test("a rename shows their change in the dialog and reloads it on request", async ({ page }) => {
    const projectKey = await seedProject("environments-conflict");
    await openEnvironments(page, projectKey);
    await environmentRow(page, "staging").getByRole("button", { name: "Rename staging" }).click();
    const rename = page.getByRole("dialog", { name: "Rename staging" });
    await rename.getByLabel("Name").fill("Mine");

    await renameElsewhere(projectKey, "staging", "Theirs");
    await rename.getByRole("button", { name: "Save" }).click();

    const banner = rename.getByRole("alert").filter({ hasText: "after you opened this" });
    await expect(banner).toContainText("bootstrap saved a change on");
    await expect(banner).toContainText("Name changed from “Staging” to “Theirs”.");
    await expectNoSeriousViolations(page, "the rename dialog's conflict banner");
    await banner.getByRole("button", { name: "Reload and lose my edits" }).click();

    await expect(banner).toHaveCount(0);
    await expect(rename.getByLabel("Name")).toHaveValue("Theirs");
    // Saving now names the version just read, so it is accepted.
    await rename.getByLabel("Name").fill("Pre-production");
    await rename.getByRole("button", { name: "Save" }).click();
    await expect(rename).toBeHidden();
    await expect(environmentRow(page, "staging")).toContainText("Pre-production");
  });
});

test.describe("what a role may do with environments", () => {
  test("shows a viewer the environments and no way to change one", async ({ page }) => {
    await openEnvironments(page, seededProject.key, viewer);

    const staging = environmentRow(page, "staging");
    await expect(staging).toContainText("Staging");

    // The screen's own action says what it would take; the rows' are hidden.
    const create = page.getByRole("button", { name: "New environment" });
    await expect(create).toHaveAttribute("aria-disabled", "true");
    await expect(create).toHaveAccessibleDescription(
      "Needs the permission to manage environments, held by Owner and Maintainer.",
    );
    await expect(staging.getByRole("button", { name: "Rename staging" })).toHaveCount(0);
    await expect(staging.getByRole("button", { name: "Delete staging" })).toHaveCount(0);
    await expect(page.getByRole("columnheader", { name: "Actions" })).toHaveCount(0);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the environments screen has no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      const projectKey = await seedProject(`environments-axe-${theme}`);
      await useTheme(page, theme);
      await openEnvironments(page, projectKey);
      await expectNoSeriousViolations(page, `environments (${theme})`);

      await page.getByRole("button", { name: "New environment" }).click();
      await expect(page.getByRole("dialog", { name: "New environment" })).toBeVisible();
      await expectNoSeriousViolations(page, `the new environment dialog (${theme})`);
    });
  }

  test("returns focus to what opened a dialog after it closes", async ({ page }) => {
    const projectKey = await seedProject("environments-focus");
    await openEnvironments(page, projectKey);

    const remove = environmentRow(page, "staging").getByRole("button", { name: "Delete staging" });
    await remove.click();
    await expect(page.getByRole("dialog", { name: "Delete staging" })).toBeVisible();

    await page.keyboard.press("Escape");

    await expect(page.getByRole("dialog", { name: "Delete staging" })).toHaveCount(0);
    await expect(remove).toBeFocused();
  });
});
