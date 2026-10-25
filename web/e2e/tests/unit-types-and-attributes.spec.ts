import { createAttribute, createSegment, createUnitType, deleteUnitType } from "@flagnetic/api";
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
import { seedFlag, stringFlag, writeConfiguration } from "../support/flags";
import { admin, seededProject, viewer } from "../support/instance";

async function openUnitTypes(page: Page, projectKey: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/unit-types`);
  await expect(page.getByRole("heading", { name: "Unit types", level: 1 })).toBeVisible();
}

async function openAttributes(page: Page, projectKey: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/attributes`);
  await expect(page.getByRole("heading", { name: "Attributes", level: 1 })).toBeVisible();
}

const rowFor = (page: Page, key: string) =>
  page.getByRole("row").filter({ has: page.getByRole("cell", { name: key, exact: true }) });

test.describe("unit types", () => {
  test("declares one, edits it with its key locked, and deletes it", async ({ page }) => {
    const projectKey = await seedProject("decl-crud");
    await openUnitTypes(page, projectKey);

    await page.getByRole("button", { name: "New unit type" }).click();
    const create = page.getByRole("dialog", { name: "New unit type" });
    await create.getByLabel("Key").fill("therapist");
    await create.getByLabel("Name").fill("Therapist");
    await create.getByLabel("Description").fill("Someone who gives therapy.");
    await create.getByRole("button", { name: "Create unit type" }).click();
    await expect(create).toBeHidden();

    const row = rowFor(page, "therapist");
    await expect(row).toContainText("Therapist");
    await expect(row).toContainText("Someone who gives therapy.");

    await row.getByRole("button", { name: "Edit therapist" }).click();
    const edit = page.getByRole("dialog", { name: "Edit therapist" });
    // The key is immutable, so it is shown disabled rather than editable.
    await expect(edit.getByLabel("Key")).toBeDisabled();
    await expect(edit.getByLabel("Key")).toHaveValue("therapist");
    await edit.getByLabel("Name").fill("Specialist");
    await edit.getByRole("button", { name: "Save" }).click();
    await expect(edit).toBeHidden();
    await expect(row).toContainText("Specialist");
    // Still the same row: the key never changed.
    await expect(row.getByRole("cell", { name: "therapist", exact: true })).toBeVisible();

    await row.getByRole("button", { name: "Delete therapist" }).click();
    const remove = page.getByRole("dialog", { name: "Delete therapist" });
    await remove.getByRole("button", { name: "Delete unit type" }).click();
    await expect(remove).toBeHidden();
    await expect(rowFor(page, "therapist")).toHaveCount(0);
  });

  test("refuses a key an attribute already has, on the field", async ({ page }) => {
    const projectKey = await seedProject("decl-shared");
    await openUnitTypes(page, projectKey);

    await page.getByRole("button", { name: "New unit type" }).click();
    const create = page.getByRole("dialog", { name: "New unit type" });
    // Unit types and attributes share one set of keys.
    await create.getByLabel("Key").fill("platform");
    await create.getByLabel("Name").fill("Platform");
    await create.getByRole("button", { name: "Create unit type" }).click();

    await expect(create.getByLabel("Key")).toHaveAccessibleDescription(
      /This project already has a unit type or an attribute with that key\./,
    );
  });

  test("shows the empty state with the primary action when a project has declared none", async ({
    page,
  }) => {
    const projectKey = await seedProject("decl-empty");
    const client = managementClient();
    for (const key of ["user", "anonymous"]) {
      must(
        `deleting ${key}`,
        await deleteUnitType({
          client,
          path: { project: projectKey, unit_type: key },
          headers: { "If-Match": '"1"' },
        }),
      );
    }

    await openUnitTypes(page, projectKey);
    await expect(page.getByText("This project has no unit type yet.")).toBeVisible();
    await expect(page.getByRole("button", { name: "New unit type" })).toHaveCount(2);
  });
});

