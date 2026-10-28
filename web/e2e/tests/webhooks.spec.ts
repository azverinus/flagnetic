import { createGrant, createWebhook, getWebhook, killFlag, testWebhook } from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations, useTheme } from "../support/axe";
import { managementClient, must, seedMember, seedProject, signIn } from "../support/console";
import { seedFlag, stringFlag, writeConfiguration } from "../support/flags";
import { advanceClock, freePort } from "../support/instance";
import { type Receiver, signedWith, startReceiver } from "../support/webhook-receiver";

/** Waits until the receiver holds `count` deliveries of one event type. */
async function receivedOf(receiver: Receiver, type: string, count = 1) {
  await expect
    .poll(() => receiver.received.filter((delivery) => delivery.type === type).length, {
      timeout: 15_000,
    })
    .toBeGreaterThanOrEqual(count);
  return receiver.received.filter((delivery) => delivery.type === type);
}

/** The secret in the open reveal dialog, stored and closed. */
async function takeSecret(page: Page): Promise<string> {
  const reveal = page.getByRole("dialog", { name: "The webhook's signing secret" });
  await expect(reveal).toBeVisible();
  await expect(reveal.getByRole("button", { name: "Close" })).toBeDisabled();
  await reveal.getByRole("button", { name: "Copy" }).click();
  await expect(reveal.getByText("Copied to the clipboard.")).toBeVisible();
  const secret = await page.evaluate(() => navigator.clipboard.readText());
  await expect(reveal.locator("code")).toHaveText(secret);
  await reveal.getByLabel("I have stored it somewhere safe").check();
  await reveal.getByRole("button", { name: "Close" }).click();
  await expect(reveal).toBeHidden();
  await expect(page.getByText(secret)).toHaveCount(0);
  return secret;
}

const webhookRow = (page: Page, name: string) =>
  page.getByRole("table", { name: "Webhooks" }).getByRole("row", { name: new RegExp(name) });

