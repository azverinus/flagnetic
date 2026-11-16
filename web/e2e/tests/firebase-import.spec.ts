import { listFlags } from "@flagnetic/api";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { managementClient, must, type SignInWords, seedProject, signIn } from "../support/console";
import { readConfiguration, seedFlag, stringFlag } from "../support/flags";
import { admin, repoRoot } from "../support/instance";

/** A Remote Config template with grouped parameters, a percent condition and a condition the importer cannot reproduce. */
const template = readFileSync(
  resolve(repoRoot, "internal/domain/transfer/firebase/testdata/firebase-template.json"),
);

const russian: SignInWords = {
  email: "Электронная почта",
  password: "Пароль",
  submit: "Войти",
  next: "Продолжить",
};

const file = { name: "remoteconfig.template.json", mimeType: "application/json", buffer: template };

async function openPage(page: Page, project: string) {
  await page.goto(`/projects/${project}/import-export`);
  await expect(page.getByRole("heading", { name: "Import from Firebase", level: 2 })).toBeVisible();
}

async function flagKeys(project: string): Promise<string[]> {
  const list = must(
    "listing flags",
    await listFlags({ client: managementClient(), path: { project }, query: { limit: 100 } }),
  );
  return list.items.map((flag) => flag.key).sort();
}

/** Uploads the template and moves to the parameters step. */
async function upload(page: Page) {
  await page.getByLabel("Remote Config template").setInputFiles(file);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("table", { name: "The parameters of the template" })).toBeVisible();
}

/** Reaches the preview of the template into an environment, without legacy_limits. */
async function previewInto(page: Page, environment: string) {
  await upload(page);
  await page.getByLabel("Include legacy_limits").uncheck();
  await page.getByRole("button", { name: "Continue with 3 parameters" }).click();
  await page.getByRole("combobox", { name: "Environment" }).click();
  await page.getByRole("option", { name: new RegExp(environment, "i") }).click();
  await page.getByRole("button", { name: "Preview the import" }).click();
  await expect(page.getByRole("region", { name: "Flags and rules" })).toBeVisible();
}

