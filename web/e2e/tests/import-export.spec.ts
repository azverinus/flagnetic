import { createGrant, exportProject } from "@flagnetic/api";
import { readFile } from "node:fs/promises";

import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedMember, seedProject, signIn } from "../support/console";

const password = "harness-quince-ferry-5521";

async function openPage(page: Page, project: string) {
  await page.goto(`/projects/${project}/import-export`);
  await expect(page.getByRole("heading", { name: "Import and export", level: 1 })).toBeVisible();
}

/** Exports in the format chosen and returns the file as saved. */
async function exportAs(page: Page, format: "JSON" | "YAML") {
  await page.getByRole("combobox", { name: "Document format" }).click();
  await page.getByRole("option", { name: format }).click();
  const downloading = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export" }).click();
  const download = await downloading;
  return {
    name: download.suggestedFilename(),
    text: await readFile((await download.path())!, "utf8"),
  };
}

test.describe("import and export page", () => {
  test("an owner exports the project document as served, in both formats", async ({ page }) => {
    const project = await seedProject("transfer");
    await signIn(page);
    await openPage(page, project);
    await expectNoSeriousViolations(page, "the import and export page");

    const json = await exportAs(page, "JSON");
    expect(json.name).toMatch(new RegExp(`^${project}-\\d{8}T\\d{6}Z\\.json$`));
    const served = must(
      "exporting",
      await exportProject({
        client: managementClient(),
        path: { project },
        query: { format: "json" },
        parseAs: "text",
      }),
    ) as unknown as string;
    expect(json.text).toBe(served);

    const yaml = await exportAs(page, "YAML");
    expect(yaml.name).toMatch(new RegExp(`^${project}-\\d{8}T\\d{6}Z\\.yaml$`));
    expect(yaml.text).toContain("schema_version");
  });

  test("lists the import sections to a holder of the permission only", async ({
    page,
    browser,
  }) => {
    const project = await seedProject("transfer");
    const viewer = await seedMember("transfer-viewer", "Vera Reader", password);
    must(
      "granting viewer",
      await createGrant({
        client: managementClient(),
        path: { project },
        body: { member: viewer.id, role: "viewer" },
      }),
    );
    const owner = await seedMember("transfer-owner", "Olga Owner", password);
    must(
      "granting owner",
      await createGrant({
        client: managementClient(),
        path: { project },
        body: { member: owner.id, role: "owner" },
      }),
    );

    await signIn(page, viewer);
    await openPage(page, project);
    await expect(page.getByRole("heading", { name: "Export", level: 2 })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Import", level: 2, exact: true })).toHaveCount(
      0,
    );
    await expect(page.getByRole("heading", { name: "Import from Firebase" })).toHaveCount(0);

    const other = await (await browser.newContext()).newPage();
    await signIn(other, owner);
    await openPage(other, project);
    await expect(
      other.getByRole("heading", { name: "Import", level: 2, exact: true }),
    ).toBeVisible();
    await expect(other.getByRole("heading", { name: "Import from Firebase" })).toBeVisible();
    await expectNoSeriousViolations(other, "the import and export page of an owner");
  });

  test("is accessible in the dark theme and does not scroll sideways at 768 px", async ({
    page,
  }) => {
    const project = await seedProject("transfer");
    await signIn(page);
    await page.setViewportSize({ width: 768, height: 900 });
    await openPage(page, project);
    await useTheme(page, "dark");
    await expectNoSeriousViolations(page, "the import and export page in the dark theme");
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });
});
