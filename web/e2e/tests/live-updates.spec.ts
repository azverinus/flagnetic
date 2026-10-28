import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { colleague, seedProject, signIn } from "../support/console";
import { choose, openFlag, seedFlag, stringFlag } from "../support/flags";

const unsavedBar = (page: Page) => page.getByRole("region", { name: "Unsaved changes" });

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

  test("a configuration with unsaved edits shows the conflict banner as soon as a colleague saves", async ({
    page,
    browser,
  }) => {
    const project = await seedProject("live-banner");
    await seedFlag(project, stringFlag("checkout_variant"));
    const other = await colleague(browser, project, "ines", "Ines Moreau");

    await openFlag(page, project, "checkout_variant", { environment: "development" });
    await openFlag(other.page, project, "checkout_variant", {
      environment: "development",
      who: other.who,
    });

    await choose(page, "Off variation", "treatment");
    await expect(unsavedBar(page)).toBeVisible();

    // No save of ours: the banner comes from the change stream alone.
    await choose(other.page, "Fallthrough variation", "treatment");
    await unsavedBar(other.page).getByRole("button", { name: "Save" }).click();
    await expect(unsavedBar(other.page)).toHaveCount(0);

    const banner = page.getByRole("alert").filter({ hasText: "saved this configuration" });
    await expect(banner).toContainText("Ines Moreau saved this configuration on");
    await expectNoSeriousViolations(page, "the banner shown by a live update");

    // The colleague's page shows the first member's change in turn, unreloaded.
    await banner.getByRole("button", { name: "Reload and reapply my draft" }).click();
    await unsavedBar(page).getByRole("button", { name: "Save" }).click();
    await expect(unsavedBar(page)).toHaveCount(0);
    await expect(
      other.page.getByRole("combobox", { name: "Off variation", exact: true }),
    ).toHaveText(/treatment/);
    await other.page.context().close();
  });
});
