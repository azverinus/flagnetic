import { createExperiment, createSegment, deleteExperiment } from "@flagnetic/api";
import { expect, type Locator, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedProject } from "../support/console";
import {
  openFlag,
  readConfiguration,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";

const rule = (page: Page, n: number) =>
  page.getByRole("region", { name: `Rule ${n}`, exact: true });

/** A project with the flag the specs edit, another flag to require, and a private segment. */
async function seedRuleProject(prefix: string): Promise<string> {
  const project = await seedProject(prefix);
  await seedFlag(project, stringFlag("checkout_variant"));
  await seedFlag(project, stringFlag("payments_v2"));
  must(
    "creating the vip segment",
    await createSegment({
      client: managementClient(),
      path: { project },
      body: {
        key: "vip",
        name: "VIP",
        private: true,
        conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "NL" }],
      },
    }),
  );
  return project;
}

/** A draft experiment comparing the edited flag's two variations evenly, over users. */
async function seedExperiment(project: string, key: string, name: string) {
  return must(
    `creating ${key}`,
    await createExperiment({
      client: managementClient(),
      path: { project },
      body: {
        key,
        name,
        hypothesis: "The shorter checkout converts better.",
        flag: "checkout_variant",
        unit_type: "user",
        variations: [
          { variation: "control", weight: 5000 },
          { variation: "treatment", weight: 5000 },
        ],
        traffic_allocation: 2500,
        expected_end_at: "2027-01-31",
      },
    }),
  );
}

async function openRules(page: Page, project: string) {
  await openFlag(page, project, "checkout_variant", { environment: "development" });
  await expect(page.getByRole("button", { name: "Add rule" })).toBeVisible();
}

