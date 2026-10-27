import { createExperiment, getExperiment, startExperiment } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  managementClient,
  must,
  seedProject,
  signIn,
  unprotectProduction,
} from "../support/console";
import { openFlag, seedFlag, stringFlag, writeConfiguration } from "../support/flags";
import { admin } from "../support/instance";
import { postEvaluationSummary, sdkRequest, seedSdkKey } from "../support/sdk";

/** The UTC day so many days before today, as the API writes a date. */
function daysAgo(days: number): string {
  return new Date(Date.now() - days * 86_400_000).toISOString().slice(0, 10);
}

/** The stale flags' row of one flag, by its key. */
const rowFor = (page: Page, key: string) =>
  page.getByRole("row").filter({ has: page.getByText(key, { exact: true }) });

/** A running experiment on a flag whose expected end passed two days ago. */
async function seedOverdueExperiment(project: string, flag: string, key: string) {
  const client = managementClient();
  must(
    `creating ${key}`,
    await createExperiment({
      client,
      path: { project },
      body: {
        key,
        name: key,
        hypothesis: "The shorter checkout converts better.",
        flag,
        unit_type: "user",
        variations: [
          { variation: "control", weight: 5000 },
          { variation: "treatment", weight: 5000 },
        ],
        expected_end_at: daysAgo(2),
      },
    }),
  );
  const draft = must(
    `reading ${key}`,
    await getExperiment({ client, path: { project, experiment: key } }),
  );
  must(
    `starting ${key}`,
    await startExperiment({
      client,
      path: { project, experiment: key },
      headers: { "If-Match": `"${draft.version}"` },
    }),
  );
}