test.describe("webhooks", () => {
  let receiver: Receiver;

  test.beforeEach(async ({ context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    receiver = await startReceiver();
  });

  test.afterEach(async () => {
    await receiver.close();
  });

  test("a maintainer subscribes, tests, reads the log, redelivers, rotates and deletes", async ({
    page,
  }) => {
    test.setTimeout(90_000);
    const project = await seedProject("webhooks");
    await seedFlag(project, stringFlag("checkout_banner"));
    await writeConfiguration(project, "checkout_banner", "development", () => ({ on: true }));
    await signIn(page);

    await page.goto(`/projects/${project}/webhooks`);
    await expect(page.getByRole("heading", { name: "Webhooks", level: 1 })).toBeVisible();
    await expect(page.getByText("No webhook yet.")).toBeVisible();

    // Create: the secret is shown once, copied, and signs every delivery.
    await page.getByRole("button", { name: "New webhook" }).click();
    const create = page.getByRole("dialog", { name: "New webhook" });
    await create.getByRole("button", { name: "Create webhook" }).click();
    await expect(create.getByText("Enter a name.")).toBeVisible();
    await expect(create.getByText("Choose at least one event type.")).toBeVisible();
    await create.getByLabel("Name").fill("Release bot");
    await create.getByLabel("URL").fill(receiver.url);
    await create.getByRole("checkbox", { name: /flag\.kill_switch/ }).check();
    await create.getByRole("button", { name: "Create webhook" }).click();
    const secret = await takeSecret(page);
    await expect(page).toHaveURL(new RegExp(`/projects/${project}/webhooks/[0-9a-f-]+`));
    await expect(page.getByRole("heading", { name: "Release bot", level: 1 })).toBeVisible();

    // A test: the receiver gets a ping signed with the secret, and the log shows it.
    await page.getByRole("button", { name: "Send test" }).click();
    await expect(page.getByRole("tab", { name: "Deliveries", selected: true })).toBeVisible();
    const [ping] = await receivedOf(receiver, "ping");
    expect(signedWith(ping!, secret)).toBe(true);
    expect(signedWith(ping!, "not-the-secret")).toBe(false);
    const pingRow = page.getByRole("row").filter({ hasText: "ping" });
    await expect(pingRow).toContainText("Succeeded", { timeout: 15_000 });
    await expect(pingRow).toContainText("200");

    // A kill switch is delivered as flag.kill_switch.
    must(
      "killing the flag",
      await killFlag({
        client: managementClient(),
        path: { project, flag: "checkout_banner", environment: "development" },
        body: { reason: "checkout 500s" },
      }),
    );
    const [killed] = await receivedOf(receiver, "flag.kill_switch");
    expect(signedWith(killed!, secret)).toBe(true);
    const killRow = page.getByRole("row").filter({ hasText: "flag.kill_switch" });
    await expect(killRow).toContainText("Succeeded", { timeout: 15_000 });
    await expectNoSeriousViolations(page, "a webhook's delivery log");

    // The delivery opens with the ids the receiver saw and what it answered.
    await page.getByRole("button", { name: "Open the flag.kill_switch delivery" }).click();
    const sheet = page.getByRole("dialog", { name: "flag.kill_switch" });
    await expect(sheet).toContainText(String(killed!.headers["x-flagnetic-event"]));
    await expect(sheet).toContainText(String(killed!.headers["x-flagnetic-delivery"]));
    await expect(sheet).toContainText("received");
    await expectNoSeriousViolations(page, "a delivery's attempts");
    await sheet.getByRole("button", { name: "Close" }).click();
    await expect(sheet).toBeHidden();

    // Redeliver the kill switch: the same event under a new delivery.
    await page.getByRole("button", { name: "Redeliver the flag.kill_switch delivery" }).click();
    const both = await receivedOf(receiver, "flag.kill_switch", 2);
    expect(both[1]!.headers["x-flagnetic-event"]).toBe(both[0]!.headers["x-flagnetic-event"]);
    expect(both[1]!.headers["x-flagnetic-delivery"]).not.toBe(
      both[0]!.headers["x-flagnetic-delivery"],
    );
    expect(both[1]!.body).toBe(both[0]!.body);
    await expect(page.getByRole("row").filter({ hasText: "flag.kill_switch" })).toHaveCount(2, {
      timeout: 15_000,
    });

    // The log narrows by event type, and the address says so.
    await page.getByRole("combobox", { name: "Event type" }).click();
    await page.getByRole("option", { name: "flag.kill_switch", exact: true }).click();
    await expect(page).toHaveURL(/event_type=flag.kill_switch/);
    await expect(page.getByRole("row").filter({ hasText: "ping" })).toHaveCount(0);
    await expect(page.getByRole("row").filter({ hasText: "flag.kill_switch" })).toHaveCount(2);

    // Rotating issues a new secret; the old one signs beside it for a day.
    await page.getByRole("button", { name: "Rotate secret" }).click();
    const confirm = page.getByRole("dialog", { name: "Rotate the secret of Release bot" });
    await confirm.getByRole("button", { name: "Rotate secret" }).click();
    const rotated = await takeSecret(page);
    expect(rotated).not.toBe(secret);
    await page.getByRole("button", { name: "Send test" }).click();
    await expect
      .poll(() => receiver.received.filter((delivery) => delivery.type === "ping").length, {
        timeout: 15_000,
      })
      .toBe(2);
    const second = receiver.received.filter((delivery) => delivery.type === "ping")[1]!;
    expect(signedWith(second, rotated)).toBe(true);
    expect(signedWith(second, secret)).toBe(true);

    // Editing points it somewhere else.
    await page.getByRole("tab", { name: "Settings" }).click();
    await page.getByLabel("Name").fill("Deploy bot");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Deploy bot is saved.")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Deploy bot", level: 1 })).toBeVisible();

    // The list shows it enabled, and deleting it from the screen empties the list.
    await page.goto(`/projects/${project}/webhooks`);
    await expect(webhookRow(page, "Deploy bot")).toContainText("Enabled");
    await page.getByRole("link", { name: "Deploy bot" }).click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    const deletion = page.getByRole("dialog", { name: "Delete Deploy bot" });
    await deletion.getByRole("button", { name: "Delete webhook" }).click();
    await expect(page).toHaveURL(new RegExp(`/projects/${project}/webhooks$`));
    await expect(page.getByText("No webhook yet.")).toBeVisible();
  });

  test("the platform disables a failing webhook, the banner tells those who manage webhooks, and enabling clears it", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    const project = await seedProject("webhooks-disabled");
    const viewer = await seedMember("hook-viewer", "Vera Viewer", "harness-cobalt-lantern-2031");
    must(
      "granting viewer",
      await createGrant({
        client: managementClient(),
        path: { project },
        body: { member: viewer.id, role: "viewer" },
      }),
    );
    const client = managementClient();
    const dead = `http://127.0.0.1:${await freePort()}/gone`;
    const created = must(
      "creating the webhook",
      await createWebhook({
        client,
        path: { project },
        body: { name: "Release bot", url: dead, events: ["flag.kill_switch"] },
      }),
    );

    // Twenty-five failed calls and a day without a success disable it: the
    // pings fail at once, and the clock stands a day and an hour ahead.
    advanceClock("25h");
    try {
      for (let ping = 0; ping < 25; ping++) {
        await testWebhook({ client, path: { project, webhook: created.id } });
      }
      await expect
        .poll(
          async () =>
            must(
              "reading the webhook",
              await getWebhook({ client, path: { project, webhook: created.id } }),
            ).enabled,
          { timeout: 60_000 },
        )
        .toBe(false);
    } finally {
      advanceClock("0s");
    }

    // A viewer cannot manage webhooks, so no banner and no screen.
    await signIn(page, viewer);
    await page.goto(`/projects/${project}/flags`);
    await expect(page.getByRole("heading", { name: "Flags", level: 1 })).toBeVisible();
    await expect(page.getByText(/The platform disabled/)).toHaveCount(0);

    // A maintainer is told on every screen of the project.
    await page.context().clearCookies();
    await signIn(page);
    await useTheme(page, "dark");
    await page.goto(`/projects/${project}/flags`);
    const banner = page.getByRole("status").filter({ hasText: "The platform disabled 1 webhook" });
    await expect(banner).toBeVisible();
    await expectNoSeriousViolations(page, "the disabled webhooks banner");

    await page.goto(`/projects/${project}/webhooks`);
    const row = webhookRow(page, "Release bot");
    await expect(row).toContainText("Disabled after persistent failure");
    await expect(row).toContainText("25");
    await expectNoSeriousViolations(page, "the webhooks list with a disabled webhook");

    await row.getByRole("button", { name: "Re-enable Release bot" }).click();
    await expect(row).toContainText("Enabled");
    await expect(row).not.toContainText("Disabled after persistent failure");
    await expect(banner).toHaveCount(0);
  });

  test("an instance admin subscribes to the audit entries that name no project", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/admin/webhooks");
    await expect(page.getByRole("heading", { name: "Instance webhooks", level: 1 })).toBeVisible();

    await page.getByRole("button", { name: "New webhook" }).click();
    const dialog = page.getByRole("dialog", { name: "New webhook" });
    await expect(
      dialog.getByText(/one event for every audit entry that names no project/),
    ).toBeVisible();
    await expect(dialog.getByRole("checkbox", { name: /flag\.kill_switch/ })).toHaveCount(0);
    await dialog.getByLabel("Name").fill("Audit archive");
    await dialog.getByLabel("URL").fill(receiver.url);
    await dialog.getByRole("button", { name: "Create webhook" }).click();
    const secret = await takeSecret(page);

    await expect(page.getByRole("heading", { name: "Audit archive", level: 1 })).toBeVisible();
    await page.getByRole("button", { name: "Send test" }).click();
    const [ping] = await receivedOf(receiver, "ping");
    expect(signedWith(ping!, secret)).toBe(true);
    await expect(page.getByRole("row").filter({ hasText: "ping" })).toContainText("Succeeded", {
      timeout: 15_000,
    });

    await page.goto("/admin/webhooks");
    await expect(webhookRow(page, "Audit archive")).toContainText("Every audit entry");

    // Nothing else is sent to a receiver that is about to go.
    await page.getByRole("button", { name: "Delete Audit archive" }).click();
    await page
      .getByRole("dialog", { name: "Delete Audit archive" })
      .getByRole("button", { name: "Delete webhook" })
      .click();
    await expect(webhookRow(page, "Audit archive")).toHaveCount(0);
  });
});
