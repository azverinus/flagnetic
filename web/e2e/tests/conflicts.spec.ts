import { createGrant, createSegment, getSegment } from "@flagnetic/api";
import { type Browser, expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  seedMember,
  seedProject,
  signIn,
} from "../support/console";
import { choose, openFlag, readConfiguration, seedFlag, stringFlag } from "../support/flags";

const password = "harness-quill-lantern-2291";

/**
 * A second member of the project, with a browser of their own: what they
 * save while the first member's page is open is the change that page
 * meets, made through the console as a colleague would make it.
 */
async function colleague(
  browser: Browser,
  project: string,
  prefix: string,
  name: string,
): Promise<{ page: Page; who: Credentials }> {
  const who = await seedMember(prefix, name, password);
  must(
    `granting developer in ${project}`,
    await createGrant({
      client: managementClient(),
      path: { project },
      body: { member: who.id, role: "developer" },
    }),
  );
  const context = await browser.newContext();
  return { page: await context.newPage(), who };
}

const unsavedBar = (page: Page) => page.getByRole("region", { name: "Unsaved changes" });

test.describe("two members saving the same thing", () => {
  test("a configuration: the later save shows who saved first and what, and reapplies the draft onto it", async ({
    page,
    browser,
  }) => {
    const project = await seedProject("two-session-config");
    await seedFlag(project, stringFlag("checkout_variant"));
    const other = await colleague(browser, project, "nadia", "Nadia Petrova");

    await openFlag(page, project, "checkout_variant", { environment: "development" });
    await openFlag(other.page, project, "checkout_variant", {
      environment: "development",
      who: other.who,
    });

    await choose(other.page, "Fallthrough variation", "treatment");
    await unsavedBar(other.page).getByRole("button", { name: "Save" }).click();
    await expect(unsavedBar(other.page)).toHaveCount(0);

    await choose(page, "Off variation", "treatment");
    await unsavedBar(page).getByRole("button", { name: "Save" }).click();
    const banner = page.getByRole("alert").filter({ hasText: "saved this configuration" });
    await expect(banner).toContainText("Nadia Petrova saved this configuration on");
    await expect(banner).toContainText("Fallthrough variation changed from control to treatment.");
    await expectNoSeriousViolations(page, "the configuration conflict banner");

    await banner.getByRole("button", { name: "Reload and reapply my draft" }).click();
    await expect(banner).toHaveCount(0);
    await unsavedBar(page).getByRole("button", { name: "Save" }).click();
    await expect(unsavedBar(page)).toHaveCount(0);

    const saved = await readConfiguration(project, "checkout_variant", "development");
    expect(saved).toMatchObject({ off_variation: "treatment", fallthrough: "treatment" });
    await other.page.context().close();
  });

  test("a segment: the later save shows who saved first and what, and keeps the member's edits", async ({
    page,
    browser,
  }) => {
    const project = await seedProject("two-session-segment");
    must(
      "creating beta",
      await createSegment({
        client: managementClient(),
        path: { project },
        body: {
          key: "beta",
          name: "Beta testers",
          conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "NL" }],
        },
      }),
    );
    const other = await colleague(browser, project, "tomas", "Tomas Berg");

    await signIn(page);
    await page.goto(`/projects/${project}/segments/beta`);
    await signIn(other.page, other.who);
    await other.page.goto(`/projects/${project}/segments/beta`);

    await other.page.getByLabel("Name").fill("Early adopters");
    await unsavedBar(other.page).getByRole("button", { name: "Save" }).click();
    await expect(other.page.getByText("The segment is saved.")).toBeVisible();

    await page.getByLabel("Description").fill("Who tries a feature first.");
    await unsavedBar(page).getByRole("button", { name: "Save" }).click();
    const banner = page.getByRole("alert").filter({ hasText: "after you opened this" });
    await expect(banner).toContainText("Tomas Berg saved a change on");
    await expect(banner.getByRole("list", { name: "Changes to the segment" })).toContainText(
      "Name changed from “Beta testers” to “Early adopters”.",
    );
    await expectNoSeriousViolations(page, "the segment conflict banner");

    await banner.getByRole("button", { name: "Keep my edits" }).click();
    await expect(page.getByText("The segment is saved.")).toBeVisible();
    const saved = must(
      "reading beta",
      await getSegment({ client: managementClient(), path: { project, segment: "beta" } }),
    );
    expect(saved).toMatchObject({
      name: "Early adopters",
      description: "Who tries a feature first.",
    });
    await other.page.context().close();
  });
});
