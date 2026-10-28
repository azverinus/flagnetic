import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { seedProject, unprotectProduction } from "../support/console";
import {
  choose,
  openFlag,
  readConfiguration,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";

const flagKey = "banner";

/** A flag rolled out to a quarter of users in staging, with one user pinned; off and bare in production. */
async function seed(project: string) {
  await seedFlag(project, stringFlag(flagKey));
  return writeConfiguration(project, flagKey, "staging", () => ({
    on: true,
    overrides: [{ unit_type: "user", unit_id: "qa-1", variation: "treatment" }],
    rules: [
      {
        description: "A quarter of users",
        conditions: [],
        serve: { kind: "rollout", variation: "treatment", unit_type: "user", percentage: 2500 },
      },
    ],
  }));
}

const sheet = (page: Page) => page.getByRole("dialog", { name: "Compare environments" });

async function compareStagingWithProduction(
  page: Page,
  project: string,
  production = "Production (protected)",
) {
  await openFlag(page, project, flagKey, { environment: "staging" });
  await page.getByRole("button", { name: "Compare environments" }).click();
  await expect(sheet(page).getByRole("combobox", { name: "Compare" })).toHaveText("Staging");
  await choose(page, "With", production);
}

test.describe("comparing environments", () => {
  test("shows two configurations side by side, marking what SDKs would receive differently", async ({
    page,
  }) => {
    const project = await seedProject("compare");
    await seed(project);
    await compareStagingWithProduction(page, project);

    await expect(sheet(page).getByRole("status")).toHaveText(
      "The environments serve differently where marked.",
    );
    const table = sheet(page).getByRole("table", { name: "Staging and Production side by side" });
    const row = (part: string) => table.getByRole("row").filter({ hasText: part });
    await expect(row("On or off")).toContainText("Differs");
    await expect(row("Overrides")).toContainText("qa-1");
    await expect(row("Rule 1")).toContainText("A quarter of users");
    await expect(row("Rule 1")).toContainText("Differs");
    await expect(row("Off variation")).not.toContainText("Differs");

    // Development and production differ in nothing SDKs receive.
    await choose(page, "Compare", "Development");
    await expect(sheet(page).getByRole("status")).toHaveText("Both environments serve the same.");
  });

  test("copies staging into production, keeping production off and its overrides unless asked", async ({
    page,
  }) => {
    const project = await seedProject("copy");
    await unprotectProduction(project);
    const staging = await seed(project);
    await compareStagingWithProduction(page, project, "Production");

    await sheet(page).getByRole("button", { name: "Copy Staging here" }).click();
    const confirm = page.getByRole("dialog", { name: "Copy Staging into Production" });
    await expect(confirm).toContainText("Production stays off.");
    await confirm.getByRole("button", { name: "Copy configuration" }).click();
    await expect(
      page.getByText("The configuration of Staging is copied into Production."),
    ).toBeVisible();

    let production = await readConfiguration(project, flagKey, "production");
    expect(production.on).toBe(false);
    expect(production.overrides).toEqual([]);
    expect(production.rules[0]?.id).toBe(staging.rules[0]?.id);
    expect(production.rules[0]?.serve.salt).toBe(staging.rules[0]?.serve.salt);
    const table = sheet(page).getByRole("table", { name: "Staging and Production side by side" });
    await expect(table.getByRole("row").filter({ hasText: "Rule 1" })).not.toContainText("Differs");

    await sheet(page).getByRole("button", { name: "Copy Staging here" }).click();
    await confirm.getByLabel("Replace the overrides of Production with those of Staging").click();
    await confirm.getByRole("button", { name: "Copy configuration" }).click();
    await expect(confirm).toBeHidden();
    production = await readConfiguration(project, flagKey, "production");
    expect(production.on).toBe(false);
    expect(production.overrides.map((override) => override.unit_id)).toEqual(["qa-1"]);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the comparison has no serious violation in the ${theme} theme`, async ({ page }) => {
      const project = await seedProject(`compare-axe-${theme}`);
      await seed(project);
      await useTheme(page, theme);
      await compareStagingWithProduction(page, project);
      await expect(sheet(page).getByRole("table")).toBeVisible();
      await expectNoSeriousViolations(page, `comparing environments (${theme})`);
    });
  }
});
