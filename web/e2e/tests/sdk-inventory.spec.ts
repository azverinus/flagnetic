import { expect, type Page, test } from "@playwright/test";
import { readFileSync } from "node:fs";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, seedProject, signIn } from "../support/console";
import { admin, seededProject, viewer } from "../support/instance";
import { sdkRequest, seedSdkKey } from "../support/sdk";

/** Signs in and opens one tab of an environment page. */
async function openEnvironment(
  page: Page,
  project: string,
  tab: "SDK keys" | "SDKs",
  who: Credentials = admin,
  environment = "staging",
) {
  await signIn(page, who);
  await page.goto(`/projects/${project}/environments/${environment}`);
  await page.getByRole("tab", { name: tab, exact: true }).click();
  await expect(page.getByRole("tab", { name: tab, exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
}

/**
 * The inventory is written by a worker once a minute, so a row an SDK
 * request caused shows within a little over that. The page is read again
 * until the row is there.
 */
async function expectRow(page: Page, name: RegExp) {
  const row = page.getByRole("row", { name });
  await expect(async () => {
    await page.reload();
    await expect(row).toBeVisible({ timeout: 1_000 });
  }).toPass({ timeout: 90_000, intervals: [3_000] });
  return row;
}

test.describe("the SDKs tab", () => {
  test.setTimeout(150_000);

  test("lists the SDKs that used the environment, and says what an empty one is waiting for", async ({
    page,
  }) => {
    const project = await seedProject("sdk-inventory");
    await openEnvironment(page, project, "SDKs");
    await expect(
      page.getByText("SDKs appear here after their first request", { exact: false }),
    ).toBeVisible();

    const server = await seedSdkKey(project, "staging", {
      kind: "server",
      name: "checkout backend",
    });
    const client = await seedSdkKey(project, "staging", { kind: "client", name: "storefront" });
    await sdkRequest(server, "/ruleset", { userAgent: "flagnetic-go/0.1.0 (linux)" });
    await sdkRequest(client, "/evaluate", {
      body: {
        context: {
          units: { user: "u-1" },
          attributes: { app_version: "4.2.0", sdk: "flagnetic-js", platform: "browser" },
        },
      },
    });

    const go = await expectRow(page, /flagnetic-go/);
    await expect(go).toContainText("0.1.0");
    await expect(go).toContainText("linux");
    await expect(go).toContainText("checkout backend");
    await expect(go).toContainText("Server");
    const version = await sdkRequest(server, "/ruleset").then((r) => r.headers.get("etag"));
    expect(version).toMatch(/^"\d+"$/);
    await expect(go).toContainText(version!.slice(1, -1));

    // The client's request may be written in a later flush than the server's.
    const js = await expectRow(page, /flagnetic-js/);
    await expect(js).toContainText("4.2.0");
    await expect(js).toContainText("browser");
    await expect(js).toContainText("storefront");
    await expect(js).toContainText("Client");
    await expectNoSeriousViolations(page, "the SDKs tab with SDKs");
    // The tab is another environment's too: staging's SDKs are not production's.
    await page.goto(`/projects/${project}/environments/production?tab=sdks`);
    await expect(
      page.getByText("SDKs appear here after their first request", { exact: false }),
    ).toBeVisible();
  });

  test("shows a viewer the inventory", async ({ page }) => {
    await openEnvironment(page, seededProject.key, "SDKs", viewer);
    await expect(page.getByRole("table", { name: "SDKs" })).toBeVisible();
  });
});

test.describe("downloading the ruleset", () => {
  test("saves the environment's ruleset document as a file named after it", async ({ page }) => {
    const project = await seedProject("ruleset-download");
    await openEnvironment(page, project, "SDK keys");

    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "Download ruleset" }).click();
    const file = await download;
    expect(file.suggestedFilename()).toMatch(
      new RegExp(`^${project}-staging-ruleset-\\d+\\.json$`),
    );
    const document = JSON.parse(readFileSync((await file.path()) ?? "", "utf8"));
    expect(document).toMatchObject({ schema_version: 1, scope: "server" });
    expect(file.suggestedFilename()).toContain(`-${document.version}.json`);
    expect(document.environment.key).toBe("staging");
  });

  test("is open to a viewer, who needs only to read the project", async ({ page }) => {
    await openEnvironment(page, seededProject.key, "SDKs", viewer);
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "Download ruleset" }).click();
    expect((await download).suggestedFilename()).toMatch(/^checkout-staging-ruleset-\d+\.json$/);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the SDKs tab has no serious violation in the ${theme} theme`, async ({ page }) => {
      const project = await seedProject(`sdk-inventory-axe-${theme}`);
      await useTheme(page, theme);
      await openEnvironment(page, project, "SDKs");
      await expectNoSeriousViolations(page, `the empty SDKs tab (${theme})`);
    });
  }
});
