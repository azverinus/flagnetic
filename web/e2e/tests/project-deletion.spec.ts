import { getProject } from "@flagnetic/api";
import { readFile } from "node:fs/promises";

import { expect, test } from "@playwright/test";

import { expectNoSeriousViolations } from "../support/axe";
import { managementClient, seedProject, signIn } from "../support/console";

test("an owner deletes a project after downloading its audit log and typing its key", async ({
  page,
}) => {
  const key = await seedProject("doomed");
  await signIn(page);
  await page.goto(`/projects/${key}/settings`);
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
  await expectNoSeriousViolations(page, "the project settings with the deletion");

  await page.getByRole("button", { name: "Delete project" }).click();
  const dialog = page.getByRole("dialog", { name: `Delete ${key}` });
  await expect(dialog).toBeVisible();
  await expectNoSeriousViolations(page, "the project deletion dialog");

  // The audit log is offered first, and is saved whole.
  const downloading = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "Download JSON Lines" }).click();
  const download = await downloading;
  expect(download.suggestedFilename()).toMatch(new RegExp(`^audit-${key}-.*\\.jsonl$`));
  const text = await readFile((await download.path())!, "utf8");
  expect(text).toContain("project.created");

  // Nothing is deleted until the key is typed out.
  const confirm = dialog.getByRole("button", { name: "Delete project" });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(`Type ${key} to confirm`).fill(`${key}x`);
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(`Type ${key} to confirm`).fill(key);
  await confirm.click();

  // The member lands on the list of projects, which no longer holds it.
  await expect(page).toHaveURL(/\/\?all=true$/);
  await expect(page.getByRole("heading", { name: "Projects", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: key, exact: true })).toHaveCount(0);
  const read = await getProject({ client: managementClient(), path: { project: key } });
  expect(read.response?.status).toBe(404);
});
