import {
  archiveFlag,
  createChangeRequest,
  createExperiment,
  createGrant,
  getChangeRequest,
  getExperiment,
} from "@flagnetic/api";
import { type Browser, expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  protectProduction,
  seedMember,
  seedProject,
  signIn,
  unprotectProduction,
} from "../support/console";
import {
  choose,
  readConfiguration,
  readFlag,
  seedFlag,
  stringFlag,
  writeConfiguration,
} from "../support/flags";
import { runningExperimentInProduction } from "../support/experiments";
import { advanceClock } from "../support/instance";

const password = "harness-saffron-tundra-2093";

/** A maintainer of the project in a browser of their own: the second person approval needs. */
async function reviewer(browser: Browser, project: string): Promise<Page> {
  const who: Credentials & { id: string } = await seedMember("rosa", "Rosa Reviewer", password);
  must(
    `granting maintainer in ${project}`,
    await createGrant({
      client: managementClient(),
      path: { project },
      body: { member: who.id, role: "maintainer" },
    }),
  );
  const page = await (await browser.newContext()).newPage();
  await signIn(page, who);
  return page;
}

/** A time ahead of the clock as a `datetime-local` input reads it, in the browser's zone. */
function inputTimeIn(minutes: number): string {
  const at = new Date(Date.now() + minutes * 60_000);
  const two = (n: number) => String(n).padStart(2, "0");
  return `${at.getFullYear()}-${two(at.getMonth() + 1)}-${two(at.getDate())}T${two(at.getHours())}:${two(at.getMinutes())}`;
}

/** Waits, reading the API, until the change request is in a state the scheduler brings it to. */
async function untilChangeRequest(
  project: string,
  id: string,
  reached: (changeRequest: Awaited<ReturnType<typeof readChangeRequest>>) => boolean,
): Promise<void> {
  await expect
    .poll(async () => reached(await readChangeRequest(project, id)), { timeout: 40_000 })
    .toBe(true);
}

async function readChangeRequest(project: string, id: string) {
  return must(
    `reading change request ${id}`,
    await getChangeRequest({ client: managementClient(), path: { project, change_request: id } }),
  );
}

