import {
  createExperiment,
  createGrant,
  type ExperimentCreate,
  getExperiment,
  getExperimentAssignment,
  listMembers,
  startExperiment,
  stopExperiment,
  updateExperiment,
} from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { type Credentials, managementClient, must, seedProject, signIn } from "../support/console";
import {
  choose,
  readConfiguration,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";
import { admin, viewer } from "../support/instance";
import { postExposures, seedSdkKey } from "../support/sdk";

/** Signs in and opens a project's experiments. */
async function openExperiments(page: Page, project: string, who: Credentials = admin) {
  await signIn(page, who);
  await page.goto(`/projects/${project}/experiments`);
  await expect(page.getByRole("heading", { level: 1, name: "Experiments" })).toBeVisible();
}

/** The list's row of one experiment, by its key. */
const rowFor = (page: Page, key: string) =>
  page.getByRole("row").filter({ has: page.getByText(key, { exact: true }) });

/** An experiment comparing a string flag's two variations evenly, over users. */
function experimentOn(flag: string, key: string, over: Partial<ExperimentCreate> = {}) {
  return {
    key,
    name: key,
    hypothesis: "The shorter checkout converts better.",
    flag,
    unit_type: "user",
    variations: [
      { variation: "control", weight: 5000 },
      { variation: "treatment", weight: 5000 },
    ],
    expected_end_at: "2027-01-31",
    ...over,
  };
}

async function seedExperiment(project: string, body: ExperimentCreate) {
  return must(
    `creating ${body.key}`,
    await createExperiment({ client: managementClient(), path: { project }, body }),
  );
}

async function readExperiment(project: string, experiment: string) {
  return must(
    `reading ${experiment}`,
    await getExperiment({ client: managementClient(), path: { project, experiment } }),
  );
}

test.describe("experiments", () => {
  test("creates a draft only once its weights sum to 100 % and lists it with the environments holding it", async ({
    page,
  }) => {
    const project = await seedProject("exp-create");
    await seedFlag(project, stringFlag("checkout_cta"));
    await openExperiments(page, project);
    await expect(page.getByText("This project has no experiment yet.")).toBeVisible();

    await page.getByRole("link", { name: "New experiment" }).first().click();
    await expect(page.getByRole("heading", { level: 1, name: "New experiment" })).toBeVisible();
    await page.getByLabel("Key").fill("cta_copy");
    await page.getByLabel("Name").fill("CTA copy");
    await choose(page, "Flag", "checkout_cta");
    // Choosing the flag splits its variations evenly.
    await expect(page.getByText("Total: 100.00 %")).toBeVisible();
    await page.getByLabel("Weight of variation 2 (%)").fill("30");
    await expect(page.getByText("Total: 80.00 %")).toBeVisible();

    // Nothing that could not start is sent: the weights, the hypothesis,
    // the unit type and the expected end are asked for first.
    await page.getByRole("button", { name: "Create experiment" }).click();
    await expect(
      page.getByText("The weights sum to 80.00 %; they must sum to 100.00 %."),
    ).toBeVisible();
    await expect(
      page.getByText("Write the hypothesis: an experiment starts only with one."),
    ).toBeVisible();
    await expect(page.getByText("Choose a unit type.")).toBeVisible();
    await expect(
      page.getByText("Choose the day the experiment is expected to end: it starts only with one."),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/experiments\/-\/new$/);

    await page.getByLabel("Weight of variation 1 (%)").fill("70");
    await page.getByLabel("Hypothesis").fill("A shorter call to action converts better.");
    await choose(page, "Unit type", "User (user)");
    await page.getByLabel("Traffic allocation (%)").fill("25.5");
    await page.getByLabel("Expected end").fill("2027-01-31");
    await page.getByLabel("Tags").fill("growth");
    await page.getByLabel("Tags").press("Enter");
    await page.getByRole("button", { name: "Create experiment" }).click();

    await expect(page.getByRole("heading", { level: 1, name: "CTA copy" })).toBeVisible();
    await expect(page.getByText("Draft", { exact: true })).toBeVisible();
    const created = await readExperiment(project, "cta_copy");
    expect(created).toMatchObject({
      status: "draft",
      hypothesis: "A shorter call to action converts better.",
      unit_type: "user",
      variations: [
        { variation: "control", weight: 7000 },
        { variation: "treatment", weight: 3000 },
      ],
      traffic_allocation: 2550,
      expected_end_at: "2027-01-31",
      tags: ["growth"],
    });

    // A rule serving the experiment makes its environment a holder.
    await writeConfiguration(project, "checkout_cta", "development", () => ({
      rules: [
        { description: "", conditions: [], serve: { kind: "experiment", experiment: "cta_copy" } },
      ],
    }));
    await page.getByRole("link", { name: "Experiments", exact: true }).first().click();
    const row = rowFor(page, "cta_copy");
    await expect(row.getByText("Draft", { exact: true })).toBeVisible();
    await expect(row.getByRole("link", { name: "checkout_cta" })).toBeVisible();
    await expect(row.getByRole("list", { name: "Holding environments" })).toHaveText("Development");
    await expect(row.getByText("growth")).toBeVisible();
  });

  test("filters by flag and status, keeping the filters in the address", async ({ page }) => {
    const project = await seedProject("exp-filter");
    await seedFlag(project, stringFlag("checkout_cta"));
    await seedFlag(project, stringFlag("search_ranking"));
    await seedExperiment(project, experimentOn("checkout_cta", "cta_copy"));
    await seedExperiment(project, experimentOn("search_ranking", "ranking_v2"));
    await openExperiments(page, project);
    await expect(rowFor(page, "cta_copy")).toBeVisible();
    await expect(rowFor(page, "ranking_v2")).toBeVisible();
    await expect(rowFor(page, "ranking_v2").getByText("No rule serves it")).toBeVisible();

    await choose(page, "Flag", "search_ranking");
    await expect(page).toHaveURL(/flag=search_ranking/);
    await expect(rowFor(page, "ranking_v2")).toBeVisible();
    await expect(rowFor(page, "cta_copy")).toHaveCount(0);

    await choose(page, "Status", "Running");
    await expect(page.getByText("No experiment matches these filters.")).toBeVisible();
    await page.getByRole("button", { name: "Clear filters" }).click();
    await expect(rowFor(page, "cta_copy")).toBeVisible();
  });

  test("edits a draft and keeps the member's edits over a change saved meanwhile", async ({
    page,
  }) => {
    const project = await seedProject("exp-edit");
    await seedFlag(project, stringFlag("checkout_cta"));
    const seeded = await seedExperiment(project, experimentOn("checkout_cta", "cta_copy"));
    await signIn(page, admin);
    await page.goto(`/projects/${project}/experiments/cta_copy`);
    await expect(page.getByRole("heading", { level: 1, name: "cta_copy" })).toBeVisible();
    await expect(page.getByLabel("Key")).toBeDisabled();

    await page.getByLabel("Name").fill("CTA copy");
    await page.getByRole("button", { name: "Split evenly" }).click();
    await page.getByLabel("Weight of variation 1 (%)").fill("60");
    await page.getByLabel("Weight of variation 2 (%)").fill("40");

    // Someone else changes the hypothesis meanwhile.
    must(
      "changing the hypothesis",
      await updateExperiment({
        client: managementClient(),
        path: { project, experiment: "cta_copy" },
        body: { hypothesis: "A greener button converts better." },
        headers: { "If-Match": `"${seeded.version}"` },
      }),
    );

    await page.getByRole("button", { name: "Save" }).click();
    const banner = page.getByRole("alert").filter({ hasText: "after you opened this" });
    await expect(banner).toBeVisible();
    await expect(banner.getByText("Hypothesis changed.")).toBeVisible();
    await banner.getByRole("button", { name: "Keep my edits" }).click();
    await expect(page.getByText("The experiment is saved.")).toBeVisible();

    expect(await readExperiment(project, "cta_copy")).toMatchObject({
      name: "CTA copy",
      hypothesis: "A greener button converts better.",
      variations: [
        { variation: "control", weight: 6000 },
        { variation: "treatment", weight: 4000 },
      ],
    });
  });

  test("starts, grows, stops with a winner and clones, confirming what changes where", async ({
    page,
  }) => {
    const project = await seedProject("exp-transitions");
    const flag = "checkout_cta";
    await seedFlag(project, stringFlag(flag));
    await seedExperiment(project, experimentOn(flag, "cta_copy", { traffic_allocation: 2500 }));
    const serving = (description: string) => ({
      description,
      conditions: [],
      serve: { kind: "experiment" as const, experiment: "cta_copy" },
    });
    await writeConfiguration(project, flag, "development", () => ({
      rules: [serving("Beta testers")],
    }));
    await writeConfiguration(project, flag, "staging", () => ({ rules: [serving("")] }));
    await signIn(page, admin);
    await page.goto(`/projects/${project}/experiments/cta_copy`);
    await expect(page.getByRole("heading", { level: 1, name: "cta_copy" })).toBeVisible();

    // Start, naming the environments whose rulesets take it.
    await page.getByRole("button", { name: "Start", exact: true }).click();
    let dialog = page.getByRole("dialog", { name: "Start cta_copy" });
    await expect(dialog.getByRole("list", { name: "Environments changed" })).toHaveText(
      /Development\s*Staging/,
    );
    await expectNoSeriousViolations(page, "the start dialog");
    await dialog.getByRole("button", { name: "Start experiment" }).click();
    await expect(page.getByText("The experiment cta_copy is running.")).toBeVisible();
    await expect(page.getByText("Running", { exact: true })).toBeVisible();
    expect((await readExperiment(project, "cta_copy")).status).toBe("running");

    // The traffic allocation only grows, through its own confirmation.
    await expect(page.getByLabel("Traffic allocation (%)")).toBeDisabled();
    await page.getByRole("button", { name: "Grow traffic allocation" }).click();
    dialog = page.getByRole("dialog", { name: "Grow the traffic allocation of cta_copy" });
    await dialog.getByLabel("New traffic allocation (%)").fill("20");
    await dialog.getByRole("button", { name: "Grow traffic allocation" }).click();
    await expect(
      dialog.getByText("Enter more than 25.00 %: the traffic allocation only grows."),
    ).toBeVisible();
    await dialog.getByLabel("New traffic allocation (%)").fill("50");
    await dialog.getByRole("button", { name: "Grow traffic allocation" }).click();
    await expect(page.getByText("The traffic allocation of cta_copy grew.")).toBeVisible();
    expect((await readExperiment(project, "cta_copy")).traffic_allocation).toBe(5000);

    // Stop with a winner, listing each rule it rewrites.
    const before = await readConfiguration(project, flag, "development");
    await page.getByRole("button", { name: "Stop", exact: true }).click();
    dialog = page.getByRole("dialog", { name: "Stop cta_copy" });
    await choose(page, "Winning variation", "treatment");
    await expect(dialog.getByRole("list", { name: "Rules rewritten" })).toContainText(
      "Development, rule 1 “Beta testers”: serves treatment, its conditions kept",
    );
    await expect(dialog.getByRole("list", { name: "Rules rewritten" })).toContainText(
      "Staging, rule 1: serves treatment, its conditions kept",
    );
    await dialog.getByLabel("Note").fill("Clear lift on checkout.");
    await dialog.getByRole("button", { name: "Stop experiment" }).click();
    await expect(page.getByText("The experiment cta_copy is stopped.")).toBeVisible();
    await expect(page.getByText("Shipped treatment: its rules serve it.")).toBeVisible();
    await expect(page.getByText("Clear lift on checkout.")).toBeVisible();
    expect(await readExperiment(project, "cta_copy")).toMatchObject({
      status: "stopped",
      decision: {
        outcome: "winner",
        winner_variation: "treatment",
        note: "Clear lift on checkout.",
      },
    });
    const after = await readConfiguration(project, flag, "development");
    expect(after.rules).toEqual([
      {
        id: before.rules[0]?.id,
        description: "Beta testers",
        conditions: [],
        serve: { kind: "variation", variation: "treatment" },
      },
    ]);

    // Clone into a new draft with new salts.
    const source = await readExperiment(project, "cta_copy");
    await page.getByRole("button", { name: "Clone", exact: true }).click();
    dialog = page.getByRole("dialog", { name: "Clone cta_copy" });
    await dialog.getByLabel("Key").fill("cta_copy_rerun");
    await dialog.getByRole("button", { name: "Clone experiment" }).click();
    await expect(page.getByText("The draft cta_copy_rerun is created.")).toBeVisible();
    await expect(page).toHaveURL(/\/experiments\/cta_copy_rerun$/);
    await expect(page.getByText("Draft", { exact: true })).toBeVisible();
    const clone = await readExperiment(project, "cta_copy_rerun");
    expect(clone).toMatchObject({ status: "draft", name: "cta_copy", decision: null });
    expect(clone.inclusion_salt).not.toBe(source.inclusion_salt);
    expect(clone.variation_salt).not.toBe(source.variation_salt);
  });

  test("shows exposures per variation and day, conflicts and an assignment once the rollup has counted them", async ({
    page,
  }) => {
    const project = await seedProject("exp-health");
    const flag = "checkout_cta";
    await seedFlag(project, stringFlag(flag));
    await seedExperiment(project, experimentOn(flag, "cta_copy", { traffic_allocation: 10_000 }));
    // Staging serves the experiment, so its exposures open first.
    await writeConfiguration(project, flag, "staging", () => ({
      rules: [
        {
          description: "",
          conditions: [],
          serve: { kind: "experiment" as const, experiment: "cta_copy" },
        },
      ],
    }));
    const key = await seedSdkKey(project, "staging", { kind: "server", name: "Checkout" });
    const now = Date.now();
    const exposure = (unitId: string, variation: string, minutesAgo: number) => ({
      experiment: "cta_copy",
      flag,
      variation,
      unitType: "user",
      unitId,
      at: new Date(now - minutesAgo * 60_000),
    });
    const result = await postExposures(key, [
      exposure("user-1", "control", 10),
      exposure("user-2", "control", 9),
      exposure("user-3", "treatment", 8),
      // user-3 met control after its first exposure to treatment: a conflict.
      exposure("user-3", "control", 2),
      exposure("user-4", "ghost", 1),
    ]);
    expect(result.accepted).toBe(4);
    expect(result.rejected.map(({ code }) => code)).toEqual(["unknown_variation"]);

    await signIn(page, admin);
    await page.goto(`/projects/${project}/experiments/cta_copy`);
    const exposures = page.getByRole("region", { name: "Exposures" });
    await expect(exposures.getByRole("tab", { name: "Staging" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    // The rollup counts what was committed about every 10 seconds; the page
    // shows its counts once it has.
    const perVariation = exposures.getByRole("table", { name: "Per variation" });
    await expect(async () => {
      await page.reload();
      await expect(perVariation.getByRole("row", { name: /^control/ })).toHaveText(
        /control\s*2\s*66\.7%\s*50\.0%\s*3\s*0/,
        { timeout: 5_000 },
      );
    }).toPass({ timeout: 45_000 });
    await expect(perVariation.getByRole("row", { name: /^treatment/ })).toHaveText(
      /treatment\s*1\s*33\.3%\s*50\.0%\s*1\s*0/,
    );
    await expect(exposures.getByText("3 units first exposed, counted up to")).toBeVisible();
    await expect(exposures.getByRole("list", { name: "Refused exposures" })).toHaveText(
      /unknown_variation\s*1/,
    );
    const perDay = exposures.getByRole("table", { name: /Units first exposed per day/ });
    await expect(perDay.getByRole("img").first()).toHaveAccessibleName(/^Shares: control/);
    await expect(
      exposures.getByText(/^1 unit was exposed to a variation other than its first/),
    ).toBeVisible();
    await expect(
      exposures
        .getByRole("table", { name: /Conflicting units/ })
        .getByRole("row", { name: /user-3/ }),
    ).toHaveText(/user-3\s*user\s*treatment\s*control/);
    await expectNoSeriousViolations(page, "an experiment's exposures");

    // Development holds no exposure of it.
    await exposures.getByRole("tab", { name: "Development" }).click();
    await expect(page).toHaveURL(/environment=development/);
    await expect(
      exposures.getByText("No exposure has been received in this environment yet."),
    ).toBeVisible();

    // The lookup answers as the API does, with its query to copy.
    const expected = must(
      "looking up user-1",
      await getExperimentAssignment({
        client: managementClient(),
        path: { project, experiment: "cta_copy" },
        query: { unit_id: "user-1" },
      }),
    );
    const lookup = page.getByRole("region", { name: "Assignment lookup" });
    await lookup.getByLabel("Unit ID (user)").fill("user-1");
    await lookup.getByRole("button", { name: "Look up" }).click();
    await expect(lookup.getByRole("status").first()).toHaveText(
      `user-1 gets ${expected.variation}.`,
    );
    await expect(lookup.getByLabel("ClickHouse query")).toHaveText(expected.sql);
  });

  test("raises a sample-ratio mismatch on skewed exposures and hides its banner once acknowledged", async ({
    page,
  }) => {
    // Posting the exposures and waiting for the rollup to count them takes
    // most of the default time.
    test.setTimeout(90_000);
    const project = await seedProject("exp-srm");
    const flag = "checkout_cta";
    const key = "cta_copy";
    await seedFlag(project, stringFlag(flag));
    await seedExperiment(project, experimentOn(flag, key, { traffic_allocation: 10_000 }));
    await writeConfiguration(project, flag, "staging", () => ({
      rules: [
        {
          description: "",
          conditions: [],
          serve: { kind: "experiment" as const, experiment: key },
        },
      ],
    }));
    const client = managementClient();
    const draft = await readExperiment(project, key);
    const running = must(
      `starting ${key}`,
      await startExperiment({
        client,
        path: { project, experiment: key },
        headers: { "If-Match": `"${draft.version}"` },
      }),
    );

    // 800 units first exposed to control and 400 to treatment, where the
    // weights ask for an even split: far past what chance explains.
    const sdkKey = await seedSdkKey(project, "staging", { kind: "server", name: "Checkout" });
    const at = new Date(Date.now() - 60_000);
    const units = Array.from({ length: 1200 }, (_, index) => ({
      experiment: key,
      flag,
      variation: index < 800 ? "control" : "treatment",
      unitType: "user",
      unitId: `user-${index}`,
      at,
    }));
    for (let start = 0; start < units.length; start += 400) {
      const result = await postExposures(sdkKey, units.slice(start, start + 400));
      expect(result.accepted).toBe(400);
    }

    await signIn(page, admin);
    await page.goto(`/projects/${project}/experiments/${key}`);
    const exposures = page.getByRole("region", { name: "Exposures" });
    // The rollup counts the exposures within seconds; the check every 10
    // minutes would come too late for a test, so the stop takes its check
    // over what the rollup counted.
    await expect(async () => {
      await page.reload();
      await expect(exposures.getByText("1,200 units first exposed, counted up to")).toBeVisible({
        timeout: 5_000,
      });
    }).toPass({ timeout: 45_000 });
    must(
      `stopping ${key}`,
      await stopExperiment({
        client,
        path: { project, experiment: key },
        headers: { "If-Match": `"${running.version}"` },
        body: { outcome: "removed", note: "" },
      }),
    );

    await page.reload();
    const banners = page.getByRole("list", { name: "Active alerts" });
    await expect(banners.getByRole("listitem")).toHaveCount(1);
    await expect(banners).toContainText("Sample-ratio mismatch in Staging");
    await expect(banners).toContainText(
      "p-value < 0.0001; units first exposed: 1,200 (control 800, expected 600; treatment 400, expected 600).",
    );
    // The stop deleted the rule, so the page opens the project's first
    // environment; the check is Staging's.
    await exposures.getByRole("tab", { name: "Staging" }).click();
    const check = exposures.getByRole("region", { name: "Sample-ratio check" });
    await expect(check).toContainText("Mismatch: the units first exposed are out of line");
    // The next round of the checks would complete it, once the rollup has read
    // every exposure received before the stop.
    await expect(check).toContainText("Final check pending: this is the check the stop took on");
    await expectNoSeriousViolations(page, "an experiment's alerts");

    // The list marks it until someone acknowledges the alert.
    await page.goto(`/projects/${project}/experiments`);
    await expect(rowFor(page, key).getByRole("list", { name: "Needs attention" })).toHaveText(
      "1 alert",
    );
    await rowFor(page, key).getByRole("link").first().click();

    await banners.getByRole("button", { name: "Acknowledge" }).click();
    await expect(page.getByText("The alert is acknowledged.")).toBeVisible();
    await expect(banners).toHaveCount(0);
    const history = page.getByRole("table", { name: "Alerts" });
    await expect(history.getByRole("row", { name: /Sample-ratio mismatch/ })).toHaveText(
      /Sample-ratio mismatch\s*Staging.*Active/,
    );
    await page.goto(`/projects/${project}/experiments`);
    await expect(rowFor(page, key)).toBeVisible();
    await expect(rowFor(page, key).getByRole("list", { name: "Needs attention" })).toHaveCount(0);
  });

  test("tells a viewer which roles may create and edit experiments", async ({ page }) => {
    // A project of the test's own, so its writes stay out of the seeded
    // project's recent activity, which other specs read.
    const project = await seedProject("exp-viewer");
    const client = managementClient();
    const members = must(
      "finding the viewer",
      await listMembers({ client, query: { q: viewer.email } }),
    );
    const member = members.items.find((candidate) => candidate.email === viewer.email);
    if (!member) throw new Error(`${viewer.email} is no member`);
    must(
      "granting viewer",
      await createGrant({ client, path: { project }, body: { member: member.id, role: "viewer" } }),
    );
    const flag = "checkout_cta";
    const key = "cta_copy";
    await seedFlag(project, stringFlag(flag));
    await seedExperiment(project, experimentOn(flag, key));
    await openExperiments(page, project, viewer);

    await expect(page.getByRole("link", { name: "New experiment" }).first()).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await rowFor(page, key).getByRole("link").first().click();
    await expect(page.getByRole("heading", { level: 1, name: key })).toBeVisible();
    await expect(
      page.getByRole("status").filter({ hasText: "edit and run experiments" }),
    ).toBeVisible();
    await expect(page.getByLabel("Name")).toBeDisabled();
    await expect(page.getByLabel("Weight of variation 1 (%)")).toBeDisabled();
  });
});