test.describe("the Firebase wizard", () => {
  test("imports a template into production with one parameter deselected", async ({ page }) => {
    const project = await seedProject("firebase");
    await signIn(page);
    await openPage(page, project);

    await upload(page);

    // Parameters as flags, and how each condition maps.
    const parameters = page.getByRole("table", { name: "The parameters of the template" });
    await expect(parameters.getByRole("row", { name: /onboarding_steps/ })).toContainText(
      "Onboarding",
    );
    const mapping = page.getByRole("list", { name: "Condition mapping" });
    await expect(
      mapping.getByRole("listitem").filter({ hasText: "android_old" }).first(),
    ).toContainText("platform equals android");
    const skipped = page.getByRole("region", { name: "Conditions that are skipped" });
    await expect(skipped).toContainText("launch_day is skipped for new_matching");
    await expectNoSeriousViolations(page, "the Firebase wizard's parameters step");

    // The deselection survives a reload.
    await page.getByLabel("Include legacy_limits").uncheck();
    await page.reload();
    await expect(page.getByLabel("Include legacy_limits")).not.toBeChecked();
    await page.getByRole("button", { name: "Continue with 3 parameters" }).click();

    // The environment and the unit type.
    await expect(
      page.getByRole("combobox", { name: "Unit type for percent rollouts" }),
    ).toContainText("anonymous");
    await expectNoSeriousViolations(page, "the Firebase wizard's environment step");
    await page.getByRole("combobox", { name: "Environment" }).click();
    await page.getByRole("option", { name: /Production/ }).click();
    await page.getByRole("button", { name: "Preview the import" }).click();

    // The preview: the dry run's flags, attributes and warnings; nothing is written yet.
    const flags = page.getByRole("region", { name: "Flags and rules" });
    await expect(flags).toContainText("new_matching");
    await expect(flags).not.toContainText("legacy_limits");
    await expect(page.getByRole("region", { name: "Warnings" })).toContainText("launch_day");
    await expect(page.getByText(/without a change request: .*Production/)).toBeVisible();
    await expectNoSeriousViolations(page, "the Firebase wizard's preview");
    expect(await flagKeys(project)).toEqual([]);

    // Apply, and the summary links to the flags it created.
    await page.getByRole("button", { name: "Apply", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Imported into Production");
    for (const key of ["banner_text", "new_matching", "onboarding_steps"]) {
      await expect(page.getByRole("link", { name: key })).toHaveAttribute(
        "href",
        new RegExp(`/projects/${project}/flags/${key}`),
      );
    }
    await expect(page.getByRole("link", { name: "legacy_limits" })).toHaveCount(0);
    await expect(page.getByRole("region", { name: "Warnings" })).toContainText("launch_day");
    await expectNoSeriousViolations(page, "the Firebase wizard's summary");

    expect(await flagKeys(project)).toEqual(["banner_text", "new_matching", "onboarding_steps"]);
    const production = await readConfiguration(project, "new_matching", "production");
    expect(production.on).toBe(true);
    expect((await readConfiguration(project, "new_matching", "development")).on).toBe(false);

    await page.getByRole("link", { name: "new_matching" }).click();
    await expect(page.getByRole("heading", { name: "new_matching", level: 1 })).toBeVisible();
  });

  test("returns to the upload when the template was lost", async ({ page }) => {
    const project = await seedProject("firebaselost");
    await signIn(page);
    await openPage(page, project);
    await page.evaluate(
      (key) => window.sessionStorage.setItem(key, JSON.stringify({ step: "preview" })),
      `flagnetic.import-firebase.${project}`,
    );

    await page.reload();

    await expect(page.getByLabel("Remote Config template")).toBeVisible();
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
  });

  test("resumes on the preview after a reload", async ({ page }) => {
    const project = await seedProject("firebasereload");
    await signIn(page);
    await openPage(page, project);
    await previewInto(page, "Development");

    await page.reload();

    await expect(page.getByRole("region", { name: "Flags and rules" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeEnabled();
  });

  test("explains a project that moved after the dry run and applies after a new one", async ({
    page,
  }) => {
    const project = await seedProject("firebasemoved");
    await signIn(page);
    await openPage(page, project);
    await previewInto(page, "Development");

    // Someone else changes the project between the dry run and the apply.
    await seedFlag(project, stringFlag("another_flag"));
    await page.getByRole("button", { name: "Apply", exact: true }).click();

    const alert = page.getByRole("alert");
    await expect(alert).toContainText("The project changed after this check");
    expect(await flagKeys(project)).toEqual(["another_flag"]);

    await alert.getByRole("button", { name: "Check again" }).click();
    await page.getByRole("button", { name: "Apply", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Imported into Development");
    expect(await flagKeys(project)).toEqual([
      "another_flag",
      "banner_text",
      "new_matching",
      "onboarding_steps",
    ]);
  });

  test("shows the fault of a template the server refuses", async ({ page }) => {
    const project = await seedProject("firebasefault");
    await signIn(page);
    await openPage(page, project);

    await page.getByLabel("Remote Config template").setInputFiles({
      name: "broken.json",
      mimeType: "application/json",
      buffer: Buffer.from(
        JSON.stringify({ parameters: { broken: { valueType: "COLOUR", defaultValue: {} } } }),
      ),
    });
    await page.getByRole("button", { name: "Continue", exact: true }).click();

    await expect(page.getByRole("list", { name: "Faults in the document" })).toContainText(
      "/parameters/broken/valueType",
    );
    await expectNoSeriousViolations(page, "the Firebase wizard's refused template");
    await page.getByLabel("Include broken").uncheck();
    await expect(page.getByRole("button", { name: /^Continue with/ })).toBeDisabled();
  });

  test.describe("in Russian", () => {
    test.use({ locale: "ru-RU" });

    test("reads the steps and the preview in Russian", async ({ page }) => {
      const project = await seedProject("firebaseru");
      await signIn(page, admin, russian);
      await page.goto(`/projects/${project}/import-export`);

      await page.getByLabel("Шаблон Remote Config").setInputFiles(file);
      await page.getByRole("button", { name: "Продолжить", exact: true }).click();
      await expect(page.getByRole("table", { name: "Параметры шаблона" })).toBeVisible();
      await expect(page.getByRole("region", { name: "Пропущенные условия" })).toContainText(
        "пропущено для new_matching",
      );
      await expectNoSeriousViolations(page, "the Firebase wizard's parameters step in Russian");

      await page.getByRole("button", { name: /^Продолжить с 4 параметрами$/ }).click();
      await page.getByRole("button", { name: "Просмотреть импорт" }).click();
      await expect(page.getByRole("region", { name: "Флаги и правила" })).toBeVisible();
      await expectNoSeriousViolations(page, "the Firebase wizard's preview in Russian");
    });
  });
});