test.describe("stale flags", () => {
  test("lists each stale flag with its reasons, filters them by reason and links to the flag", async ({
    page,
  }) => {
    const project = await seedProject("stale-flags");
    await unprotectProduction(project);
    await seedFlag(
      project,
      stringFlag("old_cleanup", {
        lifetime: "temporary",
        expected_removal_at: `${daysAgo(3)}T00:00:00Z`,
      }),
    );
    // On everywhere with no rule: every environment serves its fallthrough to everyone.
    await seedFlag(project, stringFlag("everyone_on"));
    for (const environment of ["development", "staging", "production"]) {
      await writeConfiguration(project, "everyone_on", environment, () => ({ on: true }));
    }
    await seedFlag(project, stringFlag("checkout_cta"));
    await seedOverdueExperiment(project, "checkout_cta", "cta_copy");
    await seedFlag(project, stringFlag("fresh_flag"));

    await signIn(page, admin);
    await page.goto(`/projects/${project}/flags`);
    await page.getByRole("link", { name: "Stale flags" }).first().click();
    await expect(page.getByRole("heading", { level: 1, name: "Stale flags" })).toBeVisible();

    await expect(rowFor(page, "old_cleanup")).toContainText("Past expected removal");
    await expect(rowFor(page, "everyone_on")).toContainText("Finished rollout");
    const overdue = rowFor(page, "checkout_cta");
    await expect(overdue).toContainText("Experiment overdue");
    await expect(overdue.getByRole("link", { name: "cta_copy" })).toHaveAttribute(
      "href",
      `/projects/${project}/experiments/cta_copy`,
    );
    await expect(rowFor(page, "fresh_flag")).toHaveCount(0);
    await expectNoSeriousViolations(page, "the stale flags");

    // Pressing a reason lists only the flags stale for it, and the address keeps it.
    const filter = page.getByRole("group", { name: "Stale for" });
    await filter.getByRole("button", { name: "Finished rollout" }).click();
    await expect(filter.getByRole("button", { name: "Finished rollout" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(rowFor(page, "old_cleanup")).toHaveCount(0);
    await expect(rowFor(page, "everyone_on")).toBeVisible();
    await page.reload();
    await expect(rowFor(page, "everyone_on")).toBeVisible();
    await expect(rowFor(page, "checkout_cta")).toHaveCount(0);

    await rowFor(page, "everyone_on")
      .getByRole("link", { name: /everyone_on/ })
      .click();
    await expect(page).toHaveURL(new RegExp(`/projects/${project}/flags/everyone_on$`));
    await expect(page.getByRole("heading", { level: 1, name: "everyone_on" })).toBeVisible();
  });

  test("says when no flag is stale", async ({ page }) => {
    const project = await seedProject("stale-none");
    await seedFlag(project, stringFlag("fresh_flag"));
    await signIn(page, admin);
    await page.goto(`/projects/${project}/stale-flags`);
    await expect(page.getByText("No flag is stale.")).toBeVisible();
  });
});

test.describe("evaluations and event volume", () => {
  test("show what SDKs reported apart from remote evaluations, and the events the project received", async ({
    page,
  }) => {
    // Waiting for the rollup's rounds to count the summary takes more than
    // the default time on a busy runner.
    test.setTimeout(120_000);
    const project = await seedProject("evaluations");
    const flag = "checkout_cta";
    await seedFlag(project, stringFlag(flag));
    const server = await seedSdkKey(project, "staging", { kind: "server", name: "Checkout" });
    await postEvaluationSummary(server, {
      flag,
      variation: "control",
      reason: "fallthrough",
      count: 7,
    });
    // A remote evaluation counts every flag the server answers, in its own series.
    await sdkRequest(server, "/evaluate", { body: { context: { units: { user: "u-1" } } } });

    await openFlag(page, project, flag, { environment: "staging" });
    const evaluations = page.getByRole("region", { name: "Evaluations" });
    // The rollup counts what was committed about every 10 seconds; the
    // section shows its counts once it has.
    await expect(async () => {
      await page.reload();
      await expect(
        evaluations.getByRole("img", { name: /^Reported by SDKs: 7 over 30 days/ }),
      ).toBeVisible({ timeout: 5_000 });
      await expect(
        evaluations.getByRole("img", { name: /^Remote evaluations: 1 over 30 days/ }),
      ).toBeVisible({ timeout: 5_000 });
    }).toPass({ timeout: 90_000 });
    await expect(evaluations.getByText(/^Last evaluated by an SDK:/)).not.toContainText("Never");

    await evaluations.getByText("Show as table").click();
    const table = evaluations.getByRole("table", { name: "Evaluations per day" });
    await expect(table.getByRole("row", { name: /Reported by SDKs/ })).toContainText("7");
    await expect(table.getByRole("row", { name: /Remote evaluations/ })).toContainText("1");

    // Another range is kept in the address and asked for again.
    await page.getByRole("combobox", { name: "Range" }).click();
    await page.getByRole("option", { name: "Last 7 days" }).click();
    await expect(page).toHaveURL(/days=7/);
    await expect(
      evaluations.getByRole("img", { name: /^Reported by SDKs: 7 over 7 days/ }),
    ).toBeVisible();
    await expectNoSeriousViolations(page, "a flag's evaluations");

    // Development has none.
    await page.getByRole("tab", { name: "Development" }).click();
    const none = page.getByRole("region", { name: "Evaluations" });
    await expect(none.getByText(/^Last evaluated by an SDK:/)).toContainText("Never");
    await expect(
      none.getByText("No SDK reported evaluating the flag here in these days."),
    ).toBeVisible();

    // The summary is one event the project received; remote evaluations are none.
    await page
      .getByRole("navigation", { name: "Project" })
      .getByRole("link", {
        name: "Event volume",
      })
      .click();
    await expect(page.getByRole("heading", { level: 1, name: "Event volume" })).toBeVisible();
    const legend = page.getByRole("list", { name: "Legend" });
    await expect(
      legend.getByRole("listitem").filter({ hasText: "Evaluation summaries" }),
    ).toHaveText(/Evaluation summaries\s*1/);
    await expect(legend.getByRole("listitem").filter({ hasText: "Exposures" })).toHaveText(
      /Exposures\s*0/,
    );
    await expect(page.getByRole("img", { name: /^Events per day: 1 over 30 days/ })).toBeVisible();
    await expect(page.getByText("No event was lost in these days.")).toBeVisible();
    await expectNoSeriousViolations(page, "the event volume");
  });
});
