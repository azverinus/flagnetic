import {
  createExperiment,
  createGrant,
  type ExperimentCreate,
  getExperiment,
  listMembers,
  updateExperiment,
} from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { type Credentials, managementClient, must, seedProject, signIn } from "../support/console";
import { choose, seedFlag, stringFlag, writeConfiguration } from "../support/flags";
import { admin, viewer } from "../support/instance";

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
