import { evaluate, killFlag, listProjectAuditEntries } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedProject, unprotectProduction } from "../support/console";
import {
  choose,
  openFlag,
  readFlag,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";
import { admin, seededProject, viewer } from "../support/instance";

/** What the flag serves a user in an environment, as the server evaluates it. */
async function servedTo(project: string, flag: string, environment: string, user: string) {
  const evaluation = must(
    `evaluating ${flag} for ${user}`,
    await evaluate({
      client: managementClient(),
      path: { project, environment },
      body: { context: { units: { user } }, flags: [flag] },
    }),
  );
  return evaluation.flags[flag];
}

const overrides = (page: Page) =>
  page.getByRole("table", { name: "Overrides in this environment" });

test.describe("overrides", () => {
  test("gives a unit ID an override with a note, evaluates to it, and removes it", async ({
    page,
  }) => {
    const project = await seedProject("overrides");
    await seedFlag(project, stringFlag("checkout_variant"));
    await writeConfiguration(project, "checkout_variant", "development", () => ({ on: true }));
    await openFlag(page, project, "checkout_variant", { environment: "development" });

    await expect(page.getByText("No overrides in this environment.")).toBeVisible();
    const form = page.getByRole("form", { name: "Add an override" });
    await choose(page, "Unit type", "user");
    await form.getByLabel("Unit ID").fill("qa-7");
    await choose(page, "Variation", "treatment");
    await form.getByLabel("Note (optional)").fill("Checkout QA account");
    await form.getByRole("button", { name: "Add override" }).click();

    const row = overrides(page).getByRole("row").filter({ hasText: "qa-7" });
    await expect(row).toContainText("treatment");
    await expect(row).toContainText("Checkout QA account");
    await expect(row).toContainText(admin.name);
    await expect(form.getByLabel("Unit ID")).toHaveValue("");
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toHaveCount(0);
    expect(await servedTo(project, "checkout_variant", "development", "qa-7")).toMatchObject({
      variation: "treatment",
      reason: "override",
    });

    await page.getByRole("button", { name: "Remove the override of user qa-7" }).click();
    await expect(page.getByText("No overrides in this environment.")).toBeVisible();
    expect(await servedTo(project, "checkout_variant", "development", "qa-7")).toMatchObject({
      reason: "fallthrough",
    });
  });

  test("refuses a note over 200 characters before asking the server", async ({ page }) => {
    const project = await seedProject("override-note");
    await seedFlag(project, stringFlag("checkout_variant"));
    await openFlag(page, project, "checkout_variant");

    const form = page.getByRole("form", { name: "Add an override" });
    await form.getByLabel("Unit ID").fill("qa-7");
    await form.getByLabel("Note (optional)").fill("x".repeat(201));
    await form.getByRole("button", { name: "Add override" }).click();
    await expect(form.getByLabel("Note (optional)")).toHaveAccessibleDescription(
      /At most 200 characters\./,
    );
    expect((await readFlag(project, "checkout_variant")).environments[0]?.overrides).toEqual([]);
  });
});

test.describe("the kill switch", () => {
  test("switches a flag off with a reason, and shows who used it last", async ({ page }) => {
    const project = await seedProject("kill");
    await seedFlag(project, stringFlag("payments_v2"));
    await unprotectProduction(project);
    await writeConfiguration(project, "payments_v2", "production", () => ({ on: true }));
    await openFlag(page, project, "payments_v2", { environment: "production" });

    await page.getByRole("button", { name: "Kill switch" }).click();
    const dialog = page.getByRole("dialog", { name: "Kill switch: payments_v2 in Production" });
    await expect(dialog).toContainText("Never used on this flag in Production.");
    const switchOff = dialog.getByRole("button", { name: "Switch off" });
    await expect(switchOff).toBeDisabled();
    await dialog.getByLabel("Reason").fill("too short");
    await expect(switchOff).toBeDisabled();
    await dialog.getByLabel("Reason").fill("Payments failing at the provider");
    await switchOff.click();

    await expect(page.getByText("payments_v2 is off in Production.")).toBeVisible();
    await expect(
      page.getByRole("switch", { name: "payments_v2 on in Production" }),
    ).not.toBeChecked();
    await expect(page.getByRole("button", { name: "Kill switch" })).toHaveCount(0);

    const entries = must(
      "reading the audit log",
      await listProjectAuditEntries({
        client: managementClient(),
        path: { project },
        query: { kill_switch: true },
      }),
    );
    expect(entries.items).toHaveLength(1);
    expect(entries.items[0]).toMatchObject({ action: "flag.killed", kill_switch: true });
    expect(entries.items[0]?.after).toMatchObject({ reason: "Payments failing at the provider" });

    await page.getByRole("switch", { name: "payments_v2 on in Production" }).click();
    await expect(page.getByText("payments_v2 is on in Production.")).toBeVisible();
    await page.getByRole("button", { name: "Kill switch" }).click();
    await expect(page.getByRole("dialog")).toContainText(`Last used by ${admin.name} on`);
    await expect(page.getByRole("dialog")).toContainText("“Payments failing at the provider”");
  });

  test("tells a kill on a flag someone already switched off changed nothing", async ({ page }) => {
    const project = await seedProject("kill-noop");
    await seedFlag(project, stringFlag("payments_v2"));
    await writeConfiguration(project, "payments_v2", "development", () => ({ on: true }));
    await openFlag(page, project, "payments_v2", { environment: "development" });

    await page.getByRole("button", { name: "Kill switch" }).click();
    must(
      "switching off through the API",
      await killFlag({
        client: managementClient(),
        path: { project, flag: "payments_v2", environment: "development" },
        body: { reason: "someone else was faster" },
      }),
    );
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Reason").fill("Payments failing at the provider");
    await dialog.getByRole("button", { name: "Switch off" }).click();

    await expect(
      page.getByText("payments_v2 was already off in Development. Nothing changed."),
    ).toBeVisible();
    await expect(page.getByRole("switch", { name: "payments_v2 on in Development" })).toBeVisible();
  });

  test("is disabled, with why, for a member who may not use it", async ({ page }) => {
    const key = `viewer_kill_${Date.now().toString(36)}`;
    await seedFlag(seededProject.key, stringFlag(key));
    await writeConfiguration(seededProject.key, key, "development", () => ({ on: true }));
    await openFlag(page, seededProject.key, key, { environment: "development", who: viewer });

    const kill = page.getByRole("button", { name: "Kill switch" });
    await expect(kill).toHaveAttribute("aria-disabled", "true");
    await expect(kill).toHaveAccessibleDescription(
      /Needs the permission to switch flags off with the kill switch/,
    );
    await kill.click({ force: true });
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByRole("switch", { name: `${key} on in Development` })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await expect(page.getByRole("form", { name: "Add an override" })).toHaveCount(0);
    await expect(
      page
        .getByRole("region", { name: "Overrides" })
        .getByText(/Needs the permission to change flag configurations/),
    ).toBeVisible();
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the overrides and the kill switch dialog have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      const project = await seedProject(`kill-axe-${theme}`);
      await seedFlag(project, stringFlag("payments_v2"));
      await writeConfiguration(project, "payments_v2", "development", (current) => ({
        on: true,
        overrides: [
          ...current.overrides,
          { unit_type: "user", unit_id: "qa-7", variation: "treatment", note: "QA" },
        ],
      }));
      await openFlag(page, project, "payments_v2", { environment: "development" });
      await expect(overrides(page)).toBeVisible();
      await expectNoSeriousViolations(page, `flag overrides (${theme})`);

      await page.getByRole("button", { name: "Kill switch" }).click();
      await expect(page.getByRole("dialog")).toBeVisible();
      await expectNoSeriousViolations(page, `kill switch dialog (${theme})`);
    });
  }
});
