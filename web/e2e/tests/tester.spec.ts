import { createSegment } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedProject } from "../support/console";
import {
  choose,
  openFlag,
  readConfiguration,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";
import { seededProject, viewer } from "../support/instance";

const flagKey = "banner";

/**
 * A project whose flag, on in development, serves `treatment` to the
 * private segment `employees` (user emp-1 or emp-2) and pins emp-2 to
 * `control` by an override.
 */
async function seed(): Promise<string> {
  const project = await seedProject("tester");
  must(
    "creating the private segment",
    await createSegment({
      client: managementClient(),
      path: { project },
      body: {
        key: "employees",
        name: "Employees",
        private: true,
        conditions: [{ kind: "attribute", attribute: "user", op: "in", value: ["emp-1", "emp-2"] }],
      },
    }),
  );
  await seedFlag(
    project,
    stringFlag(flagKey, {
      name: "Banner",
      variations: [
        { key: "control", value: "control" },
        { key: "treatment", value: "treatment" },
        { key: "beta", value: "beta" },
      ],
    }),
  );
  await writeConfiguration(project, flagKey, "development", () => ({
    on: true,
    off_variation: "control",
    fallthrough: "control",
    overrides: [{ unit_type: "user", unit_id: "emp-2", variation: "control" }],
    rules: [
      {
        description: "Employees",
        conditions: [{ kind: "in_segment", segment: "employees" }],
        serve: { kind: "variation", variation: "treatment" },
      },
    ],
  }));
  return project;
}

async function openBanner(page: Page, project: string) {
  await openFlag(page, project, flagKey, { environment: "development" });
  await expect(page.getByRole("region", { name: "Rule 1" })).toBeVisible();
}

const testerOf = (page: Page) => page.getByRole("complementary", { name: "Evaluate for context" });

test("the tester evaluates the saved rules and the unsaved draft, marking overrides", async ({
  page,
}) => {
  const project = await seed();
  await openBanner(page, project);

  // `t` opens the tester beside the editor and puts it in the URL.
  await page.keyboard.press("t");
  const tester = testerOf(page);
  await expect(tester).toBeVisible();
  await expect(page).toHaveURL(/tester=true/);

  const field = (name: string) => tester.getByLabel(name, { exact: true });
  const evaluate = () => tester.getByRole("button", { name: "Evaluate", exact: true }).click();

  // A matching context: the segment's rule decides.
  await choose(page, "Unit type (unit 1)", "user");
  await tester.getByLabel("Unit ID (unit 1)").fill("emp-1");
  await tester.getByRole("button", { name: "Add attribute" }).click();
  await choose(page, "Attribute 1", "platform");
  await tester.getByLabel("Value (attribute 1)").fill("ios");
  await evaluate();
  await expect(field("Value")).toHaveText('"treatment"');
  await expect(field("Variation")).toHaveText("treatment");
  await expect(field("Evaluation reason")).toContainText("Matched rule");
  await expect(field("Rule")).toContainText("Rule 1");
  const rule1 = page.getByRole("region", { name: "Rule 1" });
  await expect(rule1.getByText("Matched in the tester")).toBeVisible();
  await expect(rule1).toBeInViewport();

  // The context is remembered: closing and reopening reloads it.
  await tester.getByRole("button", { name: "Close the tester" }).click();
  await expect(tester).toBeHidden();
  await expect(rule1.getByText("Matched in the tester")).toBeHidden();
  await page.getByRole("button", { name: "Tester", exact: true }).click();
  await expect(tester.getByLabel("Unit ID (unit 1)")).toHaveValue("emp-1");
  await expect(tester.getByLabel("Value (attribute 1)")).toHaveValue("ios");
  await expect(
    tester.getByRole("region", { name: "Recent contexts" }).getByRole("button", {
      name: "user emp-1 · platform=ios",
    }),
  ).toBeVisible();

  // The unsaved draft serves beta by the same rule; the saved one still treatment.
  await choose(page, "Variation (rule 1)", "beta");
  await evaluate();
  await expect(field("Value")).toHaveText('"treatment"');
  await expect(tester.getByText("Evaluated with the saved configuration.")).toBeVisible();
  await tester.getByRole("switch", { name: "Use unsaved draft" }).click();
  await evaluate();
  await expect(tester.getByText("Evaluated with the unsaved draft.")).toBeVisible();
  await expect(field("Value")).toHaveText('"beta"');
  await expect(field("Rule")).toContainText("Rule 1");
  const saved = await readConfiguration(project, flagKey, "development");
  expect(saved.rules[0]?.serve.variation).toBe("treatment");

  // A draft the server would refuse says why in the tester.
  await rule1.getByRole("radio", { name: "Rollout" }).check();
  await rule1.getByLabel("Percentage (rule 1)").fill("150");
  await evaluate();
  await expect(tester.getByRole("alert")).toContainText(/percentage/i);
  await rule1.getByRole("radio", { name: "Variation" }).check();

  // An overridden unit: the override decides, before any rule.
  await tester.getByLabel("Unit ID (unit 1)").fill("emp-2");
  await evaluate();
  await expect(field("Evaluation reason")).toContainText("Override");
  await expect(field("Value")).toHaveText('"control"');
  await expect(tester.getByText(/an override serves this unit ID/i)).toBeVisible();
  await expect(rule1.getByText("Matched in the tester")).toBeHidden();

  // Esc closes the tester and leaves the URL, the draft staying.
  await tester.getByLabel("Unit ID (unit 1)").press("Escape");
  await expect(tester).toBeHidden();
  await expect(page).not.toHaveURL(/tester=/);
  await expect(page.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
});

test("a viewer evaluates with the tester", async ({ page }) => {
  const key = `tester_viewer_${Date.now().toString(36)}`;
  await seedFlag(seededProject.key, stringFlag(key));
  await openFlag(page, seededProject.key, key, { who: viewer });

  await page.getByRole("button", { name: "Tester", exact: true }).click();
  const tester = testerOf(page);
  await tester.getByLabel("Unit ID (unit 1)").fill("someone");
  await tester.getByRole("button", { name: "Evaluate", exact: true }).click();

  await expect(tester.getByLabel("Evaluation reason", { exact: true })).toContainText("Off");
  await expect(tester.getByLabel("Value", { exact: true })).toHaveText('"control"');
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the tester with a result has no serious violations (${theme})`, async ({ page }) => {
      const project = await seed();
      await useTheme(page, theme);
      await openBanner(page, project);
      await page.getByRole("button", { name: "Tester", exact: true }).click();
      const tester = testerOf(page);
      await choose(page, "Unit type (unit 1)", "user");
      await tester.getByLabel("Unit ID (unit 1)").fill("emp-1");
      await tester.getByRole("button", { name: "Evaluate", exact: true }).click();
      await expect(tester.getByLabel("Value", { exact: true })).toHaveText('"treatment"');
      await expectNoSeriousViolations(page, `tester (${theme})`);
    });
  }
});