async function approve(page: Page, project: string, id: string): Promise<void> {
  await page.goto(`/projects/${project}/change-requests/${id}`);
  await page.getByRole("button", { name: "Approve", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Approve the change request" })
    .getByRole("button", { name: "Approve" })
    .click();
  await expect(page.getByText(/Approved by /)).toBeVisible();
}

test.describe("scheduled changes", () => {
  // The scheduler works on the binary's clock, which a spec moves ahead
  // and puts back, since every later spec shares the instance.
  test.afterEach(() => advanceClock("0s"));

  test("a start refused for a protected holder is proposed for a time, approved, and applied then", async ({
    page,
    browser,
  }) => {
    test.setTimeout(90_000);
    const project = await seedProject("scheduled-start");
    await seedFlag(project, stringFlag("checkout_cta"));
    const client = managementClient();
    must(
      "creating cta_copy",
      await createExperiment({
        client,
        path: { project },
        body: {
          key: "cta_copy",
          name: "CTA copy",
          hypothesis: "The shorter checkout converts better.",
          flag: "checkout_cta",
          unit_type: "user",
          variations: [
            { variation: "control", weight: 5000 },
            { variation: "treatment", weight: 5000 },
          ],
          expected_end_at: "2027-01-31",
        },
      }),
    );
    // Production holds the experiment, and is protected again after the
    // rule serving it is written.
    await unprotectProduction(project);
    await writeConfiguration(project, "checkout_cta", "production", () => ({
      rules: [
        { description: "", conditions: [], serve: { kind: "experiment", experiment: "cta_copy" } },
      ],
    }));
    await protectProduction(project);

    // The start is refused and becomes a proposal, with a time to apply it.
    await signIn(page);
    await page.goto(`/projects/${project}/experiments/cta_copy`);
    await page.getByRole("button", { name: "Start", exact: true }).click();
    await page
      .getByRole("dialog", { name: "Start cta_copy" })
      .getByRole("button", { name: "Start experiment" })
      .click();
    const dialog = page.getByRole("dialog", { name: "Propose the change" });
    await expect(dialog).toContainText("An environment holding cta_copy is protected");
    await expect(dialog.getByRole("list", { name: "Environments changed" })).toHaveText(
      "Production",
    );
    await dialog.getByLabel("Title").fill("Start the copy test tonight");
    await dialog.getByLabel("Apply at").fill(inputTimeIn(30));
    await expectNoSeriousViolations(page, "the propose dialog of an experiment start");
    await dialog.getByRole("button", { name: "Propose", exact: true }).click();
    await expect(page).toHaveURL(/\/change-requests\/[0-9a-f-]{36}$/);
    const id = page.url().slice(-36);

    await expect(
      page.getByRole("heading", { name: "Start the copy test tonight", level: 1 }),
    ).toBeVisible();
    await expect(page.getByText(/Starts the experiment/)).toBeVisible();
    await expect(page.getByText("Applies at", { exact: true })).toBeVisible();
    await expect(page.getByText("once approved")).toBeVisible();
    await expect(page.getByRole("link", { name: "cta_copy" })).toBeVisible();
    await expectNoSeriousViolations(page, "a scheduled experiment change request");
    expect(await readExperimentStatus(project, "cta_copy")).toBe("draft");

    // The list tells scheduled from unscheduled ones.
    await page.goto(`/projects/${project}/change-requests`);
    await expect(page.getByRole("link", { name: "Start the copy test tonight" })).toBeVisible();
    await page.getByRole("combobox", { name: "Schedule", exact: true }).click();
    await page.getByRole("option", { name: "Not scheduled", exact: true }).click();
    await expect(page).toHaveURL(/scheduled=false/);
    await expect(page.getByText("No change request matches these filters.")).toBeVisible();
    await page.getByRole("combobox", { name: "Schedule", exact: true }).click();
    await page.getByRole("option", { name: "Scheduled", exact: true }).click();
    await expect(page).toHaveURL(/scheduled=true/);
    await expect(page.getByRole("link", { name: "Start the copy test tonight" })).toBeVisible();
    await expectNoSeriousViolations(page, "the scheduled change requests");

    // Once approved, it waits for its time and is applied by the system.
    const rosa = await reviewer(browser, project);
    await approve(rosa, project, id);
    expect(await readExperimentStatus(project, "cta_copy")).toBe("draft");
    advanceClock("1h");
    await untilChangeRequest(project, id, (changeRequest) => changeRequest.state === "applied");
    expect(await readExperimentStatus(project, "cta_copy")).toBe("running");

    await page.goto(`/projects/${project}/change-requests/${id}`);
    await expect(page.getByText("at its scheduled time")).toBeVisible();
  });

  test("a stop refused for a protected holder is proposed, and its diff shows the rule it rewrites", async ({
    page,
  }) => {
    const project = await seedProject("experiment-stop");
    await runningExperimentInProduction(project);

    await signIn(page);
    await page.goto(`/projects/${project}/experiments/cta_copy`);
    await page.getByRole("button", { name: "Stop", exact: true }).click();
    const stop = page.getByRole("dialog", { name: "Stop cta_copy" });
    await choose(page, "Winning variation", "treatment");
    await stop.getByLabel("Note").fill("Treatment won clearly.");
    await stop.getByRole("button", { name: "Stop experiment" }).click();
    const dialog = page.getByRole("dialog", { name: "Propose the change" });
    await expect(dialog).toContainText("An environment holding cta_copy is protected");
    await dialog.getByLabel("Title").fill("Ship the treatment");
    await dialog.getByRole("button", { name: "Propose", exact: true }).click();
    await expect(page).toHaveURL(/\/change-requests\/[0-9a-f-]{36}$/);

    // The toast is checked once it has faded in: halfway, its text is paler.
    await expect(
      page
        .locator("[data-sonner-toast]")
        .filter({ hasText: "The change request is open for review." }),
    ).toHaveCSS("opacity", "1");
    await expect(page.getByText("Stops the experiment and ships the winner")).toBeVisible();
    await expect(page.getByText("Treatment won clearly.")).toBeVisible();
    await expect(
      page.getByRole("list", { name: "Rules rewritten" }).getByRole("listitem"),
    ).toHaveText(["Production, experiment rule 1: serves treatment, its conditions kept"]);
    await expectNoSeriousViolations(page, "an experiment stop change request");
    expect(await readExperimentStatus(project, "cta_copy")).toBe("running");
  });

  test("a failed scheduled application shows why, until it is cancelled", async ({ browser }) => {
    test.setTimeout(90_000);
    const project = await seedProject("scheduled-failure");
    await seedFlag(project, stringFlag("gate"));
    await seedFlag(project, stringFlag("checkout_copy"));
    const client = managementClient();
    // The change request names the flag gate as a prerequisite, which
    // is archived after the approval, so applying it is refused.
    const scheduled = must(
      "scheduling a change request",
      await createChangeRequest({
        client,
        path: { project },
        body: {
          kind: "flag_configuration",
          flag: "checkout_copy",
          environment: "production",
          title: "Serve the new copy at night",
          proposed: {
            on: true,
            off_variation: "control",
            fallthrough: "treatment",
            prerequisites: [{ flag: "gate", variation: "treatment" }],
          },
          apply_at: new Date(Date.now() + 30 * 60_000).toISOString(),
          open: true,
        },
      }),
    );
    const rosa = await reviewer(browser, project);
    await approve(rosa, project, scheduled.id);
    const gate = await readFlag(project, "gate");
    must(
      "archiving gate",
      await archiveFlag({
        client,
        path: { project, flag: "gate" },
        headers: { "If-Match": `"${gate.version}"` },
      }),
    );
    advanceClock("1h");
    await untilChangeRequest(
      project,
      scheduled.id,
      (changeRequest) => changeRequest.schedule_failure !== null,
    );

    await rosa.goto(`/projects/${project}/change-requests/${scheduled.id}`);
    const failure = rosa.getByRole("region", { name: "The scheduled application failed" });
    await expect(failure).toBeVisible();
    await expect(failure.getByText("invalid_configuration")).toBeVisible();
    await expect(failure.getByText("Attempts failed in a row")).toBeVisible();
    await expect(failure.getByText(/cancel this change request and copy it/i)).toBeVisible();
    await expect(rosa.getByText("Approved", { exact: true }).first()).toBeVisible();
    await expectNoSeriousViolations(rosa, "a change request whose schedule failed");

    // The list marks it as failed.
    await rosa.goto(`/projects/${project}/change-requests?scheduled=true`);
    await expect(
      rosa.getByRole("row").filter({ hasText: "Serve the new copy at night" }),
    ).toContainText("Failed");

    // Cancelling clears the failure.
    await rosa.goto(`/projects/${project}/change-requests/${scheduled.id}`);
    await rosa.getByRole("button", { name: "Cancel change request", exact: true }).click();
    await rosa
      .getByRole("dialog", { name: "Cancel the change request" })
      .getByRole("button", { name: "Cancel change request" })
      .click();
    await expect(rosa.getByText("Cancelled", { exact: true }).first()).toBeVisible();
    await expect(failure).toHaveCount(0);
    await expect(rosa.getByText("Applies at", { exact: true })).toHaveCount(0);
  });

  test("a scheduled change cancelled before its time is never applied", async ({ browser }) => {
    test.setTimeout(90_000);
    const project = await seedProject("scheduled-cancel");
    await seedFlag(project, stringFlag("checkout_copy"));
    await seedFlag(project, stringFlag("banner"));
    const client = managementClient();
    const schedule = async (flag: string, title: string) =>
      must(
        `scheduling ${title}`,
        await createChangeRequest({
          client,
          path: { project },
          body: {
            kind: "flag_configuration",
            flag,
            environment: "production",
            title,
            proposed: { on: true, off_variation: "control", fallthrough: "treatment" },
            apply_at: new Date(Date.now() + 30 * 60_000).toISOString(),
            open: true,
          },
        }),
      );
    const cancelled = await schedule("checkout_copy", "Serve the new copy at night");
    // A second change request due at the same time shows the scheduler has
    // passed that time, so the first one not applying is not just a delay.
    const sentinel = await schedule("banner", "Show the banner at night");
    const rosa = await reviewer(browser, project);
    await approve(rosa, project, cancelled.id);
    await approve(rosa, project, sentinel.id);

    await rosa.goto(`/projects/${project}/change-requests/${cancelled.id}`);
    await expect(rosa.getByText("Applies at", { exact: true })).toBeVisible();
    await rosa.getByRole("button", { name: "Cancel change request", exact: true }).click();
    await rosa
      .getByRole("dialog", { name: "Cancel the change request" })
      .getByRole("button", { name: "Cancel change request" })
      .click();
    await expect(rosa.getByText("Cancelled", { exact: true }).first()).toBeVisible();
    await expect(rosa.getByText("Applies at", { exact: true })).toHaveCount(0);

    advanceClock("1h");
    await untilChangeRequest(
      project,
      sentinel.id,
      (changeRequest) => changeRequest.state === "applied",
    );
    expect((await readChangeRequest(project, cancelled.id)).state).toBe("cancelled");
    expect((await readConfiguration(project, "checkout_copy", "production")).on).toBe(false);
  });
});

async function readExperimentStatus(project: string, experiment: string): Promise<string> {
  return must(
    `reading ${experiment}`,
    await getExperiment({ client: managementClient(), path: { project, experiment } }),
  ).status;
}
