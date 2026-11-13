import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { seedProject, signIn } from "../support/console";
import { runningExperimentInProduction } from "../support/experiments";
import { postExposures, seedSdkKey } from "../support/sdk";
import { startReceiver, type Receiver } from "../support/webhook-receiver";

/** The secret in the open reveal dialog, stored and closed. */
async function takeSecret(page: Page): Promise<string> {
  const reveal = page.getByRole("dialog", { name: "The sink's signing secret" });
  await expect(reveal).toBeVisible();
  await expect(reveal.getByRole("button", { name: "Close" })).toBeDisabled();
  await reveal.getByRole("button", { name: "Copy" }).click();
  await expect(reveal.getByText("Copied to the clipboard.")).toBeVisible();
  const secret = await page.evaluate(() => navigator.clipboard.readText());
  await expect(reveal.locator("code")).toHaveText(secret);
  await reveal.getByLabel("I have stored it somewhere safe", { exact: true }).check();
  await reveal.getByRole("button", { name: "Close" }).click();
  await expect(reveal).toBeHidden();
  await expect(page.getByText(secret)).toHaveCount(0);
  return secret;
}

/** The batches of one event kind a receiver was posted, the sample ones apart from the real. */
function batchesOf(receiver: Receiver, eventKind: string, sample: boolean) {
  return receiver.received
    .map(
      (delivery) =>
        JSON.parse(delivery.body) as { event_kind: string; sample: boolean; events: unknown[] },
    )
    .filter((batch) => batch.event_kind === eventKind && batch.sample === sample);
}

const sinkRow = (page: Page, name: string) =>
  page.getByRole("table", { name: "Sinks" }).getByRole("row", { name: new RegExp(name) });

