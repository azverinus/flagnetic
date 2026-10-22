import { updateFlag } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedProject } from "../support/console";
import {
  choose,
  openFlag,
  readFlag,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";
import { seededProject, viewer } from "../support/instance";

const sheet = (page: Page) => page.getByRole("dialog", { name: "Edit definition" });

test.describe("the flag's definition", () => {
  test("edits the name, a variation and the tags from the definition sheet", async ({ page }) => {
    const project = await seedProject("flag-definition");
    await seedFlag(project, stringFlag("checkout_variant", { name: "Checkout variant" }));
    await openFlag(page, project, "checkout_variant");
    await expect(page.getByRole("heading", { name: "Checkout variant", level: 1 })).toBeVisible();

    await page.getByRole("button", { name: "Edit definition" }).click();
    await sheet(page).getByLabel("Name", { exact: true }).first().fill("Checkout experiment");
    const second = sheet(page).getByRole("group", { name: "Variation 2" });
    await second.getByLabel("Value").fill("treatment-b");
    await sheet(page).getByLabel("Tags", { exact: true }).fill("checkout");
    await sheet(page).getByLabel("Tags", { exact: true }).press("Enter");
    await sheet(page).getByRole("button", { name: "Save definition" }).click();

    await expect(sheet(page)).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: "Checkout experiment", level: 1 }),
    ).toBeVisible();
    const flag = await readFlag(project, "checkout_variant");
    expect(flag.name).toBe("Checkout experiment");
    expect(flag.tags).toEqual(["checkout"]);
    expect(flag.variations.map((v) => v.value)).toEqual(["control", "treatment-b"]);
  });

  test("shows who changed the definition meanwhile, with their change, and keeps the member's edits", async ({
    page,
  }) => {
    const project = await seedProject("flag-definition-conflict");
    await seedFlag(project, stringFlag("checkout_variant", { name: "Checkout variant" }));
    await openFlag(page, project, "checkout_variant");
    await page.getByRole("button", { name: "Edit definition" }).click();
    await sheet(page)
      .getByLabel("Description", { exact: true })
      .first()
      .fill("Which checkout a visitor sees.");

    const before = await readFlag(project, "checkout_variant");
    must(
      "renaming through the API",
      await updateFlag({
        client: managementClient(),
        path: { project, flag: "checkout_variant" },
        body: { name: "Renamed meanwhile" },
        headers: { "If-Match": `"${before.version}"` },
      }),
    );
    await sheet(page).getByRole("button", { name: "Save definition" }).click();

    const banner = sheet(page).getByRole("alert");
    // The suite's seeding token made the change, so the entry names it.
    await expect(banner).toContainText("bootstrap saved a change on");
    await expect(banner).toContainText(
      "Name changed from “Checkout variant” to “Renamed meanwhile”.",
    );
    await banner.getByRole("button", { name: "Keep my edits" }).click();

    await expect(sheet(page)).toHaveCount(0);
    const flag = await readFlag(project, "checkout_variant");
    expect(flag.name).toBe("Renamed meanwhile");
    expect(flag.description).toBe("Which checkout a visitor sees.");
  });

  test("reloads the definition, losing the member's edits, on request", async ({ page }) => {
    const project = await seedProject("flag-definition-reload");
    await seedFlag(project, stringFlag("checkout_variant", { name: "Checkout variant" }));
    await openFlag(page, project, "checkout_variant");
    await page.getByRole("button", { name: "Edit definition" }).click();
    await sheet(page).getByLabel("Name", { exact: true }).first().fill("Mine");

    const before = await readFlag(project, "checkout_variant");
    must(
      "renaming through the API",
      await updateFlag({
        client: managementClient(),
        path: { project, flag: "checkout_variant" },
        body: { name: "Theirs" },
        headers: { "If-Match": `"${before.version}"` },
      }),
    );
    await sheet(page).getByRole("button", { name: "Save definition" }).click();
    await sheet(page).getByRole("button", { name: "Reload and lose my edits" }).click();

    await expect(sheet(page).getByRole("alert")).toHaveCount(0);
    await expect(sheet(page).getByLabel("Name", { exact: true }).first()).toHaveValue("Theirs");
    expect((await readFlag(project, "checkout_variant")).name).toBe("Theirs");
  });
});

