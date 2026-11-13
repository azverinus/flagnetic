import { getProject } from "@flagnetic/api";

import { expect, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { managementClient, seedProject, signIn } from "../support/console";
import { seedFlag, stringFlag } from "../support/flags";
import { documentFile, exportedObject } from "../support/transfer";

test.describe("creating a project from a document", () => {
  test("creates a project from an export under a new key and opens it", async ({ page }) => {
    const source = await seedProject("source");
    await seedFlag(source, stringFlag("checkout_redesign"));
    const document = await exportedObject(source);
    const copy = `${source}-copy`;
    await signIn(page);
    await page.goto("/admin/projects");
    await expect(page.getByRole("heading", { name: "Create from a document" })).toBeVisible();
    await expectNoSeriousViolations(page, "the instance projects page");

    await page.getByLabel("Project document").setInputFiles(documentFile(document, "export.json"));
    // The key and name default to the document's.
    await expect(page.getByLabel("Key", { exact: true }).last()).toHaveValue(source);
    const section = page.getByRole("region", { name: "Create from a document" });
    await section.getByLabel("Key").fill(copy);
    await section.getByRole("button", { name: "Check what it would create" }).click();
    await expect(section.getByRole("region", { name: "Flags" })).toContainText("checkout_redesign");
    await expectNoSeriousViolations(page, "the dry run of a creation from a document");

    await section.getByRole("button", { name: "Create from this document" }).click();
    await expect(page).toHaveURL(new RegExp(`/projects/${copy}$`));
    const created = await getProject({ client: managementClient(), path: { project: copy } });
    expect(created.response?.status).toBe(200);
    const exported = await exportedObject(copy);
    expect(exported.flags.map((flag) => flag.key)).toEqual(["checkout_redesign"]);
  });

  test("explains a key the instance already has beside the key field", async ({ page }) => {
    const source = await seedProject("taken");
    const document = await exportedObject(source);
    await signIn(page);
    await page.goto("/admin/projects");

    const section = page.getByRole("region", { name: "Create from a document" });
    await section
      .getByLabel("Project document")
      .setInputFiles(documentFile(document, "export.json"));
    await section.getByRole("button", { name: "Check what it would create" }).click();

    await expect(
      section.getByText("This instance already has a project with that key."),
    ).toBeVisible();
    await expect(section.getByRole("button", { name: "Create from this document" })).toBeDisabled();
    await expectNoSeriousViolations(page, "the taken key of a creation from a document");
  });
});
