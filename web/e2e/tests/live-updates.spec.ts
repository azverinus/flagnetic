import { expect, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { seedProject, signIn } from "../support/console";
import { choose, openFlag, seedFlag, stringFlag, writeConfiguration } from "../support/flags";

test.describe("the console stays current", () => {
  test("a list shows what someone else created, without reloading", async ({ page }) => {
    const project = await seedProject("live-list");
    await seedFlag(project, stringFlag("first_flag"));
    await signIn(page);
    await page.goto(`/projects/${project}/flags`);
    const table = page.getByRole("table", { name: "Flags" });
    await expect(table.getByRole("row", { name: /first_flag/ })).toBeVisible();

    await seedFlag(project, stringFlag("second_flag"));

    await expect(table.getByRole("row", { name: /second_flag/ })).toBeVisible();
    await expectNoSeriousViolations(page, "the flags list after a live update");
  });

  test("a configuration with unsaved edits shows the conflict banner as soon as someone saves", async ({
    page,
  }) => {
    const project = await seedProject("live-banner");
    await seedFlag(project, stringFlag("checkout_variant"));
    await openFlag(page, project, "checkout_variant", { environment: "development" });

    await choose(page, "Off variation", "treatment");
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toBeVisible();

    // No save of ours: the banner comes from the change stream alone.
    await writeConfiguration(project, "checkout_variant", "development", () => ({
      fallthrough: "treatment",
    }));

    const banner = page.getByRole("alert").filter({ hasText: "saved this configuration" });
    await expect(banner).toBeVisible();
    await expectNoSeriousViolations(page, "the banner shown by a live update");
  });
});
