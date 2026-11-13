import { createSink, enableSink, getSink } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedProject, signIn } from "../support/console";
import { runningExperimentInProduction } from "../support/experiments";
import { postExposures, seedSdkKey } from "../support/sdk";
import { type Receiver, startReceiver } from "../support/webhook-receiver";

const exposure = (unitId: string) => ({
  experiment: "cta_copy",
  flag: "checkout_cta",
  variation: "control",
  unitType: "user",
  unitId,
  at: new Date(),
});

/** How many times each unit arrived in a real batch the receiver acknowledged since `since` deliveries. */
function acknowledgedUnits(receiver: Receiver, since = 0): Map<string, number> {
  const counts = new Map<string, number>();
  for (const delivery of receiver.received.slice(since)) {
    if (!delivery.acknowledged) continue;
    const batch = JSON.parse(delivery.body) as {
      event_kind?: string;
      sample?: boolean;
      events?: { unit_id: string }[];
    };
    if (batch.sample || batch.event_kind !== "exposure") continue;
    for (const row of batch.events ?? []) {
      counts.set(row.unit_id, (counts.get(row.unit_id) ?? 0) + 1);
    }
  }
  return counts;
}

/** A `datetime-local` value in the browser's time zone, which is the runner's. */
function localInput(at: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}T${pad(at.getHours())}:${pad(at.getMinutes())}`;
}

const banner = (page: Page) =>
  page
    .getByRole("status")
    .filter({ hasText: "need your attention" })
    .or(page.getByRole("status").filter({ hasText: "needs your attention" }));

/** Opens each tab of a sink's page in a theme and checks it against the accessibility rules. */
async function checkTabs(page: Page, theme: "light" | "dark") {
  for (const tab of ["Settings", /^Dead letters/, "Gaps", "Replay"]) {
    await page.getByRole("tab", { name: tab }).click();
    await expect(page.getByRole("tabpanel")).toBeVisible();
    await expectNoSeriousViolations(page, `a sink's ${String(tab)} tab (${theme})`);
  }
}

test.describe("sink recovery", () => {
  test("dead letters stop the sink; redeliver, resume and replay bring the rows back", async ({
    page,
  }) => {
    test.setTimeout(150_000);
    const receiver = await startReceiver();
    try {
      const project = await seedProject("recovery");
      await runningExperimentInProduction(project);
      const key = await seedSdkKey(project, "production", { kind: "server", name: "Checkout" });
      const client = managementClient();
      const sink = must(
        "creating the sink",
        await createSink({
          client,
          path: { project },
          body: {
            name: "Warehouse hook",
            kind: "webhook",
            webhook: { url: receiver.url },
            event_kinds: ["exposure"],
            batch_size: 1,
            flush_interval: 1,
          },
        }),
      );
      must(
        "enabling the sink",
        await enableSink({ client, path: { project, sink: sink.id }, body: {} }),
      );
      const before = new Date(Date.now() - 60_000);

      // Every batch is rejected: two dead letters in a row stop the sink.
      receiver.status = 422;
      await postExposures(key, [exposure("r-1"), exposure("r-2")]);
      await expect
        .poll(
          async () =>
            must("reading the sink", await getSink({ client, path: { project, sink: sink.id } }))
              .health,
          { timeout: 60_000 },
        )
        .toBe("stopped");

      // Outside the sinks screens: the banner links to the sink and the navigation is marked.
      await signIn(page);
      await page.goto(`/projects/${project}/flags`);
      await expect(banner(page)).toContainText("1 sink needs your attention.");
      await expect(banner(page)).toContainText("stopped");
      await expect(
        page
          .getByRole("navigation", { name: "Project" })
          .getByRole("link", { name: /Sinks/ })
          .getByText("1 sink needs your attention"),
      ).toBeAttached();
      await expectNoSeriousViolations(page, "the sinks banner");
      await banner(page).getByRole("link", { name: "Warehouse hook" }).click();

      const stopped = page.getByRole("alert").filter({ hasText: "Stopped by the platform" });
      await expect(stopped).toContainText("Exposures stream");
      await expect(stopped).toContainText("422");
      await expectNoSeriousViolations(page, "a stopped sink");

      await page.getByRole("tab", { name: "Dead letters (2)" }).click();
      await expect(page).toHaveURL(/tab=dead-letters/);
      const letters = page.getByRole("table", { name: "Dead letters" }).getByRole("row");
      await expect(letters).toHaveCount(3);
      await expect(letters.nth(1)).toContainText("Pending");
      await expect(letters.nth(1)).toContainText("422");
      // Both rejections of the batch are counted.
      await expect(letters.nth(1).getByRole("cell").nth(3)).toHaveText("2");

      // The receiver is fixed and takes the first dead letter again; the second is discarded.
      receiver.status = 200;
      await letters
        .nth(1)
        .getByRole("button", { name: /Redeliver/ })
        .click();
      await expect(page.getByRole("row", { name: /Redelivered/ })).toHaveCount(1);
      expect([...acknowledgedUnits(receiver).keys()]).toHaveLength(1);
      await page
        .getByRole("row", { name: /Pending/ })
        .getByRole("button", { name: /Discard/ })
        .click();
      const discard = page.getByRole("dialog", { name: "Discard this dead letter" });
      await expectNoSeriousViolations(page, "the discard dialog");
      await discard.getByRole("button", { name: "Discard dead letter" }).click();
      await expect(page.getByRole("row", { name: /Pending/ })).toHaveCount(0);
      await expect(page.getByRole("tab", { name: "Dead letters", exact: true })).toBeVisible();

      // Resumed, the sink delivers what comes next, and the banner goes.
      await stopped.getByRole("button", { name: "Resume" }).click();
      await expect(stopped).toBeHidden();
      await expect(banner(page)).toBeHidden();
      await postExposures(key, [exposure("r-3")]);
      await expect
        .poll(() => acknowledgedUnits(receiver).get("r-3") ?? 0, { timeout: 30_000 })
        .toBe(1);

      // A replay from before the first exposure sends everything still stored again.
      await page.getByRole("tab", { name: "Replay" }).click();
      const delivered = receiver.received.length;
      await page.getByLabel("From", { exact: true }).fill(localInput(before));
      await expectNoSeriousViolations(page, "the replay form");
      await page.getByRole("button", { name: "Replay…" }).click();
      const confirm = page.getByRole("dialog", { name: "Replay Warehouse hook" });
      await expect(confirm).toContainText("received at or after");
      await expectNoSeriousViolations(page, "the replay confirmation");
      await confirm.getByRole("button", { name: "Replay", exact: true }).click();
      await expect(confirm).toBeHidden();
      await expect
        .poll(() => [...acknowledgedUnits(receiver, delivered).keys()].sort(), { timeout: 30_000 })
        .toEqual(["r-1", "r-2", "r-3"]);

      // Every tab, in both themes.
      await useTheme(page, "light");
      await page.reload();
      await checkTabs(page, "light");
      await useTheme(page, "dark");
      await page.reload();
      await checkTabs(page, "dark");
      await useTheme(page, "light");
    } finally {
      await receiver.close();
    }
  });
});
