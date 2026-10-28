import { expect, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { seedProject, signIn, unprotectProduction } from "../support/console";
import { openFlag, seedFlag, stringFlag, writeConfiguration } from "../support/flags";
import { seededProject, viewer } from "../support/instance";

test.describe("protected environments", () => {
  test("an owner protects staging and stops protecting production, and the marks follow", async ({
    page,
  }) => {
    const project = await seedProject("protect");
    await signIn(page);
    await page.goto(`/projects/${project}/environments`);

    const row = (key: string) =>
      page.getByRole("row").filter({ has: page.getByRole("cell", { name: key, exact: true }) });
    // A new project's production starts protected; staging does not.
    await expect(row("production")).toContainText("Protected");
    await expect(row("staging")).not.toContainText("Protected");

    await page.goto(`/projects/${project}/environments/staging`);
    const toggle = page.getByRole("switch", { name: "Protected" });
    await expect(toggle).not.toBeChecked();
    await toggle.click();
    const protect = page.getByRole("dialog", { name: "Protect Staging" });
    await expectNoSeriousViolations(page, "the protect dialog");
    await protect.getByRole("button", { name: "Protect environment" }).click();
    await expect(page.getByText("Staging is protected.")).toBeVisible();
    await expect(toggle).toBeChecked();

    await page.goto(`/projects/${project}/environments/production`);
    await page.getByRole("switch", { name: "Protected" }).click();
    await page
      .getByRole("dialog", { name: "Stop protecting Production" })
      .getByRole("button", { name: "Stop protecting" })
      .click();
    await expect(page.getByText("Production is no longer protected.")).toBeVisible();
    await expect(page.getByRole("switch", { name: "Protected" })).not.toBeChecked();

    await page.goto(`/projects/${project}/environments`);
    await expect(row("staging")).toContainText("Protected");
    await expect(row("production")).not.toContainText("Protected");
  });

  test("marks protected environments on a flag's tabs and in the pickers", async ({ page }) => {
    const project = await seedProject("protect-marks");
    await seedFlag(project, stringFlag("checkout_variant"));
    await openFlag(page, project, "checkout_variant");

    await expect(page.getByRole("tab", { name: "Production Protected" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Staging", exact: true })).toBeVisible();

    await page.goto(`/projects/${project}/flags`);
    await page.getByRole("combobox", { name: "Environment", exact: true }).click();
    await expect(page.getByRole("option", { name: "Production (protected)" })).toBeVisible();
    await expect(page.getByRole("option", { name: "Staging", exact: true })).toBeVisible();
  });

  test("a viewer sees the switch disabled, with what it takes", async ({ page }) => {
    await signIn(page, viewer);
    await page.goto(`/projects/${seededProject.key}/environments/production`);

    const toggle = page.getByRole("switch", { name: "Protected" });
    await expect(toggle).toBeDisabled();
    await expect(
      page.getByText(/Needs the permission to protect environments, held by/),
    ).toBeVisible();
  });

  for (const theme of ["light", "dark"] as const) {
    test(`the environment page of a protected environment has no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      const project = await seedProject(`protect-axe-${theme}`);
      await signIn(page);
      await page.goto(`/projects/${project}/environments/production`);
      await expect(page.getByRole("switch", { name: "Protected" })).toBeChecked();
      await expectNoSeriousViolations(page, `a protected environment (${theme})`);
    });
  }
});

test.describe("the kill switch in a protected environment", () => {
  test("asks for the flag's key, and switches off once it is typed", async ({ page }) => {
    const project = await seedProject("protect-kill");
    await seedFlag(project, stringFlag("payments_v2"));
    // Turning the flag on in the protected production takes a change request;
    // the test sets it up unprotected, then protects production again.
    await unprotectProduction(project);
    await writeConfiguration(project, "payments_v2", "production", () => ({ on: true }));
    await signIn(page);
    await page.goto(`/projects/${project}/environments/production`);
    await page.getByRole("switch", { name: "Protected" }).click();
    await page
      .getByRole("dialog", { name: "Protect Production" })
      .getByRole("button", { name: "Protect environment" })
      .click();
    await expect(page.getByText("Production is protected.")).toBeVisible();

    await page.goto(`/projects/${project}/flags/payments_v2?environment=production`);
    await page.getByRole("button", { name: "Kill switch" }).click();
    const dialog = page.getByRole("dialog", { name: "Kill switch: payments_v2 in Production" });
    const switchOff = dialog.getByRole("button", { name: "Switch off" });
    await dialog.getByLabel("Reason").fill("Payments failing at the provider");
    await expect(switchOff).toBeDisabled();

    const key = dialog.getByLabel("Type payments_v2 to confirm");
    await key.fill("payments_v");
    await expect(switchOff).toBeDisabled();
    await expectNoSeriousViolations(page, "the kill switch dialog of a protected environment");
    await key.fill("payments_v2");
    await switchOff.click();

    await expect(page.getByText("payments_v2 is off in Production.")).toBeVisible();
  });
});
