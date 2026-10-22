import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { seedProject } from "../support/console";
import {
  openFlag,
  readConfiguration,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";
import { seededProject, viewer } from "../support/instance";

const flagKey = "banner";

/**
 * A flag whose staging configuration was switched on with a rollout to
 * a quarter of users (version 2), then left serving treatment to all
 * without it (version 3).
 */
async function seed(project: string, key = flagKey) {
  await seedFlag(project, stringFlag(key));
  const rolledOut = await writeConfiguration(project, key, "staging", () => ({
    on: true,
    rules: [
      {
        description: "A quarter of users",
        conditions: [],
        serve: { kind: "rollout", variation: "treatment", unit_type: "user", percentage: 2500 },
      },
    ],
  }));
  await writeConfiguration(project, key, "staging", () => ({
    fallthrough: "treatment",
    rules: [],
  }));
  return rolledOut;
}

const history = (page: Page) => page.getByRole("table", { name: "History of the flag" });
const drawer = (page: Page) => page.getByRole("dialog", { name: "Audit entry" });

async function openHistory(page: Page) {
  await page.getByRole("tab", { name: "History" }).click();
  await expect(page).toHaveURL(/tab=history/);
  await expect(history(page).getByRole("row")).toHaveCount(4);
}

test.describe("a flag's history", () => {
  test("lists every change newest first and shows what one changed", async ({ page }) => {
    const project = await seedProject("history");
    await seed(project);
    await openFlag(page, project, flagKey);

    await openHistory(page);
    const rows = history(page).getByRole("row");
    await expect(rows.nth(1)).toContainText("Configuration updated");
    await expect(rows.nth(1)).toContainText("Staging");
    await expect(rows.nth(3)).toContainText("Flag created");

    await rows.nth(1).getByRole("link").click();
    await expect(page).toHaveURL(/entry=/);
    const changes = drawer(page).getByRole("list", { name: "Changes to the configuration" });
    await expect(changes).toContainText("treatment");
    await expect(changes).toContainText("A quarter of users");
    await expect(drawer(page)).toContainText(
      "The configuration in Staging is at this version now.",
    );

    // The drawer opens from the address too, as a shared link does.
    await page.reload();
    await expect(changes).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(drawer(page)).toBeHidden();
    await expect(page).not.toHaveURL(/entry=/);
  });

  test("reverts a configuration to an earlier version with its rule identifiers and salts", async ({
    page,
  }) => {
    const project = await seedProject("revert");
    const rolledOut = await seed(project);
    await openFlag(page, project, flagKey);
    await openHistory(page);

    await history(page).getByRole("row").nth(2).getByRole("link").click();
    await drawer(page).getByRole("button", { name: "Revert to this version" }).click();
    const confirm = page.getByRole("dialog", { name: "Revert to version 2" });
    await expect(confirm).toContainText("now at version 3");
    await confirm.getByRole("button", { name: "Revert", exact: true }).click();

    await expect(
      page.getByText("The configuration in Staging is back to version 2."),
    ).toBeVisible();
    const reverted = await readConfiguration(project, flagKey, "staging");
    expect(reverted.version).toBe(4);
    expect(reverted.fallthrough).toBe("control");
    expect(reverted.rules).toHaveLength(1);
    expect(reverted.rules[0]?.id).toBe(rolledOut.rules[0]?.id);
    expect(reverted.rules[0]?.serve.salt).toBe(rolledOut.rules[0]?.serve.salt);

    await page.keyboard.press("Escape");
    await expect(history(page).getByRole("row").nth(1)).toContainText("Configuration reverted");
  });

  test("reverts a configuration to the version it started at", async ({ page }) => {
    const project = await seedProject("revert-first");
    await seed(project);
    await openFlag(page, project, flagKey);
    await openHistory(page);

    await history(page).getByRole("row").nth(2).getByRole("link").click();
    await drawer(page).getByRole("button", { name: "Revert to the version before" }).click();
    await page
      .getByRole("dialog", { name: "Revert to version 1" })
      .getByRole("button", { name: "Revert", exact: true })
      .click();

    await expect(
      page.getByText("The configuration in Staging is back to version 1."),
    ).toBeVisible();
    const reverted = await readConfiguration(project, flagKey, "staging");
    expect(reverted.on).toBe(false);
    expect(reverted.rules).toEqual([]);
  });

  test("refuses a revert based on a version someone changed meanwhile, and reverts on it", async ({
    page,
  }) => {
    const project = await seedProject("revert-stale");
    await seed(project);
    await openFlag(page, project, flagKey);
    await openHistory(page);
    await history(page).getByRole("row").nth(2).getByRole("link").click();
    await drawer(page).getByRole("button", { name: "Revert to this version" }).click();

    await writeConfiguration(project, flagKey, "staging", () => ({ off_variation: "treatment" }));
    const confirm = page.getByRole("dialog", { name: "Revert to version 2" });
    await confirm.getByRole("button", { name: "Revert", exact: true }).click();
    await expect(confirm.getByRole("alert")).toContainText("Someone else changed this");
    await expect(confirm).toContainText("now at version 4");

    await confirm.getByRole("button", { name: "Revert", exact: true }).click();
    await expect(
      page.getByText("The configuration in Staging is back to version 2."),
    ).toBeVisible();
    expect((await readConfiguration(project, flagKey, "staging")).version).toBe(5);
  });

  test("shows a viewer the history with reverting disabled and why", async ({ page }) => {
    const key = `history_viewer_${Date.now().toString(36)}`;
    await seed(seededProject.key, key);
    await openFlag(page, seededProject.key, key, { who: viewer });
    await openHistory(page);

    await history(page).getByRole("row").nth(2).getByRole("link").click();
    const revert = drawer(page).getByRole("button", { name: "Revert to this version" });
    await expect(revert).toHaveAttribute("aria-disabled", "true");
    await expect(revert).toHaveAccessibleDescription(
      /Needs the permission to revert flag configurations to earlier versions/,
    );
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the history and an entry's drawer have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      const project = await seedProject(`history-axe-${theme}`);
      await seed(project);
      await useTheme(page, theme);
      await openFlag(page, project, flagKey);
      await openHistory(page);
      await expectNoSeriousViolations(page, `the flag's history (${theme})`);

      await history(page).getByRole("row").nth(2).getByRole("link").click();
      await expect(
        drawer(page).getByRole("button", { name: "Revert to this version" }),
      ).toBeVisible();
      await expectNoSeriousViolations(page, `a history entry (${theme})`);
    });
  }
});
