import { expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { signIn } from "../support/console";
import { admin } from "../support/instance";
import {
  awaitMailTo,
  clearMailsTo,
  mailpitPassword,
  mailpitUser,
  mailsTo,
  readMailpit,
  removeSmtp,
} from "../support/mailpit";

const heading = (page: Page) => page.getByRole("heading", { level: 1, name: "Mail" });

test.describe("mail settings", () => {
  // The SMTP server is the instance's: the specs build on each other in
  // order, and the instance is left without one, as every other spec
  // expects it.
  test.describe.configure({ mode: "serial" });
  test.beforeAll(removeSmtp);
  test.afterAll(removeSmtp);

  test("configures Mailpit, saying a refused sender under its field", async ({ page }) => {
    const mailpit = readMailpit();
    await signIn(page);
    await page.getByRole("link", { name: "Mail" }).click();
    await expect(heading(page)).toBeVisible();
    await expect(page.getByText(/No SMTP server is configured/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Send a test mail" })).toHaveCount(0);
    await expectNoSeriousViolations(page, "the mail settings without a server");

    await page.getByLabel("Host").fill(mailpit.smtpHost);
    await page.getByLabel("Port").fill(String(mailpit.smtpPort));
    await page.getByRole("combobox", { name: "Security" }).click();
    await page.getByRole("option", { name: /^None/ }).click();
    await page.getByLabel("Username").fill(mailpitUser);
    await page.getByLabel("Password").fill(mailpitPassword);
    // Only the server parses an address; it refuses a name without one.
    await page.getByLabel("Sender").fill("Flagnetic");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(/Enter one address, optionally with a name/)).toBeVisible();
    await expectNoSeriousViolations(page, "the mail settings with a refused field");

    await page.getByLabel("Sender").fill("Flagnetic <flags@flagnetic.test>");
    await page.getByRole("button", { name: "Save" }).click();

    await expect(page.getByText("The mail settings are saved.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Send a test mail" })).toBeVisible();
    // The password is write-only: the form says one is stored, and shows none.
    await expect(page.getByLabel("Password")).toHaveValue("");
    await expect(page.getByText(/A password is stored and never shown again/)).toBeVisible();
    await expect(page.getByText(mailpitPassword)).toHaveCount(0);
  });

  test("sends a test mail that Mailpit receives", async ({ page }) => {
    await clearMailsTo(admin.email);
    await signIn(page);
    await page.goto("/admin/mail");

    await page.getByRole("button", { name: "Send a test mail" }).click();

    await expect(page.getByRole("status").filter({ hasText: /accepted a test mail/ })).toHaveText(
      `The SMTP server accepted a test mail to ${admin.email}. Check that it arrived.`,
    );
    const mail = await awaitMailTo(admin.email);
    expect(mail.subject).toBe("Flagnetic test mail");
    expect(mail.from).toBe("flags@flagnetic.test");
    expect(mail.text).toContain(`this test mail reached ${admin.email}`);
    await expectNoSeriousViolations(page, "the mail settings after a test mail");
  });

  test("explains a test mail that fails on a wrong password", async ({ page }) => {
    await clearMailsTo(admin.email);
    await signIn(page);
    await page.goto("/admin/mail");

    await page.getByLabel("Password").fill("not-the-relay-password");
    await page.getByRole("button", { name: "Save" }).click();
    // The toast is checked with the page once it has faded in: halfway, its
    // text is paler than it will be.
    const saved = page
      .locator("[data-sonner-toast]")
      .filter({ hasText: "The mail settings are saved." });
    await expect(saved).toHaveCSS("opacity", "1");
    await page.getByRole("button", { name: "Send a test mail" }).click();

    await expect(page.getByRole("alert")).toHaveText(
      "The SMTP server refused the username and password. Check them, then save and test again.",
    );
    await expectNoSeriousViolations(page, "the mail settings with a failed test mail");
    expect(await mailsTo(admin.email)).toEqual([]);
  });

  test("removes the server after confirmation", async ({ page }) => {
    await signIn(page);
    await page.goto("/admin/mail");

    await page.getByRole("button", { name: "Remove" }).click();
    const dialog = page.getByRole("dialog", { name: "Remove the SMTP server" });
    await expect(dialog).toContainText("The instance stops mailing");
    await expectNoSeriousViolations(page, "the SMTP server removal dialog");
    await dialog.getByRole("button", { name: "Remove SMTP server" }).click();

    await expect(page.getByText(/No SMTP server is configured/)).toBeVisible();
    await expect(page.getByLabel("Host")).toHaveValue("");
    await expect(page.getByRole("button", { name: "Send a test mail" })).toHaveCount(0);
  });
});