/** Picks an option of a select inside a part of the page, with the pointer. */
async function pick(page: Page, within: Locator, field: string, option: string) {
  await within.getByRole("combobox", { name: field, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

/** Picks an option of a select with the keyboard alone: open it, arrow down to the option, take it. */
async function pickByKeyboard(page: Page, within: Locator, field: string, option: string) {
  const select = within.getByRole("combobox", { name: field, exact: true });
  // An option already chosen needs nothing from the keyboard.
  if ((await select.textContent())?.trim() === option) return;
  await select.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("listbox")).toBeVisible();
  await page.keyboard.press("Home");
  for (let step = 0; step < 30; step++) {
    const focused = await page.evaluate(() => document.activeElement?.textContent?.trim());
    if (focused === option) break;
    await page.keyboard.press("ArrowDown");
  }
  await expect(page.getByRole("option", { name: option, exact: true })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(within.getByRole("combobox", { name: field, exact: true })).toHaveText(option);
}

/**
 * Drags one element onto another with the pointer in small steps, as a
 * hand does: the browser starts a drag only once the pointer has moved
 * with the button held.
 */
async function drag(page: Page, source: Locator, target: Locator) {
  await source.scrollIntoViewIfNeeded();
  const from = (await source.boundingBox())!;
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
  await page.mouse.down();
  await page.mouse.move(from.x + from.width / 2 + 10, from.y - 10, { steps: 5 });
  const to = (await target.boundingBox())!;
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 10 });
  await page.mouse.up();
}

/** Activates a button with the keyboard. */
async function press(within: Locator, name: string) {
  const button = within.getByRole("button", { name, exact: true });
  await button.focus();
  await button.press("Enter");
}

test.describe("the rule editor", () => {
  for (const mode of ["pointer and keyboard", "keyboard only"] as const) {
    test(`adds three rules, reorders with the keyboard and saves (${mode})`, async ({ page }) => {
      const project = await seedRuleProject(`rules-${mode === "keyboard only" ? "keys" : "mixed"}`);
      await openRules(page, project);
      const choose = mode === "keyboard only" ? pickByKeyboard : pick;
      const click =
        mode === "keyboard only"
          ? press
          : (within: Locator, name: string) =>
              within.getByRole("button", { name, exact: true }).click();

      await click(page.locator("main"), "Add rule");
      await click(rule(page, 1), "Add attribute condition");
      await choose(page, rule(page, 1), "Attribute (condition 1)", "Country");
      await choose(page, rule(page, 1), "Operator (condition 1)", "equals");
      await rule(page, 1).getByLabel("Value (condition 1)").fill("DE");
      await rule(page, 1).getByLabel("Description (rule 1, optional)").fill("Germany");

      await click(page.locator("main"), "Add rule");
      await click(rule(page, 2), "Add segment condition");
      await choose(page, rule(page, 2), "Segment (condition 1)", "VIP");
      await expect(rule(page, 2).getByText("Private: evaluated on the server only.")).toBeVisible();
      if (mode === "keyboard only") {
        await rule(page, 2).getByRole("radio", { name: "Rollout" }).focus();
        await page.keyboard.press("Space");
      } else {
        await rule(page, 2).getByRole("radio", { name: "Rollout" }).check();
      }
      await choose(page, rule(page, 2), "Variation (rule 2)", "treatment");
      await choose(page, rule(page, 2), "Unit type (rule 2)", "user");
      await rule(page, 2).getByLabel("Percentage (rule 2)").fill("25.5");
      await expect(rule(page, 2).getByText(/continue to the next rule/)).toBeVisible();
      await rule(page, 2).getByLabel("Description (rule 2, optional)").fill("VIP rollout");

      await click(page.locator("main"), "Add rule");
      await click(rule(page, 3), "Add prerequisite condition");
      await rule(page, 3).getByRole("combobox", { name: "Flag (condition 1)" }).click();
      // The list is open before what it lacks is judged, and closed before going on.
      await expect(page.getByRole("option", { name: "payments_v2" })).toBeVisible();
      await expect(page.getByRole("option", { name: "checkout_variant" })).toHaveCount(0);
      await page.keyboard.press("Escape");
      await expect(page.getByRole("listbox")).toHaveCount(0);
      await choose(page, rule(page, 3), "Flag (condition 1)", "payments_v2");
      await choose(page, rule(page, 3), "Required variation (condition 1)", "treatment");
      const third = rule(page, 3).getByLabel("Description (rule 3, optional)");
      await third.fill("Needs payments");

      await third.press("Alt+ArrowUp");
      await expect(
        page.getByRole("status").filter({ hasText: "Rule 3 moved to position 2." }),
      ).toBeAttached();
      await expect(rule(page, 2).getByLabel("Description (rule 2, optional)")).toBeFocused();
      await expect(rule(page, 2).getByLabel("Description (rule 2, optional)")).toHaveValue(
        "Needs payments",
      );

      const bar = page.getByRole("region", { name: "Unsaved changes" });
      if (mode === "keyboard only") await press(bar, "Save");
      else await bar.getByRole("button", { name: "Save" }).click();
      await expect(page.getByText("The configuration in Development is saved.")).toBeVisible();
      await expect(bar).toHaveCount(0);

      const saved = await readConfiguration(project, "checkout_variant", "development");
      expect(saved.rules.map((r) => r.description)).toEqual([
        "Germany",
        "Needs payments",
        "VIP rollout",
      ]);
      expect(saved.rules[0]!.conditions).toEqual([
        { kind: "attribute", attribute: "country", op: "eq", value: "DE" },
      ]);
      expect(saved.rules[1]!.conditions).toEqual([
        { kind: "prerequisite", flag: "payments_v2", variation: "treatment" },
      ]);
      expect(saved.rules[2]!.conditions).toEqual([{ kind: "in_segment", segment: "vip" }]);
      expect(saved.rules[2]!.serve).toMatchObject({
        kind: "rollout",
        variation: "treatment",
        unit_type: "user",
        percentage: 2550,
      });
      const salt = saved.rules[2]!.serve.salt;
      expect(salt).toMatch(/^[A-Za-z0-9]{16}$/);

      await expect(rule(page, 3).getByLabel("Salt (rule 3)")).toHaveValue(salt!);
      await page.reload();
      await expect(rule(page, 3).getByLabel("Salt (rule 3)")).toHaveValue(salt!);
      await expect(rule(page, 3).getByLabel("Percentage (rule 3)")).toHaveValue("25.5");
    });
  }

  test("reorders by dragging and announces the move", async ({ page }) => {
    const project = await seedRuleProject("rules-drag");
    await writeConfiguration(project, "checkout_variant", "development", () => ({
      rules: [
        { description: "first", serve: { kind: "variation", variation: "control" } },
        { description: "second", serve: { kind: "variation", variation: "treatment" } },
      ],
    }));
    await openRules(page, project);

    await drag(page, rule(page, 2).getByRole("button", { name: "Drag rule 2" }), rule(page, 1));
    await expect(
      page.getByRole("status").filter({ hasText: "Rule 2 moved to position 1." }),
    ).toBeAttached();
    await expect(rule(page, 1).getByLabel("Description (rule 1, optional)")).toHaveValue("second");
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toBeVisible();

    await rule(page, 1).getByRole("button", { name: "Move rule 1 down" }).click();
    await expect(
      page.getByRole("status").filter({ hasText: "Rule 1 moved to position 2." }),
    ).toBeAttached();
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toHaveCount(0);
  });

  test("lands a prerequisite cycle the server refuses on the condition that closes it", async ({
    page,
  }) => {
    const project = await seedRuleProject("rules-cycle");
    await writeConfiguration(project, "payments_v2", "development", () => ({
      prerequisites: [{ flag: "checkout_variant", variation: "treatment" }],
    }));
    await openRules(page, project);

    await page.getByRole("button", { name: "Add rule" }).click();
    await rule(page, 1).getByRole("button", { name: "Add prerequisite condition" }).click();
    await pick(page, rule(page, 1), "Flag (condition 1)", "payments_v2");
    await pick(page, rule(page, 1), "Required variation (condition 1)", "control");
    await page
      .getByRole("region", { name: "Unsaved changes" })
      .getByRole("button", { name: "Save" })
      .click();

    const condition = rule(page, 1).getByRole("group", { name: "Condition 1" });
    await expect(condition.getByRole("alert")).toContainText(
      "The flags would require each other in a cycle: checkout_variant → payments_v2 → checkout_variant.",
    );
    await expect(condition.getByRole("combobox", { name: "Flag (condition 1)" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect((await readConfiguration(project, "checkout_variant", "development")).rules).toEqual([]);
  });

  test("checks values by the attribute's type before saving", async ({ page }) => {
    const project = await seedRuleProject("rules-values");
    await openRules(page, project);

    await page.getByRole("button", { name: "Add rule" }).click();
    await rule(page, 1).getByRole("button", { name: "Add attribute condition" }).click();
    await pick(page, rule(page, 1), "Attribute (condition 1)", "App version");
    await pick(page, rule(page, 1), "Operator (condition 1)", "is one of");
    const value = rule(page, 1).getByLabel("Value (condition 1)", { exact: true });
    await value.fill("2.0");
    await value.press("Enter");
    await expect(
      rule(page, 1).getByText("2.0 is not a full semantic version, such as 1.41.0."),
    ).toBeVisible();
    await value.fill("2.0.0");
    await value.press("Enter");
    await value.fill("2.1.0");
    await value.press("Enter");
    await expect(
      rule(page, 1).getByRole("list", { name: "Values of Value (condition 1)" }),
    ).toContainText("2.0.0");
    await rule(page, 1).getByRole("button", { name: "Remove 2.0.0" }).click();

    await page.getByRole("button", { name: "Add rule" }).click();
    await rule(page, 2).getByRole("radio", { name: "Rollout" }).check();
    await rule(page, 2).getByLabel("Percentage (rule 2)").fill("100.5");
    await page
      .getByRole("region", { name: "Unsaved changes" })
      .getByRole("button", { name: "Save" })
      .click();
    await expect(
      rule(page, 2).getByText("Enter a percentage from 0 to 100, to two decimal places."),
    ).toBeVisible();
    await expect(page.getByText("Fix what is marked above before saving.")).toBeVisible();
    expect((await readConfiguration(project, "checkout_variant", "development")).rules).toEqual([]);

    await rule(page, 2).getByLabel("Percentage (rule 2)").fill("50");
    await page
      .getByRole("region", { name: "Unsaved changes" })
      .getByRole("button", { name: "Save" })
      .click();
    await expect(page.getByText("The configuration in Development is saved.")).toBeVisible();
    const saved = await readConfiguration(project, "checkout_variant", "development");
    expect(saved.rules[0]!.conditions).toEqual([
      { kind: "attribute", attribute: "app_version", op: "in", value: ["2.1.0"] },
    ]);
    expect(saved.rules[1]!.serve).toMatchObject({ kind: "rollout", percentage: 5000 });
  });

  test("regenerates a rollout's salt after a warning, keeping the rest of the draft", async ({
    page,
  }) => {
    const project = await seedRuleProject("rules-salt");
    const before = await writeConfiguration(project, "checkout_variant", "development", () => ({
      rules: [
        {
          description: "half",
          serve: { kind: "rollout", variation: "treatment", unit_type: "user", percentage: 5000 },
        },
      ],
    }));
    const oldSalt = before.rules[0]!.serve.salt!;
    await openRules(page, project);
    await expect(rule(page, 1).getByLabel("Salt (rule 1)")).toHaveValue(oldSalt);

    await rule(page, 1).getByLabel("Description (rule 1, optional)").fill("half of users");
    await rule(page, 1).getByRole("button", { name: "Regenerate salt" }).click();
    const dialog = page.getByRole("dialog", { name: "Regenerate the salt?" });
    await expect(dialog).toContainText("Every unit is bucketed afresh");
    await dialog.getByRole("button", { name: "Regenerate salt" }).click();

    await expect(page.getByText("The rollout has a new salt.")).toBeVisible();
    const regenerated = (await readConfiguration(project, "checkout_variant", "development"))
      .rules[0]!;
    expect(regenerated.serve.salt).not.toBe(oldSalt);
    expect(regenerated.description).toBe("half");
    await expect(rule(page, 1).getByLabel("Salt (rule 1)")).toHaveValue(regenerated.serve.salt!);
    await expect(rule(page, 1).getByLabel("Description (rule 1, optional)")).toHaveValue(
      "half of users",
    );

    await page
      .getByRole("region", { name: "Unsaved changes" })
      .getByRole("button", { name: "Save" })
      .click();
    await expect(page.getByText("The configuration in Development is saved.")).toBeVisible();
    const saved = (await readConfiguration(project, "checkout_variant", "development")).rules[0]!;
    expect(saved).toMatchObject({
      description: "half of users",
      serve: { salt: regenerated.serve.salt },
    });
  });
});

test.describe("a rule serving an experiment", () => {
  test("serves a draft experiment, shows it as it is, and marks the flag in the list", async ({
    page,
  }) => {
    const project = await seedRuleProject("rules-experiment");
    await seedExperiment(project, "checkout_test", "Shorter checkout");
    const gone = await seedExperiment(project, "gone_test", "Gone");
    await openRules(page, project);

    await page.getByRole("button", { name: "Add rule" }).click();
    await rule(page, 1).getByRole("radio", { name: "Experiment" }).check();
    await rule(page, 1).getByRole("combobox", { name: "Experiment (rule 1)" }).click();
    await expect(page.getByRole("option", { name: "gone_test" })).toBeVisible();
    await page.getByRole("option", { name: "checkout_test", exact: true }).click();
    // The experiment's status, allocation and weights show as they are, with its page a link away.
    await expect(rule(page, 1).getByText("Draft", { exact: true })).toBeVisible();
    await expect(rule(page, 1).getByText("25.00% of user units included")).toBeVisible();
    const weights = rule(page, 1).getByRole("list", { name: "Weights (rule 1)" });
    await expect(weights.getByRole("listitem")).toHaveText(["control 50.00%", "treatment 50.00%"]);
    await expect(rule(page, 1).getByText(/SDKs do not receive this rule/)).toBeVisible();
    await expect(rule(page, 1).getByRole("link", { name: "Shorter checkout" })).toHaveAttribute(
      "href",
      `/projects/${project}/experiments/checkout_test`,
    );

    // Saving without an experiment chosen is stopped on the rule.
    await page.getByRole("button", { name: "Add rule" }).click();
    await rule(page, 2).getByRole("radio", { name: "Experiment" }).check();
    const bar = page.getByRole("region", { name: "Unsaved changes" });
    await bar.getByRole("button", { name: "Save" }).click();
    await expect(rule(page, 2).getByText("Choose an experiment.")).toBeVisible();

    // An experiment deleted while the page is open is refused by the
    // server, and the refusal is pinned on the rule that served it.
    await pick(page, rule(page, 2), "Experiment (rule 2)", "gone_test");
    must(
      "deleting gone_test",
      await deleteExperiment({
        client: managementClient(),
        path: { project, experiment: "gone_test" },
        headers: { "If-Match": `"${gone.version}"` },
      }),
    );
    await bar.getByRole("button", { name: "Save" }).click();
    await expect(rule(page, 2).getByText("The project has no such experiment.")).toBeVisible();

    await rule(page, 2).getByRole("button", { name: "Remove rule 2" }).click();
    await bar.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("The configuration in Development is saved.")).toBeVisible();
    const saved = await readConfiguration(project, "checkout_variant", "development");
    expect(saved.rules.map((r) => r.serve)).toEqual([
      { kind: "experiment", experiment: "checkout_test" },
    ]);

    // The flags list marks the environment and filters by it.
    await page.goto(`/projects/${project}/flags?environment=development&state=experiment`);
    await expect(page.getByRole("link", { name: "checkout_variant" })).toBeVisible();
    await expect(page.getByRole("link", { name: "payments_v2" })).toHaveCount(0);
    await expect(page.getByTitle("Serves an experiment")).toHaveCount(1);
  });
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the rule editor has no serious violation in the ${theme} theme`, async ({ page }) => {
      await useTheme(page, theme);
      const project = await seedRuleProject(`rules-axe-${theme}`);
      await seedExperiment(project, "checkout_test", "Shorter checkout");
      await writeConfiguration(project, "checkout_variant", "development", () => ({
        prerequisites: [{ flag: "payments_v2", variation: "treatment" }],
        rules: [
          {
            description: "Germany",
            conditions: [
              { kind: "attribute", attribute: "country", op: "in", value: ["DE", "AT"] },
            ],
            serve: { kind: "variation", variation: "treatment" },
          },
          {
            description: "VIP rollout",
            conditions: [{ kind: "in_segment", segment: "vip" }],
            serve: { kind: "rollout", variation: "treatment", unit_type: "user", percentage: 2500 },
          },
          {
            description: "Checkout test",
            conditions: [],
            serve: { kind: "experiment", experiment: "checkout_test" },
          },
        ],
      }));
      await openRules(page, project);
      await rule(page, 1).getByLabel("Description (rule 1, optional)").fill("Germany and Austria");
      await expect(page.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
      await expectNoSeriousViolations(page, `rule editor (${theme})`);
    });
  }
});
