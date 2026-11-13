import { createGrant, createSdkKey, exportProject } from "@flagnetic/api";
import { readFile } from "node:fs/promises";

import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import {
  managementClient,
  must,
  type SignInWords,
  seedMember,
  seedProject,
  signIn,
} from "../support/console";
import { admin } from "../support/instance";
import { readConfiguration, seedFlag, stringFlag } from "../support/flags";
import {
  documentFile,
  documentWithFlagToggled,
  exportedDocument,
  exportedObject,
} from "../support/transfer";

/** The sign-in page as it reads in Russian. */
const russian: SignInWords = {
  email: "Электронная почта",
  password: "Пароль",
  submit: "Войти",
  next: "Продолжить",
};

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

/** Chooses a file and asks for the dry run. */
async function check(page: Page, file: { name: string; mimeType: string; buffer: Buffer }) {
  await page.getByLabel("Project document").setInputFiles(file);
  await page.getByRole("button", { name: "Check what it would change" }).click();
}

test.describe("importing a project document", () => {
  test("re-importing an export shows everything unchanged, survives a reload and cannot be applied", async ({
    page,
  }) => {
    const project = await seedProject("reimport");
    await seedFlag(project, stringFlag("checkout_redesign"));
    await signIn(page);
    await openPage(page, project);

    const document = await exportedDocument(project);
    await check(page, {
      name: `${project}.json`,
      mimeType: "application/json",
      buffer: Buffer.from(JSON.stringify(document)),
    });
    await expect(
      page.getByText("Unchanged. The project already matches this document."),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled();
    await expectNoSeriousViolations(page, "the import review with nothing to apply");

    await page.reload();
    await expect(page.getByText(`Document: ${project}.json`)).toBeVisible();
    await expect(
      page.getByText("Unchanged. The project already matches this document."),
    ).toBeVisible();

    await page.getByRole("button", { name: "Choose another file" }).click();
    await expect(page.getByLabel("Project document")).toBeVisible();
  });

  test("returns to the upload when the document was lost", async ({ page }) => {
    const project = await seedProject("lostdoc");
    await signIn(page);
    await openPage(page, project);
    await page.evaluate(
      (key) => window.sessionStorage.setItem(key, JSON.stringify({ step: "review" })),
      `flagnetic.import.${project}`,
    );

    await page.reload();

    await expect(page.getByLabel("Project document")).toBeVisible();
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
  });

  test("shows the changes of an edited document, names the protected environment and applies them", async ({
    page,
  }) => {
    const project = await seedProject("mergeapply");
    await seedFlag(project, stringFlag("checkout_redesign"));
    await signIn(page);
    await openPage(page, project);
    const before = await readConfiguration(project, "checkout_redesign", "production");

    await check(page, await documentWithFlagToggled(project, "checkout_redesign", "production"));

    const flags = page.getByRole("region", { name: "Flags" });
    const patch = flags.getByRole("list", { name: "Changes to checkout_redesign" });
    await expect(patch).toContainText("/configurations/production/on");
    await expect(page.getByText(/without a change request: .*Production/)).toBeVisible();
    await expectNoSeriousViolations(page, "the import review with changes");

    await page.getByRole("button", { name: "Apply", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Imported.");
    const after = await readConfiguration(project, "checkout_redesign", "production");
    expect(after.on).toBe(!before.on);
  });

  test("explains a project that moved after the dry run and applies after a new one", async ({
    page,
  }) => {
    const project = await seedProject("moved");
    await seedFlag(project, stringFlag("checkout_redesign"));
    await signIn(page);
    await openPage(page, project);
    const before = await readConfiguration(project, "checkout_redesign", "production");
    await check(page, await documentWithFlagToggled(project, "checkout_redesign", "production"));
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeEnabled();

    // Someone else changes the project between the dry run and the apply.
    await seedFlag(project, stringFlag("another_flag"));
    await page.getByRole("button", { name: "Apply", exact: true }).click();

    const alert = page.getByRole("alert");
    await expect(alert).toContainText("The project changed after this check");
    expect((await readConfiguration(project, "checkout_redesign", "production")).on).toBe(
      before.on,
    );

    await alert.getByRole("button", { name: "Check again" }).click();
    await page.getByRole("button", { name: "Apply", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Imported.");
    expect((await readConfiguration(project, "checkout_redesign", "production")).on).toBe(
      !before.on,
    );
  });

  test("shows the pointer of each fault in a document the server refuses", async ({ page }) => {
    const project = await seedProject("refused");
    await signIn(page);
    await openPage(page, project);

    await check(page, {
      name: "broken.json",
      mimeType: "application/json",
      buffer: Buffer.from(JSON.stringify({ schema_version: 1, mystery: true })),
    });

    await expect(page.getByRole("alert")).toBeVisible();
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
    await expectNoSeriousViolations(page, "the import review of a refused document");
  });
});

/** Switches the import to replace mode. */
async function chooseReplace(page: Page) {
  await page.getByRole("combobox", { name: "Mode" }).click();
  await page.getByRole("option", { name: "Replace" }).click();
}

test.describe("replacing a project's configuration with a document", () => {
  test("shows what a replace deletes, emphasised, and deletes it on apply", async ({ page }) => {
    const project = await seedProject("replace");
    await seedFlag(project, stringFlag("checkout_redesign"));
    await seedFlag(project, stringFlag("legacy_banner"));
    await signIn(page);
    await openPage(page, project);
    const document = await exportedObject(project);
    document.flags = document.flags.filter((flag) => flag.key !== "legacy_banner");

    await chooseReplace(page);
    await check(page, documentFile(document, `${project}.json`));

    await expect(page.getByRole("alert")).toContainText(
      "This import deletes 1 entity from the project",
    );
    const flags = page.getByRole("region", { name: "Flags" });
    await expect(flags).toContainText("Deleted");
    await expect(flags).toContainText("legacy_banner");
    await expectNoSeriousViolations(page, "the replace review with a deletion");

    await page.getByRole("button", { name: "Apply", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Imported.");
    const gone = await exportedObject(project);
    expect(gone.flags.map((flag) => flag.key)).toEqual(["checkout_redesign"]);
  });

  test("refuses a replace that would delete what is in use and lists the references", async ({
    page,
  }) => {
    const project = await seedProject("replaceuse");
    must(
      "creating an SDK key",
      await createSdkKey({
        client: managementClient(),
        path: { project, environment: "staging" },
        body: { kind: "server", name: "Staging service" },
      }),
    );
    await signIn(page);
    await openPage(page, project);
    const document = await exportedObject(project);
    document.environments = document.environments.filter((env) => env.key !== "staging");
    for (const flag of document.flags) delete flag.configurations.staging;

    await chooseReplace(page);
    await check(page, documentFile(document, `${project}.json`));

    const references = page.getByRole("list", { name: "Still in use by" });
    await expect(references).toContainText("the SDK key");
    await expect(references).toContainText("in staging");
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
    await expectNoSeriousViolations(page, "the replace review with an in_use refusal");
  });

  test.describe("in Russian", () => {
    test.use({ locale: "ru-RU" });

    test("reads the mode and the deletions in Russian", async ({ page }) => {
      const project = await seedProject("replaceru");
      await seedFlag(project, stringFlag("legacy_banner"));
      await signIn(page, admin, russian);
      await page.goto(`/projects/${project}/import-export`);
      const document = await exportedObject(project);
      document.flags = [];

      await page.getByRole("combobox", { name: "Режим" }).click();
      await page.getByRole("option", { name: "Замена" }).click();
      await page.getByLabel("Документ проекта").setInputFiles(documentFile(document, "p.json"));
      await page.getByRole("button", { name: "Проверить, что изменится" }).click();

      await expect(page.getByRole("alert")).toContainText("Этот импорт удалит 1 объект");
      await expectNoSeriousViolations(page, "the replace review in Russian");
    });
  });
});
