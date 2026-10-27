import { createGrant, getEnvironment, setEnvironmentProtection } from "@flagnetic/api";
import { type Browser, expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  managementClient,
  must,
  seedMember,
  seedProject,
  signIn,
  unprotectProduction,
} from "../support/console";
import {
  choose,
  openFlag,
  readConfiguration,
  readFlag,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";

const password = "harness-lantern-quorum-8164";

/** A member of the project with a role, signed in in a browser of their own. */
async function memberWith(
  browser: Browser,
  project: string,
  role: "viewer" | "developer" | "maintainer",
  name: string,
): Promise<Page> {
  const who = await seedMember(role, name, password);
  must(
    `granting ${role} in ${project}`,
    await createGrant({
      client: managementClient(),
      path: { project },
      body: { member: who.id, role },
    }),
  );
  const page = await (await browser.newContext()).newPage();
  await signIn(page, who);
  return page;
}

/** Protects a project's production again, after a spec wrote it directly. */
async function protectProduction(project: string): Promise<void> {
  const client = managementClient();
  const path = { project, environment: "production" };
  const current = must(`reading production of ${project}`, await getEnvironment({ client, path }));
  must(
    `protecting production of ${project}`,
    await setEnvironmentProtection({
      client,
      path,
      headers: { "If-Match": `"${current.version}"` },
      body: { protected: true },
    }),
  );
}

/** Fills and sends the propose dialog, and waits for the change request it opens. */
async function propose(page: Page, title: string): Promise<void> {
  const dialog = page.getByRole("dialog", { name: "Propose the change" });
  await dialog.getByLabel("Title").fill(title);
  await dialog.getByRole("button", { name: "Propose", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(/\/change-requests\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { name: title, level: 1 })).toBeVisible();
  await expect(page.getByText("Open", { exact: true })).toBeVisible();
}

test.describe("proposing from the editors", () => {
  test("a developer proposes a production change, which a maintainer approves and applies", async ({
    browser,
  }) => {
    const project = await seedProject("proposing");
    await seedFlag(project, stringFlag("checkout_copy"));

    // The editor of a protected environment proposes instead of saving.
    const dmitri = await memberWith(browser, project, "developer", "Dmitri Developer");
    await dmitri.goto(`/projects/${project}/flags/checkout_copy?environment=production`);
    await expect(
      dmitri.getByText(
        "The environment is protected: changes to its configuration are proposed as change requests and go live once approved and applied.",
      ),
    ).toBeVisible();
    await choose(dmitri, "Fallthrough variation", "treatment");
    const bar = dmitri.getByRole("region", { name: "Unsaved changes" });
    await expect(bar.getByRole("button", { name: "Save", exact: true })).toHaveCount(0);
    await bar.getByRole("button", { name: "Propose", exact: true }).click();
    await expect(dmitri.getByRole("dialog", { name: "Propose the change" })).toBeVisible();
    await expectNoSeriousViolations(dmitri, "the propose dialog");
    await propose(dmitri, "Serve the new copy");
    await expect(
      dmitri.getByRole("list", { name: "Changes to the configuration" }).getByRole("listitem"),
    ).toHaveText(["Fallthrough variation changed from control to treatment."]);
    expect((await readConfiguration(project, "checkout_copy", "production")).fallthrough).toBe(
      "control",
    );

    // A viewer reads the configuration and is told what proposing needs.
    const vera = await memberWith(browser, project, "viewer", "Vera Viewer");
    await vera.goto(`/projects/${project}/flags/checkout_copy?environment=production`);
    await expect(vera.getByText(/propose changes as change requests/)).toBeVisible();
    await expect(
      vera.getByRole("combobox", { name: "Fallthrough variation", exact: true }),
    ).toBeDisabled();

    // A maintainer approves and applies it, and production serves it.
    const rosa = await memberWith(browser, project, "maintainer", "Rosa Reviewer");
    await rosa.goto(`/projects/${project}/change-requests?tab=awaiting`);
    await rosa.getByRole("link", { name: "Serve the new copy" }).click();
    await rosa.getByRole("button", { name: "Approve", exact: true }).click();
    await rosa
      .getByRole("dialog", { name: "Approve the change request" })
      .getByRole("button", { name: "Approve" })
      .click();
    await expect(rosa.getByText("Approved by Rosa Reviewer")).toBeVisible();
    await rosa.getByRole("button", { name: "Apply", exact: true }).click();
    await rosa
      .getByRole("dialog", { name: "Apply the change request" })
      .getByRole("button", { name: "Apply" })
      .click();
    await expect(rosa.getByText("Applied", { exact: true }).first()).toBeVisible();
    expect((await readConfiguration(project, "checkout_copy", "production")).fallthrough).toBe(
      "treatment",
    );
  });

  test("a definition edit refused for a protected environment becomes a proposal", async ({
    page,
  }) => {
    const project = await seedProject("proposing-definition");
    await seedFlag(project, stringFlag("checkout_variant", { name: "Checkout variant" }));
    await openFlag(page, project, "checkout_variant");

    await page.getByRole("button", { name: "Edit definition" }).click();
    const sheet = page.getByRole("dialog", { name: "Edit definition" });
    await sheet.getByLabel("Name", { exact: true }).first().fill("Checkout experiment");
    await sheet.getByRole("group", { name: "Variation 2" }).getByLabel("Value").fill("treatment-b");
    await sheet.getByRole("button", { name: "Save definition" }).click();

    const dialog = page.getByRole("dialog", { name: "Propose the change" });
    await expect(dialog).toContainText("the variations and automatic exposure are proposed");
    await expectNoSeriousViolations(page, "the propose dialog of a definition");
    await propose(page, "Rename the treatment's value");
    await expect(
      page.getByRole("list", { name: "Changes to the definition" }).getByRole("listitem"),
    ).toHaveText(['Variation treatment serves "treatment-b" instead of "treatment".']);

    // The name needs no review and is saved; the variation waits for it.
    const flag = await readFlag(project, "checkout_variant");
    expect(flag.name).toBe("Checkout experiment");
    expect(flag.variations.map((variation) => variation.value)).toEqual(["control", "treatment"]);
  });

  test("reverting a protected environment proposes the version reverted to", async ({ page }) => {
    const project = await seedProject("proposing-revert");
    await unprotectProduction(project);
    await seedFlag(project, stringFlag("banner"));
    await writeConfiguration(project, "banner", "production", () => ({ on: true }));
    await writeConfiguration(project, "banner", "production", () => ({
      fallthrough: "treatment",
    }));
    await protectProduction(project);

    await openFlag(page, project, "banner", { environment: "production" });
    await page.getByRole("tab", { name: "History" }).click();
    const history = page.getByRole("table", { name: "History of the flag" });
    await history.getByRole("row").nth(2).getByRole("link").click();
    const drawer = page.getByRole("dialog", { name: "Audit entry" });
    await drawer.getByRole("button", { name: "Revert to this version" }).click();
    await page
      .getByRole("dialog", { name: "Revert to version 2" })
      .getByRole("button", { name: "Revert", exact: true })
      .click();

    await expect(page.getByRole("dialog", { name: "Propose the change" })).toContainText(
      "reverting its configuration to version 2 goes through a change request",
    );
    await propose(page, "Back to control");
    await expect(page.getByText("Reverts the configuration to version 2.")).toBeVisible();
    await expect(
      page.getByRole("list", { name: "Changes to the configuration" }).getByRole("listitem"),
    ).toHaveText(["Fallthrough variation changed from treatment to control."]);
    await expectNoSeriousViolations(page, "a revert's change request");
    expect((await readConfiguration(project, "banner", "production")).fallthrough).toBe(
      "treatment",
    );
  });
});
