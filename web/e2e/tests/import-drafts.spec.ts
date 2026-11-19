import { createGrant } from "@flagnetic/api";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { expect, type Page, test } from "@playwright/test";

import { managementClient, must, seedMember, seedProject, signIn } from "../support/console";
import { admin, repoRoot } from "../support/instance";
import { documentFile, exportedDocument } from "../support/transfer";

const password = "harness-quince-ferry-5521";

const template = {
  name: "remoteconfig.template.json",
  mimeType: "application/json",
  buffer: readFileSync(
    resolve(repoRoot, "internal/domain/transfer/firebase/testdata/firebase-template.json"),
  ),
};

/** An owner of the project, so the person can open both import wizards. */
async function owner(project: string, prefix: string, name: string) {
  const who = await seedMember(prefix, name, password);
  must(
    "granting owner",
    await createGrant({
      client: managementClient(),
      path: { project },
      body: { member: who.id, role: "owner" },
    }),
  );
  return who;
}

const importPage = (project: string) => `/projects/${project}/import-export`;

/** Reads a project document into the review step of the project import. */
async function draftProjectImport(page: Page, project: string, name: string) {
  await page.goto(importPage(project));
  await page
    .getByLabel("Project document")
    .setInputFiles(documentFile(await exportedDocument(project), name));
  await page.getByRole("button", { name: "Check what it would change" }).click();
  await expect(page.getByText(`Document: ${name}`)).toBeVisible();
}

/** Reads the template into the parameters step of the Firebase wizard. */
async function draftFirebaseImport(page: Page, project: string) {
  await page.goto(importPage(project));
  await page.getByLabel("Remote Config template").setInputFiles(template);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("table", { name: "The parameters of the template" })).toBeVisible();
}

async function signOut(page: Page, name: string) {
  await page.getByRole("button", { name }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
}

test.describe("the import wizards' drafts", () => {
  test("a second person signing in on the same tab finds no draft of the first, and a reload restores a person's own", async ({
    page,
  }) => {
    const project = await seedProject("drafts");
    const second = await owner(project, "drafts-second", "Grace Second");
    await signIn(page);

    await draftProjectImport(page, project, "first-persons-document.json");
    await page.reload();
    await expect(page.getByText("Document: first-persons-document.json")).toBeVisible();

    await draftFirebaseImport(page, project);
    await page.reload();
    await expect(page.getByRole("table", { name: "The parameters of the template" })).toBeVisible();

    await signOut(page, admin.name);
    await signIn(page, second);
    await page.goto(importPage(project));

    // Neither wizard resumes: both start at the upload, and nothing of the
    // first person's file is left in the tab.
    await expect(page.getByLabel("Project document")).toBeVisible();
    await expect(page.getByLabel("Remote Config template")).toBeVisible();
    await expect(page.getByText("first-persons-document.json")).toHaveCount(0);
    const kept = await page.evaluate(() => Object.keys(window.sessionStorage));
    expect(kept.filter((key) => key.startsWith("flagnetic.import"))).toEqual([]);
  });

  test("the drafts are gone when another account signs in without the first signing out", async ({
    page,
  }) => {
    const project = await seedProject("draftsswitch");
    const second = await owner(project, "draftsswitch-second", "Henry Second");
    await signIn(page);
    await draftProjectImport(page, project, "left-behind.json");

    // The first session ends where the page cannot tell: the second person
    // signs in on a fresh sign-in page in the same tab.
    await page.context().clearCookies();
    await signIn(page, second);
    await page.goto(importPage(project));

    await expect(page.getByLabel("Project document")).toBeVisible();
    await expect(page.getByText("left-behind.json")).toHaveCount(0);
  });
});
