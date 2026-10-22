import { archiveFlag, createFlag, createGrant, type FlagCreate, getFlag } from "@flagnetic/api";
import { expect, type Locator, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  seedMember,
  seedProject,
  signIn,
} from "../support/console";
import { admin, seededProject, viewer } from "../support/instance";

async function openFlags(page: Page, projectKey: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${projectKey}/flags`);
  await expect(page.getByRole("heading", { name: "Flags", level: 1 })).toBeVisible();
}

async function openNewFlag(page: Page, projectKey: string) {
  await signIn(page);
  await page.goto(`/projects/${projectKey}/flags/-/new`);
  await expect(page.getByRole("heading", { name: "New flag", level: 1 })).toBeVisible();
}

/** The console has opened the page of the flag it just created. */
async function expectFlagPage(page: Page, projectKey: string, key: string, name: string) {
  await expect(page).toHaveURL(
    new RegExp(`/projects/${projectKey}/flags/${key.replace(".", "\\.")}$`),
  );
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}

/** Picks an option of one of the console's selects. */
async function choose(page: Page, field: string, option: string) {
  await page.getByRole("combobox", { name: field }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

const variation = (page: Page, n: number) => page.getByRole("group", { name: `Variation ${n}` });

const flagsTable = (page: Page) => page.getByRole("table", { name: "Flags" });

const rowFor = (page: Page, key: string): Locator =>
  flagsTable(page)
    .getByRole("row")
    .filter({ has: page.getByText(key, { exact: true }) });

/** Creates flags through the management API, as the instance admin. */
async function seedFlags(project: string, flags: FlagCreate[]): Promise<void> {
  const client = managementClient();
  for (const body of flags) {
    must(`creating ${body.key}`, await createFlag({ client, path: { project }, body }));
  }
}

async function readFlag(project: string, flag: string) {
  return must(
    `reading ${flag}`,
    await getFlag({ client: managementClient(), path: { project, flag } }),
  );
}

test.describe("creating a flag", () => {
  test("creates a JSON flag with two variations, correcting a value after its error, and lists it", async ({
    page,
  }) => {
    const projectKey = await seedProject("flags-json");
    await openNewFlag(page, projectKey);

    await page.getByLabel("Key").fill("checkout.limits");
    await choose(page, "Type", "JSON");
    await page.getByLabel("Name", { exact: true }).first().fill("Checkout limits");
    await expect(variation(page, 1)).toBeVisible();
    await expect(variation(page, 2)).toBeVisible();
    await variation(page, 1).getByLabel("Key").fill("small");
    await variation(page, 1).getByLabel("Value").fill('{"items": 3}');
    await variation(page, 2).getByLabel("Key").fill("large");
    await variation(page, 2).getByLabel("Value").fill('{"items": ');
    await page.getByRole("button", { name: "Create flag" }).click();

    await expect(variation(page, 2).getByLabel("Value")).toHaveAccessibleDescription(
      "Enter JSON other than null.",
    );
    await variation(page, 2).getByLabel("Value").fill('{"items": 30}');
    await page.getByRole("button", { name: "Create flag" }).click();

    await expectFlagPage(page, projectKey, "checkout.limits", "Checkout limits");
    await page.goto(`/projects/${projectKey}/flags`);
    await expect(rowFor(page, "checkout.limits")).toContainText("Checkout limits");
    await expect(rowFor(page, "checkout.limits")).toContainText("JSON");

    const flag = await readFlag(projectKey, "checkout.limits");
    expect(flag.variations.map(({ key, value }) => ({ key, value }))).toEqual([
      { key: "small", value: { items: 3 } },
      { key: "large", value: { items: 30 } },
    ]);
    // A new flag is off in every environment of its project.
    expect(flag.environments.length).toBeGreaterThan(0);
    expect(flag.environments.every((configuration) => !configuration.on)).toBe(true);
  });

  test("creates a boolean flag with no variations to edit, the server giving it true and false", async ({
    page,
  }) => {
    const projectKey = await seedProject("flags-bool");
    await openNewFlag(page, projectKey);

    await expect(
      page.getByText("A boolean flag always has exactly the variations true and false."),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Add variation" })).toHaveCount(0);
    await page.getByLabel("Key").fill("newCheckout");
    await page.getByLabel("Name", { exact: true }).fill("New checkout");
    await page.getByRole("button", { name: "Create flag" }).click();

    await expectFlagPage(page, projectKey, "newCheckout", "New checkout");
    await page.goto(`/projects/${projectKey}/flags`);
    await expect(rowFor(page, "newCheckout")).toContainText("Boolean");
    const flag = await readFlag(projectKey, "newCheckout");
    expect(flag.variations.map((v) => v.key)).toEqual(["true", "false"]);
    expect(flag.lifetime).toBe("temporary");
    expect(flag.expected_removal_at).not.toBeNull();
  });

  test("lays out a JSON variation's value on request", async ({ page }) => {
    const projectKey = await seedProject("flags-format");
    await openNewFlag(page, projectKey);

    await choose(page, "Type", "JSON");
    await variation(page, 1).getByLabel("Value").fill('{"a":1,"b":[2]}');
    await variation(page, 1).getByRole("button", { name: "Format variation 1" }).click();
    await expect(variation(page, 1).getByLabel("Value")).toHaveValue(
      '{\n  "a": 1,\n  "b": [\n    2\n  ]\n}',
    );
  });

  test("adds and removes string variation rows, keeping at least one", async ({ page }) => {
    const projectKey = await seedProject("flags-rows");
    await openNewFlag(page, projectKey);

    await choose(page, "Type", "String");
    const rows = page.getByRole("group", { name: /^Variation \d+$/ });
    await expect(rows).toHaveCount(2);
    await page.getByRole("button", { name: "Add variation" }).click();
    await expect(rows).toHaveCount(3);
    await page.getByRole("button", { name: "Remove variation 3" }).click();
    await page.getByRole("button", { name: "Remove variation 2" }).click();
    await expect(rows).toHaveCount(1);
    await expect(page.getByRole("button", { name: "Remove variation 1" })).toBeDisabled();
  });

  test("names another member as owner, with tags, and makes the flag permanent", async ({
    page,
  }) => {
    const projectKey = await seedProject("flags-owner");
    const grace = await seedMember("grace", "Grace Hopper", "harness-compiler-cobol-1959");
    must(
      "granting developer to Grace",
      await createGrant({
        client: managementClient(),
        path: { project: projectKey },
        body: { member: grace.id, role: "developer" },
      }),
    );
    await openNewFlag(page, projectKey);

    await page.getByLabel("Key").fill("search.ranking");
    await page.getByLabel("Name", { exact: true }).fill("Search ranking");
    await expect(page.getByRole("combobox", { name: "Owner" })).toHaveText(`Me (${admin.name})`);
    await choose(page, "Owner", grace.name);
    await page.getByLabel("Tags", { exact: true }).fill("search");
    await page.getByLabel("Tags", { exact: true }).press("Enter");
    await page.getByLabel("Tags", { exact: true }).fill("web");
    await page.getByLabel("Tags", { exact: true }).press("Enter");
    await page.getByRole("button", { name: "Remove tag search" }).click();
    await page.getByLabel("Tags", { exact: true }).fill("ranking");
    await choose(page, "Lifetime", "Permanent");
    await expect(page.getByLabel("Expected removal")).toHaveCount(0);
    await page.getByRole("button", { name: "Create flag" }).click();

    await expectFlagPage(page, projectKey, "search.ranking", "Search ranking");
    await page.goto(`/projects/${projectKey}/flags`);
    await expect(rowFor(page, "search.ranking")).toContainText(grace.name);
    const flag = await readFlag(projectKey, "search.ranking");
    expect(flag.owner).toBe(grace.id);
    expect(flag.tags).toEqual(["web", "ranking"]);
    expect(flag.lifetime).toBe("permanent");
    expect(flag.expected_removal_at).toBeNull();
  });

  test("opens a flag keyed new at flags/new, the new flag page being at flags/-/new", async ({
    page,
  }) => {
    const projectKey = await seedProject("flags-key-new");
    await seedFlags(projectKey, [{ key: "new", type: "boolean", name: "New checkout" }]);
    await signIn(page);

    await page.goto(`/projects/${projectKey}/flags/new`);
    await expectFlagPage(page, projectKey, "new", "New checkout");

    await page.goto(`/projects/${projectKey}/flags`);
    await page.getByRole("link", { name: "New flag" }).first().click();
    await expect(page.getByRole("heading", { name: "New flag", level: 1 })).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`/projects/${projectKey}/flags/-/new$`));
  });

  test("shows a key the project already has on the key field", async ({ page }) => {
    const projectKey = await seedProject("flags-taken");
    await seedFlags(projectKey, [{ key: "taken", type: "boolean", name: "Taken" }]);
    await openNewFlag(page, projectKey);

    await page.getByLabel("Key").fill("taken");
    await page.getByLabel("Name", { exact: true }).fill("Taken again");
    await page.getByRole("button", { name: "Create flag" }).click();

    await expect(page.getByLabel("Key")).toHaveAccessibleDescription(
      /This project already has a flag with that key\./,
    );
  });
});

test.describe("the flags list", () => {
  test("filters by text, type, tag, owner and archived through the address", async ({ page }) => {
    const projectKey = await seedProject("flags-filters");
    const linus = await seedMember("linus-flags", "Linus Flags", "harness-penguin-flags-1991");
    must(
      "granting developer to Linus",
      await createGrant({
        client: managementClient(),
        path: { project: projectKey },
        body: { member: linus.id, role: "developer" },
      }),
    );
    await seedFlags(projectKey, [
      { key: "checkout.button", type: "boolean", name: "Checkout button", tags: ["web"] },
      {
        key: "checkout.copy",
        type: "string",
        name: "Checkout copy",
        tags: ["web", "copy"],
        owner: linus.id,
        variations: [
          { key: "short", value: "Buy" },
          { key: "long", value: "Buy now" },
        ],
      },
      {
        key: "search.limit",
        type: "number",
        name: "Search limit",
        variations: [{ key: "ten", value: 10 }],
      },
      { key: "retired.banner", type: "boolean", name: "Retired banner" },
    ]);
    must(
      "archiving retired.banner",
      await archiveFlag({
        client: managementClient(),
        path: { project: projectKey, flag: "retired.banner" },
        headers: { "If-Match": '"1"' },
      }),
    );
    await openFlags(page, projectKey);
    const keys = flagsTable(page).locator("tbody code");

    await expect(keys).toHaveText(["checkout.button", "checkout.copy", "search.limit"]);

    await page.getByRole("searchbox", { name: "Search" }).fill("checkout");
    await expect(page).toHaveURL(/q=checkout/);
    await expect(keys).toHaveText(["checkout.button", "checkout.copy"]);

    await choose(page, "Type", "String");
    await expect(page).toHaveURL(/type=string/);
    await expect(keys).toHaveText(["checkout.copy"]);

    await choose(page, "Type", "Any type");
    await page.getByRole("searchbox", { name: "Tag" }).fill("web");
    await expect(page).toHaveURL(/tag=web/);
    await choose(page, "Owner", linus.name);
    await expect(keys).toHaveText(["checkout.copy"]);

    // The address holds the whole view: opening it again shows the same.
    await page.reload();
    await expect(keys).toHaveText(["checkout.copy"]);
    await expect(page.getByRole("searchbox", { name: "Tag" })).toHaveValue("web");

    await page.goto(`/projects/${projectKey}/flags`);
    await page.getByLabel("Archived flags instead").click();
    await expect(page).toHaveURL(/archived=true/);
    await expect(keys).toHaveText(["retired.banner"]);
  });

  test("tells no match from no flag, and clears the filters", async ({ page }) => {
    const projectKey = await seedProject("flags-empty");
    await openFlags(page, projectKey);

    await expect(page.getByText("This project has no flag yet.")).toBeVisible();
    // The header's and the empty state's.
    await expect(page.getByRole("link", { name: "New flag" })).toHaveCount(2);

    await page.goto(`/projects/${projectKey}/flags?type=json`);
    await expect(page.getByText("No flag matches these filters.")).toBeVisible();
    await page.getByRole("button", { name: "Clear filters" }).click();
    await expect(page.getByText("This project has no flag yet.")).toBeVisible();
    await expect(page).not.toHaveURL(/type=/);
  });

  test("pages forward and back through 60 flags", async ({ page }) => {
    const projectKey = await seedProject("flags-pages");
    await seedFlags(
      projectKey,
      Array.from({ length: 60 }, (_, i) => ({
        key: `flag${String(i).padStart(2, "0")}`,
        type: "boolean" as const,
        name: `Flag ${i}`,
      })),
    );
    await openFlags(page, projectKey);
    const keys = flagsTable(page).locator("tbody code");
    const previous = page.getByRole("button", { name: "Previous" });
    const next = page.getByRole("button", { name: "Next" });

    await expect(keys).toHaveCount(50);
    await expect(keys.first()).toHaveText("flag00");
    await expect(previous).toBeDisabled();

    await next.click();
    await expect(page).toHaveURL(/cursor=/);
    await expect(keys).toHaveCount(10);
    await expect(keys.first()).toHaveText("flag50");
    await expect(next).toBeDisabled();

    await previous.click();
    await expect(keys).toHaveCount(50);
    await expect(keys.first()).toHaveText("flag00");
    await expect(page).not.toHaveURL(/cursor=/);
    await expect(previous).toBeDisabled();
  });

  test("shows a viewer the list with creating a flag disabled and why", async ({ page }) => {
    await openFlags(page, seededProject.key, viewer);

    const create = page.getByRole("link", { name: "New flag" }).first();
    await expect(create).toHaveAttribute("aria-disabled", "true");
    await expect(create).toHaveAccessibleDescription(/Needs the permission to edit flags/);
    await create.click({ force: true });
    await expect(page).toHaveURL(new RegExp(`/projects/${seededProject.key}/flags$`));
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the new flag form has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      const projectKey = await seedProject(`flags-axe-${theme}`);
      await openNewFlag(page, projectKey);
      await choose(page, "Type", "String");
      await page.getByRole("button", { name: "Create flag" }).click();
      await expect(page.getByLabel("Name", { exact: true }).first()).toHaveAttribute(
        "aria-invalid",
        "true",
      );

      await expectNoSeriousViolations(page, `new flag form (${theme})`);
    });
  }
});