test.describe("attributes", () => {
  test("declares one with its type, which then stays fixed", async ({ page }) => {
    const projectKey = await seedProject("decl-attr");
    await openAttributes(page, projectKey);

    await page.getByRole("button", { name: "New attribute" }).click();
    const create = page.getByRole("dialog", { name: "New attribute" });
    await create.getByLabel("Key").fill("plan");
    await create.getByLabel("Type").click();
    await page.getByRole("option", { name: "Number" }).click();
    await create.getByLabel("Name").fill("Plan");
    await create.getByRole("button", { name: "Create attribute" }).click();
    await expect(create).toBeHidden();

    const plan = rowFor(page, "plan");
    await expect(plan).toContainText("Number");
    await plan.getByRole("button", { name: "Edit plan" }).click();
    const edit = page.getByRole("dialog", { name: "Edit plan" });
    await expect(edit.getByLabel("Type")).toBeDisabled();
    await expect(edit.getByLabel("Type")).toHaveText("Number");
    await page.keyboard.press("Escape");

    await plan.getByRole("button", { name: "Delete plan" }).click();
    await page
      .getByRole("dialog", { name: "Delete plan" })
      .getByRole("button", { name: "Delete attribute" })
      .click();
    await expect(rowFor(page, "plan")).toHaveCount(0);
  });

  test("refuses a type left unchosen before asking the server", async ({ page }) => {
    const projectKey = await seedProject("decl-notype");
    await openAttributes(page, projectKey);

    await page.getByRole("button", { name: "New attribute" }).click();
    const create = page.getByRole("dialog", { name: "New attribute" });
    await create.getByLabel("Key").fill("plan");
    await create.getByLabel("Name").fill("Plan");
    await create.getByRole("button", { name: "Create attribute" }).click();

    await expect(create.getByLabel("Type")).toHaveAccessibleDescription("Choose a type.");
    await expect(create.getByLabel("Type")).toBeFocused();
  });

  test("offers no delete or retype on a standard attribute", async ({ page }) => {
    const projectKey = await seedProject("decl-standard");
    await openAttributes(page, projectKey);

    const platform = rowFor(page, "platform");
    await expect(platform).toContainText("Standard");
    await expect(platform.getByRole("button", { name: "Delete platform" })).toHaveCount(0);

    // Its name and description still edit; its type does not.
    await platform.getByRole("button", { name: "Edit platform" }).click();
    const edit = page.getByRole("dialog", { name: "Edit platform" });
    await expect(edit.getByLabel("Type")).toBeDisabled();
    await expect(edit.getByLabel("Type")).toHaveText("String");
    await edit.getByLabel("Name").fill("Platform name");
    await edit.getByRole("button", { name: "Save" }).click();
    await expect(edit).toBeHidden();
    await expect(platform).toContainText("Platform name");
  });
});

test.describe("what names a declaration", () => {
  test("counts the flags and segments naming an attribute and lists them when its deletion is refused", async ({
    page,
  }) => {
    const projectKey = await seedProject("decl-attr-used");
    const client = managementClient();
    must(
      "declaring plan",
      await createAttribute({
        client,
        path: { project: projectKey },
        body: { key: "plan", type: "string", name: "Plan" },
      }),
    );
    must(
      "declaring tier",
      await createAttribute({
        client,
        path: { project: projectKey },
        body: { key: "tier", type: "string", name: "Tier" },
      }),
    );
    const plan = [
      { kind: "attribute" as const, attribute: "plan", op: "eq" as const, value: "pro" },
    ];
    must(
      "creating paying",
      await createSegment({
        client,
        path: { project: projectKey },
        body: { key: "paying", name: "Paying", conditions: plan },
      }),
    );
    await seedFlag(projectKey, stringFlag("upsell"));
    await writeConfiguration(projectKey, "upsell", "development", () => ({
      rules: [
        {
          description: "Pro",
          conditions: plan,
          serve: { kind: "variation", variation: "treatment" },
        },
      ],
    }));
    await openAttributes(page, projectKey);

    const used = rowFor(page, "plan");
    await expect(used.getByRole("cell", { name: "2", exact: true })).toBeVisible();
    await expect(rowFor(page, "tier").getByRole("cell", { name: "0", exact: true })).toBeVisible();
    await expectNoSeriousViolations(page, "the attributes with their usage counts");

    await used.getByRole("button", { name: "Delete plan" }).click();
    const remove = page.getByRole("dialog", { name: "Delete plan" });
    await remove.getByRole("button", { name: "Delete attribute" }).click();
    const refusal = remove.getByRole("alert");
    await expect(refusal).toContainText("It is still in use by");
    await expect(refusal).toContainText("the flag upsell in development");
    await expect(refusal).toContainText("the segment paying");
    await page.keyboard.press("Escape");
    await expect(rowFor(page, "plan")).toBeVisible();
  });

  test("counts the flags naming a unit type in an override and lists them when its deletion is refused", async ({
    page,
  }) => {
    const projectKey = await seedProject("decl-unit-used");
    must(
      "declaring account",
      await createUnitType({
        client: managementClient(),
        path: { project: projectKey },
        body: { key: "account", name: "Account" },
      }),
    );
    await seedFlag(projectKey, stringFlag("upsell"));
    await unprotectProduction(projectKey);
    await writeConfiguration(projectKey, "upsell", "production", () => ({
      overrides: [
        { unit_type: "account", unit_id: "acme", variation: "treatment", note: "Pilot customer" },
      ],
    }));
    await openUnitTypes(page, projectKey);

    const account = rowFor(page, "account");
    await expect(account.getByRole("cell", { name: "1", exact: true })).toBeVisible();
    await expectNoSeriousViolations(page, "the unit types with their usage counts");
    await account.getByRole("button", { name: "Delete account" }).click();
    const remove = page.getByRole("dialog", { name: "Delete account" });
    await remove.getByRole("button", { name: "Delete unit type" }).click();
    await expect(remove.getByRole("alert")).toHaveText(
      "It is still in use by the flag upsell in production.",
    );
  });
});

