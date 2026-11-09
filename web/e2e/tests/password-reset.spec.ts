import { getAuthSettings, updateAuthSettings } from "@flagnetic/api";
import { type Browser, expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  pathOf,
  seedInvitation,
  seedMember,
  signIn,
  submitSignIn,
} from "../support/console";
import { awaitMailTo, clearMailsTo, configureSmtp, mailsTo, removeSmtp } from "../support/mailpit";

const oldPassword = "harness-pebble-orchard-3381";
const newPassword = "harness-copper-meadow-5527";

/** The answer the forgotten-password page gives, whoever the email belongs to. */
const sameAnswer =
  "If this email belongs to a member who can reset their password, a link to reset it is on its way. It works for an hour.";

/** The masked email the reset page shows: its first character, then the domain. */
function masked(email: string): string {
  const [local = "", domain = ""] = email.split("@");
  return `${local.charAt(0)}***@${domain}`;
}

/** The password reset link a mail carries. */
function linkIn(text: string): string {
  const link = /https?:\/\/\S+\/password-reset\/\S+/.exec(text)?.[0];
  if (!link) throw new Error(`no password reset link in the mail:\n${text}`);
  return link;
}

async function choose(page: Page, value: string) {
  await page.getByLabel("New password", { exact: true }).fill(value);
  await page.getByLabel("Confirm the new password").fill(value);
  await page.getByRole("button", { name: "Set password" }).click();
}

/** A browser of its own, signed in as nobody. */
async function freshPage(browser: Browser): Promise<Page> {
  const context = await browser.newContext();
  return context.newPage();
}

/** Switches self-service password reset on or off through the management API. */
async function setSelfService(on: boolean): Promise<void> {
  const client = managementClient();
  const settings = must("reading the auth settings", await getAuthSettings({ client }));
  must(
    "saving the auth settings",
    await updateAuthSettings({ client, body: { ...settings, self_service_password_reset: on } }),
  );
}