test.describe("sinks", () => {
  test.beforeEach(async ({ context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  });

  test("a maintainer creates a sink of each kind, edits one keeping its credential, rotates a webhook secret and deletes", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    const project = await seedProject("sinks");
    await signIn(page);

    // The navigation lists Sinks, and the list starts empty.
    await page.goto(`/projects/${project}`);
    await page
      .getByRole("navigation", { name: "Project" })
      .getByRole("link", { name: "Sinks" })
      .click();
    await expect(page).toHaveURL(new RegExp(`/projects/${project}/sinks$`));
    await expect(page.getByRole("heading", { name: "Sinks", level: 1 })).toBeVisible();
    await expect(page.getByText("No sink yet.")).toBeVisible();
    await expectNoSeriousViolations(page, "the empty sinks list");

    // ClickHouse: the form refuses what is missing, then saves without testing the target.
    await page.getByRole("button", { name: "New sink" }).first().click();
    await expect(page).toHaveURL(new RegExp(`/projects/${project}/sinks/new$`));
    await expect(page.getByRole("radio", { name: /ClickHouse/ })).toBeChecked();
    await expect(page.getByLabel("Batch size", { exact: true })).toHaveAttribute(
      "placeholder",
      "5000",
    );
    await page.getByRole("button", { name: "Create sink" }).click();
    await expect(page.getByText("Enter a name.")).toBeVisible();
    await expectNoSeriousViolations(page, "the new ClickHouse sink form");
    await page.getByLabel("Name", { exact: true }).fill("Warehouse");
    await page
      .getByRole("textbox", { name: "URL", exact: true })
      .fill("https://clickhouse.example.com:8443");
    await page.getByLabel("Database", { exact: true }).fill("analytics");
    await page.getByLabel("Username", { exact: true }).fill("writer");
    await page.getByLabel("Password", { exact: true }).fill("warehouse-password");
    await page.getByRole("checkbox", { name: /Exposures/ }).check();
    await page.getByRole("button", { name: "Create sink" }).click();
    await expect(page).toHaveURL(new RegExp(`/projects/${project}/sinks/[0-9a-f-]{36}$`));
    await expect(page.getByRole("heading", { name: "Warehouse", level: 1 })).toBeVisible();

    // The credential is never shown and is kept when something else is edited.
    const password = page.getByLabel("Password", { exact: true });
    await expect(password).toHaveValue("");
    await expect(password).toHaveAttribute("placeholder", "***");
    await expect(page.getByLabel("Database", { exact: true })).toHaveValue("analytics");
    await expect(page.getByRole("checkbox", { name: /Exposures/ })).toBeChecked();
    await expectNoSeriousViolations(page, "a ClickHouse sink's settings");
    await page.getByLabel("Name", { exact: true }).fill("Analytics warehouse");
    await expect(page.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
    await page.getByRole("button", { name: "Save" }).first().click();
    await expect(page.getByText("Analytics warehouse is saved.")).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Analytics warehouse", level: 1 }),
    ).toBeVisible();
    await expect(page.getByLabel("Password", { exact: true })).toHaveAttribute(
      "placeholder",
      "***",
    );

    // Pointing the sink elsewhere needs the credential again: the server holds it and says so.
    await page
      .getByRole("textbox", { name: "URL", exact: true })
      .fill("https://other.example.com:8443");
    await page.getByRole("button", { name: "Save" }).first().click();
    await expect(page.getByText("Required.")).toBeVisible();
    await page.getByRole("button", { name: "Discard" }).click();
    await expect(page.getByRole("textbox", { name: "URL", exact: true })).toHaveValue(
      "https://clickhouse.example.com:8443",
    );

    // Object storage: its own fields and defaults, Parquet taking no compression.
    await page.goto(`/projects/${project}/sinks/new`);
    await page.getByRole("radio", { name: /Object storage/ }).check();
    await expect(page.getByLabel("Batch size", { exact: true })).toHaveAttribute(
      "placeholder",
      "500000",
    );
    await expect(page.getByLabel("Flush interval (seconds)", { exact: true })).toHaveAttribute(
      "placeholder",
      "300",
    );
    await page.getByLabel("Name", { exact: true }).fill("Lake");
    await page.getByLabel("Bucket", { exact: true }).fill("flagnetic-events");
    await page.getByLabel("Access key ID", { exact: true }).fill("AKIAEXAMPLE");
    await page.getByLabel("Secret access key", { exact: true }).fill("lake-secret-key");
    await page.getByLabel("Format", { exact: true }).click();
    await page.getByRole("option", { name: "Parquet" }).click();
    await expect(page.getByLabel("Compression", { exact: true })).toHaveCount(0);
    await expectNoSeriousViolations(page, "the new object storage sink form");
    await page.getByRole("button", { name: "Create sink" }).click();
    await expect(page.getByRole("heading", { name: "Lake", level: 1 })).toBeVisible();
    await expect(page.getByLabel("Secret access key", { exact: true })).toHaveAttribute(
      "placeholder",
      "***",
    );

    // A refusal of the server is explained under its field.
    await page.goto(`/projects/${project}/sinks/new`);
    await page.getByRole("radio", { name: /^Webhook/ }).check();
    await page.getByLabel("Name", { exact: true }).fill("Pipeline");
    await page
      .getByRole("textbox", { name: "URL", exact: true })
      .fill("https://hooks.example.com/flagnetic");
    await page.getByRole("button", { name: "Add a header" }).click();
    await page.getByLabel("Header name", { exact: true }).fill("X-Flagnetic-Id");
    await page.getByLabel("Header value", { exact: true }).fill("x");
    await page.getByRole("button", { name: "Create sink" }).click();
    await expect(page.getByText("The X-Flagnetic- headers are the platform's own.")).toBeVisible();
    await expect(page.getByLabel("Header name", { exact: true })).toHaveAttribute(
      "aria-invalid",
      "true",
    );

    // A webhook sink: its secret is shown once at creation.
    await page.getByLabel("Header name", { exact: true }).fill("Authorization");
    await page.getByLabel("Header value", { exact: true }).fill("Bearer pipeline-token");
    await page.getByRole("button", { name: "Create sink" }).click();
    const first = await takeSecret(page);
    await expect(page).toHaveURL(new RegExp(`/projects/${project}/sinks/[0-9a-f-]{36}$`));
    await expect(page.getByRole("heading", { name: "Pipeline", level: 1 })).toBeVisible();
    await expect(page.getByLabel("Header name", { exact: true })).toHaveValue("Authorization");

    // Rotating issues another, shown once as well.
    await page.getByRole("button", { name: "Rotate secret" }).click();
    const confirm = page.getByRole("dialog", { name: "Rotate the secret of Pipeline" });
    await confirm.getByRole("button", { name: "Rotate secret" }).click();
    const rotated = await takeSecret(page);
    expect(rotated).not.toBe(first);

    // The list shows all three, none enabled, and a sink is deleted after confirming.
    await page
      .getByRole("navigation", { name: "Project" })
      .getByRole("link", { name: "Sinks" })
      .click();
    await expect(sinkRow(page, "Analytics warehouse")).toContainText("ClickHouse");
    await expect(sinkRow(page, "Lake")).toContainText("Object storage");
    await expect(sinkRow(page, "Pipeline")).toContainText("Webhook");
    await expect(sinkRow(page, "Pipeline")).toContainText("Disabled");
    await expectNoSeriousViolations(page, "the sinks list");
    await useTheme(page, "dark");
    await page.reload();
    await expectNoSeriousViolations(page, "the sinks list in the dark");
    await useTheme(page, "light");
    await page.reload();

    await page.getByRole("button", { name: "Delete Lake" }).click();
    const deletion = page.getByRole("dialog", { name: "Delete Lake" });
    await deletion.getByRole("button", { name: "Delete sink" }).click();
    await expect(sinkRow(page, "Lake")).toHaveCount(0);
    await expect(sinkRow(page, "Pipeline")).toHaveCount(1);
  });
  test("a maintainer tests before the first save, sends a sample, enables, sees events delivered and disables", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    const receiver = await startReceiver();
    try {
      const project = await seedProject("sinks-detail");
      await runningExperimentInProduction(project);
      const key = await seedSdkKey(project, "production", { kind: "server", name: "Checkout" });
      await signIn(page);

      // A target that does not answer is reported by class, and saving is not held back.
      await page.goto(`/projects/${project}/sinks/new`);
      await page.getByRole("radio", { name: /^Webhook/ }).check();
      await page.getByLabel("Name", { exact: true }).fill("Pipeline");
      const url = page.getByRole("textbox", { name: "URL", exact: true });
      await url.fill("http://127.0.0.1:9/flagnetic");
      await page.getByRole("button", { name: "Test connection" }).click();
      await expect(page.getByText(/The test failed:/)).toBeVisible();
      await expectNoSeriousViolations(page, "a failed test of a new sink");

      // The receiver answers: the test passes, and it was sent a call carrying no rows.
      await url.fill(receiver.url);
      await page.getByRole("button", { name: "Test connection" }).click();
      await expect(page.getByText("The target answered and can take a batch.")).toBeVisible();
      expect(batchesOf(receiver, "test", false).length).toBeGreaterThan(0);
      await page.getByRole("button", { name: "Create sink" }).click();
      await takeSecret(page);
      await expect(page.getByRole("heading", { name: "Pipeline", level: 1 })).toBeVisible();

      // Before it is enabled: disabled, no lag, no streams yet.
      const summary = page.locator("dl");
      await expect(summary).toContainText("Disabled");
      await expect(
        page.getByText("Each event kind gets a stream once the sink is enabled."),
      ).toBeVisible();
      await expectNoSeriousViolations(page, "a disabled sink's page");

      // A saved sink is tested as stored.
      await page.getByRole("button", { name: "Test connection" }).click();
      await expect(page.getByText("The target answered and can take a batch.")).toBeVisible();

      // A sample reaches a disabled sink, marked as such, one batch per kind.
      await page.getByRole("button", { name: "Send sample" }).first().click();
      const sample = page.getByRole("dialog", { name: "Send a sample to Pipeline" });
      await expectNoSeriousViolations(page, "the sample dialog");
      await sample.getByRole("button", { name: "Send sample" }).click();
      await expect(sample.getByRole("listitem")).toHaveCount(3);
      await expect(sample.getByText("sent")).toHaveCount(3);
      await sample.getByRole("button", { name: "Close" }).first().click();
      expect(batchesOf(receiver, "exposure", true)).toHaveLength(1);
      expect(batchesOf(receiver, "exposure", true)[0]!.events).toHaveLength(10);

      // Enabling from now: the target is checked, and an exposure then reaches it.
      await page.getByRole("button", { name: "Enable", exact: true }).click();
      const enable = page.getByRole("dialog", { name: "Enable Pipeline" });
      await expectNoSeriousViolations(page, "the enable dialog");
      await enable.getByRole("button", { name: "Enable sink" }).click();
      await expect(page.getByText("Pipeline is enabled.")).toBeVisible();
      await expect(page.getByRole("button", { name: "Disable", exact: true })).toBeVisible();
      await postExposures(key, [
        {
          experiment: "cta_copy",
          flag: "checkout_cta",
          variation: "control",
          unitType: "user",
          unitId: "user-1",
          at: new Date(),
        },
      ]);
      await expect
        .poll(() => batchesOf(receiver, "exposure", false).length, { timeout: 30_000 })
        .toBe(1);
      expect(batchesOf(receiver, "exposure", false)[0]!.events).toHaveLength(1);

      // The streams table shows the delivery once the page reads the sink again.
      await page.reload();
      const streams = page.getByRole("table", { name: "Streams" });
      await expect(streams.getByRole("row", { name: /Exposures/ })).not.toContainText("Never");
      await expectNoSeriousViolations(page, "an enabled sink's page");

      // Disabling says the place is kept.
      await page.getByRole("button", { name: "Disable", exact: true }).click();
      const disable = page.getByRole("dialog", { name: "Disable Pipeline" });
      await expect(disable.getByText(/keeps its place in the event stream/)).toBeVisible();
      await disable.getByRole("button", { name: "Disable sink" }).click();
      await expect(page.getByRole("button", { name: "Enable", exact: true })).toBeVisible();
    } finally {
      await receiver.close();
    }
  });

  test("enabling a sink whose target does not answer is refused inline, naming the failure", async ({
    page,
  }) => {
    const project = await seedProject("sinks-refused");
    await signIn(page);
    await page.goto(`/projects/${project}/sinks/new`);
    await page.getByRole("radio", { name: /^Webhook/ }).check();
    await page.getByLabel("Name", { exact: true }).fill("Gone");
    await page.getByRole("textbox", { name: "URL", exact: true }).fill("http://127.0.0.1:9/gone");
    await page.getByRole("button", { name: "Create sink" }).click();
    await takeSecret(page);

    await page.getByRole("button", { name: "Enable", exact: true }).click();
    const enable = page.getByRole("dialog", { name: "Enable Gone" });
    await enable.getByRole("button", { name: "Enable sink" }).click();
    await expect(enable.getByText(/The sink is not ready to be enabled:/)).toBeVisible();
    await expectNoSeriousViolations(page, "a refused enable");
    await enable.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("button", { name: "Enable", exact: true })).toBeVisible();
  });
});
