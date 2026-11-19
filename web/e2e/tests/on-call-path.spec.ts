import { createChangeRequest, createGrant } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  managementClient,
  must,
  seedMember,
  seedProject,
  signIn,
  unprotectProduction,
} from "../support/console";
import { seedFlag, stringFlag, writeConfiguration } from "../support/flags";
import { overlappingControls, sidewaysOverflow } from "../support/layout";

/**
 * The on-call path on a phone: an engineer woken at night signs in, finds
 * the project and the flag, and switches it off, or reads a change request
 * and decides on it, at 390 px with no sideways scroll, no control covered
 * by another and at most one drawer or dialog open at a time.
 */

const password = "harness-quartz-ledger-5520";

/** Fails when the screen scrolls sideways, a control is covered, or two layers are open. */
async function expectFitsAPhone(page: Page, where: string): Promise<void> {
  expect(await sidewaysOverflow(page), `${where} scrolls sideways`).toBeLessThanOrEqual(0);
  // A dialog takes pointer events one render after it opens, so a moment of
  // being covered is waited out; one that stays covered fails.
  await expect
    .poll(() => overlappingControls(page), { message: `${where}: controls covered` })
    .toEqual([]);
  expect(await page.getByRole("dialog").count(), `${where}: layers open`).toBeLessThanOrEqual(1);
}

test.describe("the on-call path at 390 px", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("sign-in to a flag's kill switch dialog", async ({ page }) => {
    const project = await seedProject("on-call");
    await seedFlag(project, stringFlag("payments_v2"));
    await unprotectProduction(project);
    await writeConfiguration(project, "payments_v2", "production", () => ({ on: true }));

    await page.goto("/sign-in");
    await expect(page.getByLabel("Email")).toBeVisible();
    await expectFitsAPhone(page, "sign-in");
    await signIn(page);
    await expectFitsAPhone(page, "projects");

    await page.locator(`a[href="/projects/${project}"]`).first().click();
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expectFitsAPhone(page, "the project");

    // The sidebar is a drawer here: one layer, then the flags list.
    await page
      .getByRole("button", { name: /sidebar/i })
      .first()
      .click();
    await expect(page.getByRole("dialog")).toHaveCount(1);
    await expectFitsAPhone(page, "the sidebar drawer");
    await page
      .getByRole("navigation", { name: "Project" })
      .getByRole("link", { name: "Flags", exact: true })
      .click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByRole("table", { name: "Flags" })).toBeVisible();
    await expectFitsAPhone(page, "the flags list");

    await page.getByRole("link", { name: "payments_v2" }).first().click();
    await expect(page.getByRole("heading", { level: 1, name: /payments_v2/ })).toBeVisible();
    // The context panel is a sheet here, so the environment is chosen beside the title.
    await page.getByRole("combobox", { name: "Environment" }).click();
    await page.getByRole("option", { name: /Production/ }).click();
    await expect(page.getByRole("button", { name: "Kill switch" })).toBeVisible();
    await expectFitsAPhone(page, "the flag");

    await page.getByRole("button", { name: "Kill switch" }).click();
    const dialog = page.getByRole("dialog", { name: "Kill switch: payments_v2 in Production" });
    await expect(dialog).toBeVisible();
    await expectFitsAPhone(page, "the kill switch dialog");
    await expectNoSeriousViolations(page, "the kill switch dialog at 390 px");
    await dialog.getByLabel("Reason").fill("Payments failing at the provider");
    await expect(dialog.getByRole("button", { name: "Switch off" })).toBeEnabled();
    const box = await dialog.getByRole("button", { name: "Switch off" }).boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= 390, "Switch off is on screen").toBe(true);
  });

  test("a change request's detail with its approval", async ({ page }) => {
    const project = await seedProject("on-call-cr");
    await seedFlag(project, stringFlag("checkout_copy"));
    const request = must(
      "proposing a change",
      await createChangeRequest({
        client: managementClient(),
        path: { project },
        body: {
          kind: "flag_configuration",
          flag: "checkout_copy",
          environment: "production",
          title: "Serve the new copy to every customer once the review is over",
          description: "The new copy tested well.",
          proposed: { on: true, off_variation: "control", fallthrough: "treatment" },
          open: true,
        },
      }),
    );
    const reviewer = await seedMember("on-call-rosa", "Rosa Reviewer", password);
    must(
      `granting maintainer in ${project}`,
      await createGrant({
        client: managementClient(),
        path: { project },
        body: { member: reviewer.id, role: "maintainer" },
      }),
    );
    await signIn(page, reviewer);

    await page.goto(`/projects/${project}/change-requests`);
    await expect(page.getByRole("table", { name: "Change requests" })).toBeVisible();
    await expectFitsAPhone(page, "the change requests list");

    await page
      .getByRole("link", { name: /Serve the new copy/ })
      .first()
      .click();
    await expect(page).toHaveURL(new RegExp(`/change-requests/${request.id}`));
    const approve = page.getByRole("button", { name: "Approve", exact: true });
    await expect(approve).toBeVisible();
    await expectFitsAPhone(page, "the change request");
    await approve.click();
    const dialog = page.getByRole("dialog", { name: "Approve the change request" });
    await expect(dialog).toBeVisible();
    await expectFitsAPhone(page, "the approval dialog");
    await expectNoSeriousViolations(page, "the approval dialog at 390 px");
    await dialog.getByLabel("Comment").fill("Looks right.");
    await dialog.getByRole("button", { name: "Approve" }).click();
    await expect(page.getByText("Approved by Rosa Reviewer")).toBeVisible();
    await expectFitsAPhone(page, "the approved change request");
  });
});
