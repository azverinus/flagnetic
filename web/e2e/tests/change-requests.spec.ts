import {
  type ChangeRequest,
  type ChangeRequestCreate,
  createChangeRequest,
  createGrant,
  updateChangeRequest,
} from "@flagnetic/api";
import { type Browser, expect, type Page, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import {
  type Credentials,
  managementClient,
  must,
  seedMember,
  seedProject,
  signIn,
} from "../support/console";
import { readConfiguration, seedFlag, stringFlag } from "../support/flags";

const password = "harness-quartz-ledger-5520";

/** Proposes, as the instance admin, a change to the flag's production configuration, opened for review. */
async function propose(
  project: string,
  flag: string,
  title: string,
  proposed: ChangeRequestCreate["proposed"],
): Promise<ChangeRequest> {
  return must(
    `proposing ${title}`,
    await createChangeRequest({
      client: managementClient(),
      path: { project },
      body: {
        kind: "flag_configuration",
        flag,
        environment: "production",
        title,
        description: "The new copy tested well.",
        proposed,
        open: true,
      },
    }),
  );
}

/**
 * A maintainer of the project, in a browser of their own: the second
 * person approval needs, deciding what the first proposed.
 */
async function reviewer(
  browser: Browser,
  project: string,
): Promise<{ page: Page; who: Credentials & { name: string } }> {
  const who = await seedMember("rosa", "Rosa Reviewer", password);
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
  return { page, who };
}

const switchedOn = { on: true, off_variation: "control", fallthrough: "treatment" };

test.describe("change requests", () => {
  test("one member proposes, another approves and applies it from the inbox; a second is rejected", async ({
    page,
    browser,
  }) => {
    const project = await seedProject("change-requests");
    await seedFlag(project, stringFlag("checkout_copy"));
    const first = await propose(project, "checkout_copy", "Serve the new copy", switchedOn);
    const second = await propose(project, "checkout_copy", "Turn it on with control", {
      on: true,
      off_variation: "control",
      fallthrough: "control",
    });

    // The author cannot decide their own change request, and is told why.
    await signIn(page);
    await page.goto(`/projects/${project}/change-requests/${first.id}`);
    await expect(page.getByRole("heading", { name: "Serve the new copy", level: 1 })).toBeVisible();
    const ownApprove = page.getByRole("button", { name: "Approve", exact: true });
    await expect(ownApprove).toHaveAttribute("aria-disabled", "true");
    await expect(ownApprove).toHaveAccessibleDescription(
      "You cannot approve or reject your own change request.",
    );
    await expect(
      page.getByRole("list", { name: "Changes to the configuration" }).getByRole("listitem"),
    ).toHaveText(["Switched on.", "Fallthrough variation changed from control to treatment."]);
    await expectNoSeriousViolations(page, "a change request's detail");

    // The flag's production tab says what waits on it.
    await page.goto(`/projects/${project}/flags/checkout_copy?environment=production`);
    await expect(
      page.getByRole("heading", { name: "Change requests waiting here: 2 open, 0 approved" }),
    ).toBeVisible();

    // The reviewer finds both in their inbox and approves the first.
    const rosa = await reviewer(browser, project);
    await rosa.page.goto(`/projects/${project}`);
    const nav = rosa.page.getByRole("navigation", { name: "Project" });
    const inbox = nav.getByRole("link", { name: /Change requests/ });
    await expect(inbox).toContainText("2 change requests await your decision");
    await inbox.click();
    await expect(rosa.page.getByRole("tab", { name: "Awaiting my decision" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const rows = rosa.page.getByRole("table", { name: "Change requests" }).getByRole("row");
    await expect(rows).toHaveCount(3);
    await expectNoSeriousViolations(rosa.page, "the change requests awaiting a decision");
    await rosa.page.getByRole("link", { name: "Serve the new copy" }).click();

    await rosa.page.getByRole("button", { name: "Approve", exact: true }).click();
    const approveDialog = rosa.page.getByRole("dialog", { name: "Approve the change request" });
    await approveDialog.getByLabel("Comment").fill("Looks right.");
    await approveDialog.getByRole("button", { name: "Approve" }).click();
    await expect(approveDialog).toHaveCount(0);
    await expect(rosa.page.getByText("Approved by Rosa Reviewer")).toBeVisible();
    await expect(rosa.page.getByText("Looks right.")).toBeVisible();

    // The second is rejected before the first is applied, which would
    // supersede it, with the reason its author needs.
    await rosa.page.goto(`/projects/${project}/change-requests/${second.id}`);
    await rosa.page.getByRole("button", { name: "Reject", exact: true }).click();
    const rejectDialog = rosa.page.getByRole("dialog", { name: "Reject the change request" });
    await rejectDialog.getByRole("button", { name: "Reject" }).click();
    await expect(
      rejectDialog.getByText("Say why, so the author knows what to change."),
    ).toBeVisible();
    await rejectDialog.getByLabel("Comment").fill("Control is what we serve already.");
    await rejectDialog.getByRole("button", { name: "Reject" }).click();
    await expect(rejectDialog).toHaveCount(0);
    await expect(rosa.page.getByText("Rejected by Rosa Reviewer")).toBeVisible();
    await expect(nav.getByText(/await your decision/)).toHaveCount(0);

    // The first is applied.
    await rosa.page.goto(`/projects/${project}/change-requests/${first.id}`);
    await rosa.page.getByRole("button", { name: "Apply", exact: true }).click();
    const applyDialog = rosa.page.getByRole("dialog", { name: "Apply the change request" });
    await applyDialog.getByRole("button", { name: "Apply" }).click();
    await expect(applyDialog).toHaveCount(0);
    await expect(rosa.page.getByText("Applied", { exact: true }).first()).toBeVisible();
    const production = await readConfiguration(project, "checkout_copy", "production");
    expect(production.on).toBe(true);
    expect(production.fallthrough).toBe("treatment");

    // Every state the author's list shows, final ones included.
    await page.goto(`/projects/${project}/change-requests?tab=mine`);
    const mine = page.getByRole("table", { name: "Change requests" });
    await expect(mine.getByRole("row", { name: /Serve the new copy/ })).toContainText("Applied");
    await expect(mine.getByRole("row", { name: /Turn it on with control/ })).toContainText(
      "Rejected",
    );
  });

  test("a decision on a change request someone edited meanwhile is refused and shows the edit", async ({
    browser,
  }) => {
    const project = await seedProject("change-request-conflict");
    await seedFlag(project, stringFlag("checkout_copy"));
    const proposed = await propose(project, "checkout_copy", "Serve the new copy", switchedOn);
    const rosa = await reviewer(browser, project);
    // Without the project's change stream the page keeps what it read, as
    // a page does whose stream is down, so the edit is met at the decision.
    await rosa.page.route("**/projects/*/stream", (route) => route.abort());
    await rosa.page.goto(`/projects/${project}/change-requests/${proposed.id}`);
    await expect(
      rosa.page.getByRole("heading", { name: "Serve the new copy", level: 1 }),
    ).toBeVisible();

    must(
      "retitling the change request",
      await updateChangeRequest({
        client: managementClient(),
        path: { project, change_request: proposed.id },
        headers: { "If-Match": `"${proposed.version}"` },
        body: { title: "Serve the newest copy" },
      }),
    );

    await rosa.page.getByRole("button", { name: "Approve", exact: true }).click();
    const dialog = rosa.page.getByRole("dialog", { name: "Approve the change request" });
    await dialog.getByRole("button", { name: "Approve" }).click();
    await expect(dialog.getByRole("alert")).toContainText(
      "Someone else changed this since you opened it.",
    );
    await rosa.page.keyboard.press("Escape");
    await expect(
      rosa.page.getByRole("heading", { name: "Serve the newest copy", level: 1 }),
    ).toBeVisible();

    // Approving again is based on the version now shown.
    await rosa.page.getByRole("button", { name: "Approve", exact: true }).click();
    await dialog.getByRole("button", { name: "Approve" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(rosa.page.getByText("Approved by Rosa Reviewer")).toBeVisible();
  });

  test("a developer sees what deciding needs, and copies a final change request into a draft", async ({
    browser,
  }) => {
    const project = await seedProject("change-request-copy");
    await seedFlag(project, stringFlag("checkout_copy"));
    const proposed = await propose(project, "checkout_copy", "Serve the new copy", switchedOn);
    const dev = await seedMember("dmitri", "Dmitri Developer", password);
    must(
      `granting developer in ${project}`,
      await createGrant({
        client: managementClient(),
        path: { project },
        body: { member: dev.id, role: "developer" },
      }),
    );
    const page = await (await browser.newContext()).newPage();
    await signIn(page, dev);

    await page.goto(`/projects/${project}/change-requests?tab=awaiting`);
    await expect(page.getByText("No change request awaits your decision.")).toBeVisible();
    await page.goto(`/projects/${project}/change-requests/${proposed.id}`);
    const approve = page.getByRole("button", { name: "Approve", exact: true });
    await expect(approve).toHaveAttribute("aria-disabled", "true");
    await expect(approve).toHaveAccessibleDescription(/approve and reject change requests/);

    await page.getByRole("button", { name: "Copy", exact: true }).click();
    await page
      .getByRole("dialog", { name: "Copy the change request" })
      .getByRole("button", { name: "Copy" })
      .click();
    await expect(page.getByText("Draft", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Open for review" })).toBeVisible();
    await expect(page).not.toHaveURL(new RegExp(proposed.id));
  });
});