test.describe("archiving and deleting", () => {
  test("archives, restores, archives again and deletes a flag, typing its key", async ({
    page,
  }) => {
    const project = await seedProject("flag-archive");
    await seedFlag(project, stringFlag("checkout_variant"));
    await openFlag(page, project, "checkout_variant");

    await page.getByRole("button", { name: "Archive", exact: true }).click();
    await page.getByRole("button", { name: "Archive flag" }).click();
    await expect(page.getByText("Archived", { exact: true })).toBeVisible();
    await expect(
      page.getByText("The flag is archived. Restore it to change its configurations."),
    ).toBeVisible();

    await page.getByRole("button", { name: "Restore", exact: true }).click();
    await page.getByRole("button", { name: "Restore flag" }).click();
    await expect(page.getByText("Archived", { exact: true })).toHaveCount(0);

    await page.getByRole("button", { name: "Archive", exact: true }).click();
    await page.getByRole("button", { name: "Archive flag" }).click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    const confirm = page.getByRole("button", { name: "Delete flag" });
    await expect(confirm).toBeDisabled();
    await page.getByLabel("Type checkout_variant to confirm").fill("checkout_variant");
    await confirm.click();

    await expect(page).toHaveURL(new RegExp(`/projects/${project}/flags$`));
    await expect(page.getByText("The flag checkout_variant is deleted.")).toBeVisible();
  });

  test("refuses to archive a flag another flag requires, naming it", async ({ page }) => {
    const project = await seedProject("flag-archive-refused");
    await seedFlag(project, stringFlag("payments_v2"));
    await seedFlag(project, stringFlag("checkout_variant"));
    await writeConfiguration(project, "checkout_variant", "staging", () => ({
      prerequisites: [{ flag: "payments_v2", variation: "treatment" }],
    }));
    await openFlag(page, project, "payments_v2");

    await page.getByRole("button", { name: "Archive", exact: true }).click();
    await page.getByRole("button", { name: "Archive flag" }).click();
    await expect(page.getByRole("dialog").getByRole("alert")).toContainText(
      "It is still in use by the flag checkout_variant in staging.",
    );
    expect((await readFlag(project, "payments_v2")).archived_at).toBeNull();
  });
});

test.describe("a member who may only read", () => {
  test("sees every write control disabled, with why", async ({ page }) => {
    const key = `viewer_detail_${Date.now().toString(36)}`;
    await seedFlag(seededProject.key, stringFlag(key));
    await openFlag(page, seededProject.key, key, { who: viewer });

    const edit = page.getByRole("button", { name: "Edit definition" });
    await expect(edit).toHaveAttribute("aria-disabled", "true");
    await expect(edit).toHaveAccessibleDescription(/Needs the permission to edit flags/);
    const archive = page.getByRole("button", { name: "Archive", exact: true });
    await expect(archive).toHaveAttribute("aria-disabled", "true");
    await expect(archive).toHaveAccessibleDescription(
      /Needs the permission to archive and delete flags/,
    );
  });
});

test.describe("tabs", () => {
  test("opens an environment's tab from the address and keeps the tab in it", async ({ page }) => {
    const project = await seedProject("flag-tabs");
    await seedFlag(project, stringFlag("checkout_variant"));
    await openFlag(page, project, "checkout_variant", { environment: "production" });

    await expect(page.getByRole("tab", { name: "Production" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await page.getByRole("tab", { name: "Staging" }).click();
    await expect(page).toHaveURL(/environment=staging/);
    await page.getByRole("tab", { name: "History" }).click();
    await expect(page).toHaveURL(/tab=history/);
    await expect(page.getByRole("link", { name: "Open the audit log" })).toBeVisible();
    await page.getByRole("tab", { name: "Development" }).click();
    await expect(page).not.toHaveURL(/environment=|tab=/);
  });

  test("tells a missing flag from an error", async ({ page }) => {
    const project = await seedProject("flag-missing");
    await openFlag(page, project, "nothing_here");
    await expect(page.getByRole("heading", { name: "Flag not found" })).toBeVisible();
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the flag page and its definition sheet have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      const project = await seedProject(`flag-axe-${theme}`);
      await seedFlag(project, stringFlag("checkout_variant", { tags: ["checkout"] }));
      await openFlag(page, project, "checkout_variant");
      await expect(page.getByRole("tab", { name: "Development" })).toBeVisible();
      await expectNoSeriousViolations(page, `flag page (${theme})`);

      await page.getByRole("button", { name: "Edit definition" }).click();
      await choose(page, "Lifetime", "Permanent");
      await expectNoSeriousViolations(page, `definition sheet (${theme})`);
    });
  }
});