test.describe("what a role may do with declarations", () => {
  test("shows a viewer both lists and no way to write to either", async ({ page }) => {
    await openUnitTypes(page, seededProject.key, viewer);
    await expect(rowFor(page, "user")).toBeVisible();
    await expect(page.getByRole("button", { name: "New unit type" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await expect(page.getByRole("button", { name: /^Edit/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /^Delete/ })).toHaveCount(0);
    await expect(page.getByRole("columnheader", { name: "Actions" })).toHaveCount(0);

    await page.goto(`/projects/${seededProject.key}/attributes`);
    await expect(rowFor(page, "platform")).toBeVisible();
    const create = page.getByRole("button", { name: "New attribute" });
    await expect(create).toHaveAttribute("aria-disabled", "true");
    await expect(create).toHaveAccessibleDescription(
      "Needs the permission to manage unit types and attributes, held by Owner and Maintainer.",
    );
    await expect(page.getByRole("button", { name: /^Edit/ })).toHaveCount(0);
    await expect(page.getByRole("columnheader", { name: "Actions" })).toHaveCount(0);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`unit types has no serious violation in the ${theme} theme`, async ({ page }) => {
      const projectKey = await seedProject(`decl-axe-ut-${theme}`);
      await useTheme(page, theme);
      await openUnitTypes(page, projectKey);
      await expect(rowFor(page, "user")).toBeVisible();
      await expectNoSeriousViolations(page, `unit types (${theme})`);

      await page.getByRole("button", { name: "New unit type" }).click();
      await expect(page.getByRole("dialog", { name: "New unit type" })).toBeVisible();
      await expectNoSeriousViolations(page, `the new unit type dialog (${theme})`);
    });

    test(`attributes has no serious violation in the ${theme} theme`, async ({ page }) => {
      const projectKey = await seedProject(`decl-axe-attr-${theme}`);
      await useTheme(page, theme);
      await openAttributes(page, projectKey);
      await expect(rowFor(page, "platform")).toBeVisible();
      await expectNoSeriousViolations(page, `attributes (${theme})`);

      await page.getByRole("button", { name: "New attribute" }).click();
      const create = page.getByRole("dialog", { name: "New attribute" });
      await create.getByLabel("Type").click();
      await expect(page.getByRole("option", { name: "Semantic version" })).toBeVisible();
      await expectNoSeriousViolations(page, `the new attribute dialog with its types (${theme})`);
    });
  }

  test("returns focus to what opened a dialog after it closes", async ({ page }) => {
    const projectKey = await seedProject("decl-focus");
    await openUnitTypes(page, projectKey);

    const remove = rowFor(page, "user").getByRole("button", { name: "Delete user" });
    await remove.click();
    await expect(page.getByRole("dialog", { name: "Delete user" })).toBeVisible();

    await page.keyboard.press("Escape");

    await expect(page.getByRole("dialog", { name: "Delete user" })).toHaveCount(0);
    await expect(remove).toBeFocused();
  });
});
