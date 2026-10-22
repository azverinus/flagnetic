import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { type Credentials, seedProject, signIn } from "../support/console";
import { admin, seededProject, viewer } from "../support/instance";

/** Signs in and opens the SDK keys of one environment from the environments list. */
async function openKeys(
  page: Page,
  projectKey: string,
  environment = "staging",
  who: Credentials = admin,
) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/environments`);
  await page.getByRole("link", { name: environment, exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/environments/${environment}$`));
  await expect(page.getByRole("tab", { name: "SDK keys" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("button", { name: "New SDK key" })).toBeVisible();
}

/** The row of one key in the table, by its name and, when two share it, its status. */
const keyRow = (page: Page, name: string, status?: string) =>
  page
    .getByRole("row")
    .filter({ has: page.getByRole("cell", { name, exact: true }) })
    .filter(status ? { has: page.getByText(status, { exact: true }) } : {});

/** Creates a key through the dialog and answers its value, shown once. */
async function createKey(
  page: Page,
  { kind, name, origins = [] }: { kind: "Server" | "Client"; name: string; origins?: string[] },
): Promise<string> {
  await page.getByRole("button", { name: "New SDK key" }).click();
  const dialog = page.getByRole("dialog", { name: "New SDK key" });
  if (kind === "Client") {
    await dialog.getByRole("combobox", { name: "Kind" }).click();
    await page.getByRole("option", { name: "Client" }).click();
  }
  await dialog.getByLabel("Name").fill(name);
  for (const origin of origins) {
    await dialog.getByLabel("Allowed origins").fill(origin);
    await dialog.getByLabel("Allowed origins").press("Enter");
  }
  await dialog.getByRole("button", { name: "Create key" }).click();
  return acknowledgeValue(page, "The new SDK key", kind === "Client" ? "fn_cli_" : "fn_srv_");
}

/** Reads the value in a reveal dialog, says it is stored and closes the dialog. */
async function acknowledgeValue(page: Page, title: string, prefix: string): Promise<string> {
  const reveal = page.getByRole("dialog", { name: title });
  const value = (await reveal.locator("code").textContent()) ?? "";
  expect(value).toMatch(new RegExp(`^${prefix}[0-9A-Za-z]{40}$`));
  await reveal.getByLabel("I have stored it somewhere safe").check();
  await reveal.getByRole("button", { name: "Close" }).click();
  await expect(reveal).toBeHidden();
  return value;
}

test.describe("creating an SDK key", () => {
  test("shows a server key's value once, with a copy button and a warning", async ({
    page,
    context,
  }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const projectKey = await seedProject("sdk-keys-create");
    await openKeys(page, projectKey);
    await expect(page.getByText("This environment has no SDK key yet.")).toBeVisible();

    await page.getByRole("button", { name: "New SDK key" }).click();
    const dialog = page.getByRole("dialog", { name: "New SDK key" });
    // A server key takes no origins.
    await expect(dialog.getByLabel("Allowed origins")).toHaveCount(0);
    await dialog.getByLabel("Name").fill("checkout backend");
    await dialog.getByRole("button", { name: "Create key" }).click();

    const reveal = page.getByRole("dialog", { name: "The new SDK key" });
    await expect(reveal).toContainText("This is the only time it is shown.");
    const value = (await reveal.locator("code").textContent()) ?? "";
    expect(value).toMatch(/^fn_srv_[0-9A-Za-z]{40}$/);
    await reveal.getByRole("button", { name: "Copy" }).click();
    await expect(reveal.getByText("Copied to the clipboard.")).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(value);
    // There is no second showing, so the dialog cannot be closed before the member says so.
    await expect(reveal.getByRole("button", { name: "Close" })).toBeDisabled();
    await reveal.getByLabel("I have stored it somewhere safe").check();
    await reveal.getByRole("button", { name: "Close" }).click();

    const row = keyRow(page, "checkout backend");
    await expect(row).toContainText("Server");
    await expect(row).toContainText("Active");
    await expect(row).toContainText(value.slice("fn_srv_".length, "fn_srv_".length + 8));
    // The value is nowhere on the page any more, not even after a reload.
    await expect(page.getByText(value)).toHaveCount(0);
    await page.reload();
    await expect(keyRow(page, "checkout backend")).toBeVisible();
    await expect(page.getByText(value)).toHaveCount(0);
  });

  test("normalizes a client key's origins and names one that is no origin", async ({ page }) => {
    const projectKey = await seedProject("sdk-keys-origins");
    await openKeys(page, projectKey);

    await page.getByRole("button", { name: "New SDK key" }).click();
    const dialog = page.getByRole("dialog", { name: "New SDK key" });
    await dialog.getByRole("combobox", { name: "Kind" }).click();
    await page.getByRole("option", { name: "Client" }).click();
    await dialog.getByLabel("Name").fill("storefront");
    const origins = dialog.getByLabel("Allowed origins");
    await origins.fill("HTTPS://Shop.Example.com:443");
    await origins.press("Enter");
    await origins.fill("shop.example.com/cart");
    await origins.press("Enter");
    await dialog.getByRole("button", { name: "Create key" }).click();
    await expect(dialog.getByLabel("Allowed origins")).toHaveAccessibleDescription(
      /“shop.example.com\/cart” is not an origin/,
    );

    await dialog.getByRole("button", { name: "Remove tag shop.example.com/cart" }).click();
    await dialog.getByRole("button", { name: "Create key" }).click();
    await acknowledgeValue(page, "The new SDK key", "fn_cli_");

    const row = keyRow(page, "storefront");
    await expect(row).toContainText("Client");
    // Shown as the server keeps it: lower case, without the default port.
    await expect(row.getByText("https://shop.example.com", { exact: true })).toBeVisible();
  });

  test("lists a client key with no origins as open to any", async ({ page }) => {
    const projectKey = await seedProject("sdk-keys-any");
    await openKeys(page, projectKey);
    await createKey(page, { kind: "Client", name: "kiosk" });
    await expect(keyRow(page, "kiosk")).toContainText("Any origin");
  });
});

test.describe("rotating, editing and revoking an SDK key", () => {
  test("rotates with an overlap and shows the replacement's value once", async ({ page }) => {
    const projectKey = await seedProject("sdk-keys-rotate");
    await openKeys(page, projectKey);
    const first = await createKey(page, { kind: "Server", name: "worker" });

    await page.getByRole("button", { name: /^Rotate worker/ }).click();
    const dialog = page.getByRole("dialog", { name: "Rotate key worker" });
    const overlap = dialog.getByRole("combobox", { name: "Keep the old key working for" });
    // Seven days is the server's own default.
    await expect(overlap).toContainText("7 days");
    await overlap.click();
    await expect(page.getByRole("option")).toHaveText([
      "None, end the old key now",
      "1 hour",
      "24 hours",
      "7 days",
    ]);
    await page.getByRole("option", { name: "1 hour" }).click();
    await dialog.getByRole("button", { name: "Rotate key" }).click();

    const second = await acknowledgeValue(page, "The replacement SDK key", "fn_srv_");
    expect(second).not.toBe(first);
    // The old key is rotating until its overlap ends, the replacement is active.
    await expect(keyRow(page, "worker", "Rotating")).toBeVisible();
    await expect(keyRow(page, "worker", "Active")).toBeVisible();
    // A key already rotated is never rotated again.
    await expect(
      keyRow(page, "worker", "Rotating").getByRole("button", { name: /^Rotate/ }),
    ).toHaveCount(0);
    await expect(
      keyRow(page, "worker", "Active").getByRole("button", { name: /^Rotate/ }),
    ).toBeVisible();
  });

  test("with no overlap ends the old key at once", async ({ page }) => {
    const projectKey = await seedProject("sdk-keys-rotate-now");
    await openKeys(page, projectKey);
    await createKey(page, { kind: "Server", name: "batch" });

    await page.getByRole("button", { name: /^Rotate batch/ }).click();
    const dialog = page.getByRole("dialog", { name: "Rotate key batch" });
    await dialog.getByRole("combobox", { name: "Keep the old key working for" }).click();
    await page.getByRole("option", { name: "None, end the old key now" }).click();
    await dialog.getByRole("button", { name: "Rotate key" }).click();
    await acknowledgeValue(page, "The replacement SDK key", "fn_srv_");

    await expect(keyRow(page, "batch", "Expired")).toBeVisible();
    await expect(keyRow(page, "batch", "Active")).toBeVisible();
    await expect(keyRow(page, "batch", "Expired").getByRole("button")).toHaveCount(0);
  });

  test("renames a key and edits its allowed origins, down to none", async ({ page }) => {
    const projectKey = await seedProject("sdk-keys-edit");
    await openKeys(page, projectKey);
    await createKey(page, {
      kind: "Client",
      name: "web shop",
      origins: ["https://shop.example.com", "http://localhost:3000"],
    });
    await expect(keyRow(page, "web shop")).toContainText("http://localhost:3000");

    await page.getByRole("button", { name: /^Edit web shop/ }).click();
    const dialog = page.getByRole("dialog", { name: "Edit key web shop" });
    await dialog.getByLabel("Name").fill("storefront");
    await dialog.getByRole("button", { name: "Remove tag http://localhost:3000" }).click();
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(dialog).toBeHidden();
    const renamed = keyRow(page, "storefront");
    await expect(renamed).toContainText("https://shop.example.com");
    await expect(renamed).not.toContainText("http://localhost:3000");

    // Removing the last origin lifts the restriction: it is sent as an empty list, not left out.
    await page.getByRole("button", { name: /^Edit storefront/ }).click();
    const again = page.getByRole("dialog", { name: "Edit key storefront" });
    await again.getByRole("button", { name: "Remove tag https://shop.example.com" }).click();
    await again.getByRole("button", { name: "Save" }).click();
    await expect(again).toBeHidden();
    await expect(keyRow(page, "storefront")).toContainText("Any origin");
  });

  test("revokes a key once confirmed", async ({ page }) => {
    const projectKey = await seedProject("sdk-keys-revoke");
    await openKeys(page, projectKey);
    await createKey(page, { kind: "Server", name: "leaked" });

    await page.getByRole("button", { name: /^Revoke leaked/ }).click();
    const dialog = page.getByRole("dialog", { name: "Revoke key leaked" });
    await expect(dialog).toContainText("This cannot be undone.");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(keyRow(page, "leaked")).toContainText("Active");

    await page.getByRole("button", { name: /^Revoke leaked/ }).click();
    await dialog.getByRole("button", { name: "Revoke key" }).click();
    await expect(dialog).toBeHidden();
    const row = keyRow(page, "leaked");
    await expect(row).toContainText("Revoked");
    await expect(row.getByRole("button")).toHaveCount(0);
  });

  test("explains the limit of ten active keys on the dialog", async ({ page }) => {
    const projectKey = await seedProject("sdk-keys-limit");
    await openKeys(page, projectKey);
    for (let n = 1; n <= 10; n++) {
      await createKey(page, { kind: "Server", name: `service ${n}` });
    }

    await page.getByRole("button", { name: "New SDK key" }).click();
    const dialog = page.getByRole("dialog", { name: "New SDK key" });
    await dialog.getByLabel("Name").fill("one too many");
    await dialog.getByRole("button", { name: "Create key" }).click();
    await expect(dialog.getByRole("alert")).toContainText(
      "The environment already has 10 active keys of this kind.",
    );
  });
});

test.describe("what a role may do with SDK keys", () => {
  test("shows a viewer the keys and no way to change one", async ({ page }) => {
    await openKeys(page, seededProject.key, "staging", viewer);
    const create = page.getByRole("button", { name: "New SDK key" });
    await expect(create).toHaveAttribute("aria-disabled", "true");
    await expect(create).toHaveAccessibleDescription(
      "Needs the permission to manage SDK keys, held by Owner and Maintainer.",
    );
    await expect(page.getByRole("columnheader", { name: "Actions" })).toHaveCount(0);
    await create.click({ force: true });
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });
});

test.describe("the environment page", () => {
  test("opens from the environments list and says when the environment is gone", async ({
    page,
  }) => {
    const projectKey = await seedProject("sdk-keys-page");
    await openKeys(page, projectKey, "production");
    await expect(page.getByRole("heading", { name: "Production", level: 1 })).toBeVisible();

    await page.goto(`/projects/${projectKey}/environments/nowhere`);
    await expect(page.getByRole("heading", { name: "Environment not found" })).toBeVisible();
    await page.getByRole("link", { name: "Go to the environments" }).click();
    await expect(page.getByRole("heading", { name: "Environments", level: 1 })).toBeVisible();
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the SDK keys tab and its dialogs have no serious violation in the ${theme} theme`, async ({
      page,
    }) => {
      const projectKey = await seedProject(`sdk-keys-axe-${theme}`);
      await useTheme(page, theme);
      await openKeys(page, projectKey);
      await createKey(page, {
        kind: "Client",
        name: "storefront",
        origins: ["https://shop.example.com"],
      });
      await createKey(page, { kind: "Server", name: "backend" });
      await expectNoSeriousViolations(page, `the SDK keys tab (${theme})`);

      await page.getByRole("button", { name: "New SDK key" }).click();
      await expect(page.getByRole("dialog", { name: "New SDK key" })).toBeVisible();
      await expectNoSeriousViolations(page, `the new SDK key dialog (${theme})`);
      await page.keyboard.press("Escape");

      await page.getByRole("button", { name: /^Edit storefront/ }).click();
      await expect(page.getByRole("dialog", { name: "Edit key storefront" })).toBeVisible();
      await expectNoSeriousViolations(page, `the edit dialog (${theme})`);
      await page.keyboard.press("Escape");

      await page.getByRole("button", { name: /^Rotate backend/ }).click();
      await expect(page.getByRole("dialog", { name: "Rotate key backend" })).toBeVisible();
      await expectNoSeriousViolations(page, `the rotate dialog (${theme})`);
      await page.getByRole("dialog").getByRole("button", { name: "Rotate key" }).click();
      await expect(page.getByRole("dialog", { name: "The replacement SDK key" })).toBeVisible();
      await expectNoSeriousViolations(page, `the value dialog (${theme})`);
      await acknowledgeValue(page, "The replacement SDK key", "fn_srv_");

      await page.getByRole("button", { name: /^Revoke storefront/ }).click();
      await expect(page.getByRole("dialog", { name: "Revoke key storefront" })).toBeVisible();
      await expectNoSeriousViolations(page, `the revoke dialog (${theme})`);
    });
  }
});
