import { createSegment, getSegment, updateSegment } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, managementClient, must, seedProject, signIn } from "../support/console";
import { choose, seedFlag, writeConfiguration } from "../support/flags";
import { admin, seededProject, viewer } from "../support/instance";

/** Signs in and opens a project's segments. */
async function openSegments(page: Page, project: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${project}/segments`);
  await expect(page.getByRole("heading", { level: 1, name: "Segments" })).toBeVisible();
}

/** The list's row of one segment, by its key. */
const rowFor = (page: Page, key: string) =>
  page.getByRole("row").filter({ has: page.getByText(key, { exact: true }) });

/** A boolean flag whose rule in development targets a segment, through the management API. */
async function referenceSegment(project: string, flag: string, segment: string) {
  await seedFlag(project, { key: flag, type: "boolean", name: flag, lifetime: "permanent" });
  await writeConfiguration(project, flag, "development", () => ({
    rules: [
      {
        description: "",
        conditions: [{ kind: "in_segment", segment }],
        serve: { kind: "variation", variation: "true" },
      },
    ],
  }));
}

async function seedSegment(project: string, key: string, name: string) {
  return must(
    `creating ${key}`,
    await createSegment({
      client: managementClient(),
      path: { project },
      body: {
        key,
        name,
        conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "NL" }],
      },
    }),
  );
}

test.describe("segments", () => {
  test("builds a private segment from a country list and a semantic version, refusing a shorthand version", async ({
    page,
  }) => {
    const project = await seedProject("seg-build");
    await openSegments(page, project);
    await page.getByRole("link", { name: "New segment" }).first().click();
    await expect(page.getByRole("heading", { level: 1, name: "New segment" })).toBeVisible();

    await page.getByLabel("Key").fill("eu_countries");
    await page.getByLabel("Name").fill("EU countries");
    await page.getByRole("switch", { name: "Private" }).click();

    await page.getByRole("button", { name: "Add condition" }).click();
    await choose(page, "Attribute (condition 1)", "Country");
    await choose(page, "Operator (condition 1)", "is one of");
    // The select hands the focus back to itself once closed; typing waits
    // for that, so the value is typed where the member would type it.
    await expect(page.getByRole("combobox", { name: "Operator (condition 1)" })).toBeFocused();
    const countries = page.getByRole("textbox", { name: "Value (condition 1)" });
    await countries.fill("DE");
    await countries.press("Enter");
    await countries.fill("FR");
    await countries.press("Enter");
    await expect(page.getByRole("button", { name: "Remove DE" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Remove FR" })).toBeVisible();

    await page.getByRole("button", { name: "Add condition" }).click();
    await choose(page, "Attribute (condition 2)", "App version");
    await choose(page, "Operator (condition 2)", "is at least");
    await page.getByLabel("Value (condition 2)").fill("2.0");
    await page.getByRole("button", { name: "Create segment" }).click();
    await expect(page.getByText("Enter a full semantic version, such as 1.41.0.")).toBeVisible();
    await expect(page.getByRole("heading", { level: 1, name: "New segment" })).toBeVisible();

    await page.getByLabel("Value (condition 2)").fill("2.0.0");
    await page.getByRole("button", { name: "Create segment" }).click();
    await expect(page.getByRole("heading", { level: 1, name: "EU countries" })).toBeVisible();
    await expect(page.getByRole("main").getByText("eu_countries", { exact: true })).toBeVisible();

    await page
      .getByRole("navigation", { name: "Project" })
      .getByRole("link", { name: "Segments" })
      .click();
    const row = rowFor(page, "eu_countries");
    await expect(row.getByText("Private", { exact: true })).toBeVisible();
    await expect(row.getByRole("cell").nth(2)).toHaveText("2");
    const saved = must(
      "reading eu_countries",
      await getSegment({ client: managementClient(), path: { project, segment: "eu_countries" } }),
    );
    expect(saved.private).toBe(true);
    expect(saved.conditions).toEqual([
      { kind: "attribute", attribute: "country", op: "in", value: ["DE", "FR"] },
      { kind: "attribute", attribute: "app_version", op: "gte", value: "2.0.0" },
    ]);
  });

  test("shows the usage count and the flags using it before saving, and refuses its deletion", async ({
    page,
  }) => {
    const project = await seedProject("seg-usage");
    await openSegments(page, project);
    await page.getByRole("link", { name: "New segment" }).first().click();
    await page.getByLabel("Key").fill("employees");
    await page.getByLabel("Name").fill("Employees");
    await page.getByRole("button", { name: "Add condition" }).click();
    await choose(page, "Attribute (condition 1)", "User (unit ID)");
    await choose(page, "Operator (condition 1)", "equals");
    await page.getByLabel("Value (condition 1)").fill("qa-1");
    await page.getByRole("button", { name: "Create segment" }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Employees" })).toBeVisible();
    await expect(page.getByText("No flag's rules use this segment.")).toBeVisible();

    await page
      .getByRole("navigation", { name: "Project" })
      .getByRole("link", { name: "Segments" })
      .click();
    await expect(rowFor(page, "employees").getByRole("cell").nth(3)).toHaveText("0");

    await referenceSegment(project, "gated-checkout", "employees");
    await page.reload();
    await expect(rowFor(page, "employees").getByRole("cell").nth(3)).toHaveText("1");

    await rowFor(page, "employees").getByRole("link", { name: "Employees" }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Employees" })).toBeVisible();
    const usedBy = page.getByRole("region", { name: "Used by" });
    await expect(usedBy.getByRole("link", { name: "gated-checkout" })).toBeVisible();
    await expect(usedBy.getByText("in development")).toBeVisible();

    await page.getByLabel("Description").fill("Internal accounts only.");
    await page
      .getByRole("region", { name: "Unsaved changes" })
      .getByRole("button", { name: "Save" })
      .click();
    const confirm = page.getByRole("dialog", { name: "Save with usages?" });
    await expect(confirm.getByText("gated-checkout")).toBeVisible();
    await expect(confirm.getByText("in development")).toBeVisible();
    await confirm.getByRole("button", { name: "Save anyway" }).click();
    await expect(confirm).toBeHidden();
    await expect(page.getByText("The segment is saved.")).toBeVisible();
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toBeHidden();

    await page.getByRole("button", { name: "Delete", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Delete employees" });
    await dialog.getByRole("button", { name: "Delete segment" }).click();
    await expect(dialog.getByRole("alert")).toHaveText(
      "It is still in use by the flag gated-checkout in development.",
    );
  });

  test("deletes a segment no rule uses", async ({ page }) => {
    const project = await seedProject("seg-delete");
    await seedSegment(project, "beta", "Beta testers");
    await openSegments(page, project);
    await rowFor(page, "beta").getByRole("link", { name: "Beta testers" }).click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    await page
      .getByRole("dialog", { name: "Delete beta" })
      .getByRole("button", { name: "Delete segment" })
      .click();
    await expect(page.getByText("The segment beta is deleted.")).toBeVisible();
    await expect(page.getByRole("heading", { level: 1, name: "Segments" })).toBeVisible();
    await expect(page.getByText("This project has no segment yet.")).toBeVisible();
  });

  test("shows who saved meanwhile and keeps the member's edits over their change", async ({
    page,
  }) => {
    const project = await seedProject("seg-conflict");
    const seeded = await seedSegment(project, "beta", "Beta testers");
    await signIn(page);
    await page.goto(`/projects/${project}/segments/beta`);
    await page.getByLabel("Name").fill("Early adopters");

    must(
      "changing beta meanwhile",
      await updateSegment({
        client: managementClient(),
        path: { project, segment: "beta" },
        body: { conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "DE" }] },
        headers: { "If-Match": `"${seeded.version}"` },
      }),
    );
    await page
      .getByRole("region", { name: "Unsaved changes" })
      .getByRole("button", { name: "Save" })
      .click();
    const banner = page.getByRole("alert").filter({ hasText: "after you opened this" });
    await expect(banner).toContainText("saved a change on");
    await expect(banner.getByRole("list", { name: "Changes to the segment" })).toContainText(
      "Condition 1 changed from",
    );
    await banner.getByRole("button", { name: "Keep my edits" }).click();
    await expect(page.getByText("The segment is saved.")).toBeVisible();

    const saved = must(
      "reading beta",
      await getSegment({ client: managementClient(), path: { project, segment: "beta" } }),
    );
    expect(saved.name).toBe("Early adopters");
    expect(saved.conditions).toEqual([
      { kind: "attribute", attribute: "country", op: "eq", value: "DE" },
    ]);
  });

  test("lets a viewer read a segment and says why nothing can be changed", async ({ page }) => {
    const key = `viewer_segment_${Date.now().toString(36)}`;
    await seedSegment(seededProject.key, key, "Viewer segment");
    await openSegments(page, seededProject.key, viewer);
    await expect(page.getByRole("link", { name: "New segment" }).first()).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await rowFor(page, key).getByRole("link", { name: "Viewer segment" }).click();
    await expect(page.getByRole("status").filter({ hasText: "edit segments" })).toBeVisible();
    await expect(page.getByLabel("Name")).toBeDisabled();
    await expect(page.getByRole("combobox", { name: "Attribute (condition 1)" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Add condition" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Delete", exact: true })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
  });

  for (const theme of ["light", "dark"] as const) {
    test(`segments have no serious violation in the ${theme} theme`, async ({ page }) => {
      const project = await seedProject(`seg-axe-${theme}`);
      await seedSegment(project, "beta", "Beta testers");
      await useTheme(page, theme);
      await openSegments(page, project);
      await expectNoSeriousViolations(page, `segments list (${theme})`);
      await page.getByRole("link", { name: "New segment" }).first().click();
      await expect(page.getByRole("heading", { level: 1, name: "New segment" })).toBeVisible();
      await expectNoSeriousViolations(page, `new segment (${theme})`);
      await page.goto(`/projects/${project}/segments/beta`);
      await expect(page.getByRole("heading", { level: 1, name: "Beta testers" })).toBeVisible();
      await expectNoSeriousViolations(page, `segment editor (${theme})`);
    });
  }
});