test.describe("resetting a forgotten password", () => {
  // The SMTP server and the auth settings are the instance's: the specs
  // run in order, and the instance is left without SMTP and with
  // self-service reset on, as every other spec expects it.
  test.describe.configure({ mode: "serial" });
  // Members join through an invitation's link, which an instance with
  // SMTP mails rather than shows, so they are seeded before SMTP is set.
  let rhea: Credentials & { id: string };
  let tomas: Credentials & { id: string };
  test.beforeAll(async () => {
    await removeSmtp();
    rhea = await seedMember("rhea", "Rhea Okafor", oldPassword);
    tomas = await seedMember("tomas", "Tomas Lindqvist", oldPassword);
    await configureSmtp();
    await setSelfService(true);
  });
  test.afterAll(async () => {
    await setSelfService(true);
    await removeSmtp();
  });

  test("mails the link, which sets the new password under the policy and signs the member in", async ({
    page,
    browser,
  }) => {
    const member = rhea;
    await clearMailsTo(member.email);

    await page.goto("/sign-in");
    await page.getByRole("link", { name: "Forgot your password?" }).click();
    await expect(page.getByRole("heading", { name: "Forgot your password?" })).toBeVisible();
    await expectNoSeriousViolations(page, "the forgotten-password page");
    await page.getByLabel("Email").fill(member.email);
    await page.getByRole("button", { name: "Send a password reset link" }).click();
    await expect(page.getByRole("status")).toHaveText(sameAnswer);
    await expectNoSeriousViolations(page, "the forgotten-password page once sent");

    const mail = await awaitMailTo(member.email);
    expect(mail.subject).toBe("Reset your Flagnetic password");
    const link = linkIn(mail.text);

    await page.goto(pathOf(link));
    await expect(page.getByRole("heading", { name: "Choose a new password" })).toBeVisible();
    await expect(page.getByText(`for Rhea Okafor (${masked(member.email)})`)).toBeVisible();
    await expect(page.locator("body")).not.toContainText(member.email);
    await expectNoSeriousViolations(page, "the password reset page");

    await choose(page, "too-short");
    await expect(page.getByText("Use 12 to 128 characters.")).toBeVisible();
    await expect(page.getByLabel("New password", { exact: true })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await expectNoSeriousViolations(page, "the password reset page with a refusal");

    await choose(page, newPassword);
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("button", { name: "Rhea Okafor", exact: true })).toBeVisible();
    await expect(
      page.getByText("Your new password is set. Every other session of yours is signed out."),
    ).toBeVisible();

    // The old password no longer opens a session; the new one does.
    const elsewhere = await freshPage(browser);
    await submitSignIn(elsewhere, { email: member.email, password: oldPassword });
    await expect(elsewhere.getByRole("alert")).toHaveText(
      "The email or the password is not right.",
    );
    await signIn(elsewhere, { email: member.email, password: newPassword });
    await expect(elsewhere.getByRole("button", { name: "Rhea Okafor", exact: true })).toBeVisible();

    // The link works once.
    const again = await freshPage(browser);
    await again.goto(pathOf(link));
    await expect(
      again.getByRole("heading", { name: "This password reset link no longer works" }),
    ).toBeVisible();
    await expect(again.getByLabel("New password", { exact: true })).toHaveCount(0);
    await expectNoSeriousViolations(again, "a password reset link that no longer works");
    await again.getByRole("link", { name: "Ask for a new link" }).click();
    await expect(again.getByRole("heading", { name: "Forgot your password?" })).toBeVisible();
  });

  test("answers the same for an email no member signs in with, mailing nothing", async ({
    page,
  }) => {
    const nobody = `nobody-${Date.now().toString(36)}@flagnetic.test`;
    await page.goto("/forgot-password");
    await page.getByLabel("Email").fill(nobody);
    await page.getByRole("button", { name: "Send a password reset link" }).click();

    await expect(page.getByRole("status")).toHaveText(sameAnswer);
    expect(await mailsTo(nobody)).toEqual([]);
  });

  test("offers the forgotten password only while self-service reset is on", async ({
    page,
    browser,
  }) => {
    try {
      await signIn(page);
      await page.goto("/admin/auth-settings");
      const toggle = page.getByRole("switch", { name: "Self-service password reset" });
      await expect(toggle).toBeChecked();
      await expectNoSeriousViolations(page, "the auth settings with self-service reset");
      await toggle.click();
      await page.getByRole("button", { name: "Save" }).click();
      await expect(page.getByText("The auth settings are saved.")).toBeVisible();

      const visitor = await freshPage(browser);
      await visitor.goto("/sign-in");
      await expect(visitor.getByLabel("Password")).toBeVisible();
      await expect(visitor.getByRole("link", { name: "Forgot your password?" })).toHaveCount(0);
      await visitor.goto("/forgot-password");
      await expect(
        visitor.getByText(
          "This instance does not mail password resets. Ask an instance admin for a password reset link.",
        ),
      ).toBeVisible();
      await expect(visitor.getByLabel("Email")).toHaveCount(0);
      await expectNoSeriousViolations(visitor, "the forgotten-password page with reset off");
    } finally {
      await setSelfService(true);
    }
  });

  test("lets an instance admin issue a password reset link, shown once, for an active member only", async ({
    page,
    browser,
  }) => {
    const member = tomas;
    await clearMailsTo(member.email);
    await signIn(page);
    await page.goto(`/admin/members/${member.id}`);

    await page.getByRole("button", { name: "Issue a password reset link" }).click();
    const confirm = page.getByRole("dialog", {
      name: "Issue a password reset link for Tomas Lindqvist",
    });
    await expect(confirm).toContainText("Whoever opens the link chooses a new password");
    await expectNoSeriousViolations(page, "the password reset link confirmation");
    await confirm.getByRole("button", { name: "Issue link" }).click();

    const reveal = page.getByRole("dialog", { name: "A password reset link for Tomas Lindqvist" });
    await expect(reveal).toContainText("The instance does not mail this link");
    const link = (await reveal.locator("code").textContent()) ?? "";
    expect(link).toMatch(/\/password-reset\//);
    await expectNoSeriousViolations(page, "the password reset link, shown once");
    await reveal.getByLabel("I have stored it somewhere safe").check();
    await reveal.getByRole("button", { name: "Close" }).click();
    await expect(reveal).toBeHidden();
    await expect(page.locator("body")).not.toContainText(link);

    const theirs = await freshPage(browser);
    await theirs.goto(pathOf(link));
    await choose(theirs, newPassword);
    await expect(
      theirs.getByRole("button", { name: "Tomas Lindqvist", exact: true }),
    ).toBeVisible();
    // An instance admin's link is never mailed.
    expect(await mailsTo(member.email)).toEqual([]);

    // An invited member joins through their invitation instead.
    const invited = await seedInvitation("ines", "Ines Duarte");
    await page.goto(`/admin/members/${invited.id}`);
    await expect(page.getByRole("button", { name: "Issue a new invitation link" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Issue a password reset link" })).toHaveCount(0);
  });

  test("reads in Russian for a Russian browser", async ({ browser }) => {
    const context = await browser.newContext({ locale: "ru-RU" });
    const page = await context.newPage();
    await page.goto("/sign-in");
    await page.getByRole("link", { name: "Забыли пароль?" }).click();
    await expect(page.getByRole("heading", { name: "Забыли пароль?" })).toBeVisible();
    await page.getByLabel("Электронная почта").fill("nobody@flagnetic.test");
    await page.getByRole("button", { name: "Отправить ссылку для сброса пароля" }).click();
    await expect(page.getByRole("status")).toContainText("ссылка для сброса уже в пути");
    await expectNoSeriousViolations(page, "the forgotten-password page in Russian");

    await page.goto("/password-reset/not-a-secret-anyone-issued");
    await expect(
      page.getByRole("heading", { name: "Эта ссылка для сброса пароля больше не действует" }),
    ).toBeVisible();
    await expectNoSeriousViolations(page, "a gone password reset link in Russian");
    await context.close();
  });
});
