import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, seedProject, signIn } from "../support/console";
import { admin, seededProject, viewer } from "../support/instance";
import { sdkRequest, seedSdkKey } from "../support/sdk";

/** Sends one context to remote evaluation, as an app would, so its undeclared attributes are discovered. */
async function evaluate(key: string, attributes: Record<string, unknown>): Promise<void> {
  await sdkRequest(key, "/evaluate", {
    body: { context: { units: { user: "u-1" }, attributes } },
  });
}

/** Seeds a project whose evaluated contexts carried `plan` as a string and `level` as a number, then a string. */
async function seedDiscovered(prefix: string): Promise<string> {
  const project = await seedProject(prefix);
  await seedDiscoveredIn(project);
  return project;
}

async function seedDiscoveredIn(project: string): Promise<void> {
  const client = await seedSdkKey(project, "staging", { kind: "client", name: "storefront" });
  await evaluate(client, { plan: "pro", level: 3 });
  await evaluate(client, { plan: "free", level: "gold" });
}

/** Opens the Discovered tab of a project's attributes, once the recorder has written. */
async function openDiscovered(page: Page, project: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${project}/attributes`);
  await page.getByRole("tab", { name: "Discovered", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Discovered", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  // Recording is asynchronous, within about a second of each evaluation, and
  // the two seeded evaluations may be written in different flushes: the page
  // is read again until the second one, which saw `level` as a string, shows.
  const level = page.getByRole("row", { name: /^level/ });
  await expect(async () => {
    await page.reload();
    await expect(level).toContainText("String", { timeout: 1_000 });
  }).toPass({ timeout: 20_000, intervals: [1_000] });
}

test.describe("the discovered attributes", () => {
  test("list what SDKs sent that the project has not declared, with the types seen and how often", async ({
    page,
  }) => {
    const project = await seedDiscovered("discovered-list");
    await openDiscovered(page, project);

    const plan = page.getByRole("row", { name: /^plan/ });
    await expect(plan).toContainText("String");
    // Times seen: both evaluations carried `plan`.
    await expect(plan.getByRole("cell").nth(4)).toHaveText("2");
    const level = page.getByRole("row", { name: /^level/ });
    await expect(level).toContainText("Number, String");
    // Declared attributes, such as the standard ones, are not discovered.
    await expect(page.getByRole("row", { name: /^app_version/ })).toHaveCount(0);
  });

  test("declares an attribute seen with one type in one step, and lists it as declared", async ({
    page,
  }) => {
    const project = await seedDiscovered("discovered-declare");
    await openDiscovered(page, project);

    await page.getByRole("button", { name: "Declare plan" }).click();
    const dialog = page.getByRole("dialog", { name: "Declare plan" });
    // The only type seen is chosen already.
    await expect(dialog.getByRole("combobox", { name: "Type" })).toHaveText("String");
    await dialog.getByRole("button", { name: "Declare attribute" }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("row", { name: /^plan/ })).toHaveCount(0);

    await page.getByRole("tab", { name: "Declared", exact: true }).click();
    const declared = page.getByRole("row", { name: /^plan/ });
    await expect(declared).toBeVisible();
    await expect(declared).toContainText("String");
  });

  test("makes the member choose between the types seen, and no other", async ({ page }) => {
    const project = await seedDiscovered("discovered-choose");
    await openDiscovered(page, project);

    await page.getByRole("button", { name: "Declare level" }).click();
    const dialog = page.getByRole("dialog", { name: "Declare level" });
    await dialog.getByRole("button", { name: "Declare attribute" }).click();
    await expect(dialog.getByText("Choose a type.")).toBeVisible();

    await dialog.getByRole("combobox", { name: "Type" }).click();
    await expect(page.getByRole("option")).toHaveText(["Number", "String"]);
    await page.getByRole("option", { name: "Number" }).click();
    await dialog.getByRole("button", { name: "Declare attribute" }).click();
    await expect(dialog).toBeHidden();

    await page.getByRole("tab", { name: "Declared", exact: true }).click();
    await expect(page.getByRole("row", { name: /^level/ })).toContainText("Number");
  });

  test("dismisses an attribute after a confirmation, and it stays out of both lists", async ({
    page,
  }) => {
    const project = await seedDiscovered("discovered-dismiss");
    await openDiscovered(page, project);

    await page.getByRole("button", { name: "Dismiss plan" }).click();
    const dialog = page.getByRole("dialog", { name: "Dismiss plan" });
    await expect(dialog).toContainText("comes back if SDKs send it with a type not seen before");
    await dialog.getByRole("button", { name: "Dismiss attribute" }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("row", { name: /^plan/ })).toHaveCount(0);
    await expect(page.getByRole("row", { name: /^level/ })).toBeVisible();

    await page.reload();
    await page.getByRole("tab", { name: "Discovered", exact: true }).click();
    await expect(page.getByRole("row", { name: /^level/ })).toBeVisible();
    await expect(page.getByRole("row", { name: /^plan/ })).toHaveCount(0);
    await page.getByRole("tab", { name: "Declared", exact: true }).click();
    await expect(page.getByRole("row", { name: /^plan/ })).toHaveCount(0);
  });

  test("says when nothing undeclared has been seen", async ({ page }) => {
    const project = await seedProject("discovered-empty");
    await signIn(page, admin);
    await page.goto(`/projects/${project}/attributes?tab=discovered`);
    await expect(page.getByText("No undeclared attribute has been seen.")).toBeVisible();
  });
});

test.describe("what a role may do with discovered attributes", () => {
  test("shows a viewer the list and no way to declare or dismiss", async ({ page }) => {
    await seedDiscoveredIn(seededProject.key);
    await openDiscovered(page, seededProject.key, viewer);

    await expect(page.getByRole("row", { name: /^plan/ })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Actions" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /^(Declare|Dismiss) / })).toHaveCount(0);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the Discovered tab and its dialogs have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      const project = await seedDiscovered(`discovered-axe-${theme}`);
      await useTheme(page, theme);
      await openDiscovered(page, project);
      await expectNoSeriousViolations(page, `the Discovered tab (${theme})`);

      await page.getByRole("button", { name: "Declare level" }).click();
      await expect(page.getByRole("dialog", { name: "Declare level" })).toBeVisible();
      await expectNoSeriousViolations(page, `the declare dialog (${theme})`);
      await page.keyboard.press("Escape");

      await page.getByRole("button", { name: "Dismiss plan" }).click();
      await expect(page.getByRole("dialog", { name: "Dismiss plan" })).toBeVisible();
      await expectNoSeriousViolations(page, `the dismiss dialog (${theme})`);
    });
  }
});
