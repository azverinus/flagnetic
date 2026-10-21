import { type Client, createClient, createConfig, createProject } from "@flagnetic/api";
import type { Page } from "@playwright/test";

import { admin, readState } from "./instance";

export type Credentials = { email: string; password: string };

/** Signs in through the sign-in page and waits until the console has left it. */
export async function signIn(page: Page, who: Credentials = admin): Promise<void> {
  await page.goto("/sign-in");
  await page.getByLabel("Email").fill(who.email);
  await page.getByLabel("Password").fill(who.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL((url) => !url.pathname.startsWith("/sign-in"));
}

/** The management API as the instance admin, for seeding and for checking. */
export function managementClient(): Client {
  const state = readState();
  return createClient(
    createConfig({
      baseUrl: `${state.baseURL}/api/v1`,
      headers: { Authorization: `Bearer ${state.token}` },
    }),
  );
}

/** The data of a generated operation's result, or a failure naming what was attempted. */
export function must<T>(what: string, result: { data?: T; error?: unknown }): T {
  if (result.error !== undefined) {
    throw new Error(`${what} failed: ${JSON.stringify(result.error)}`);
  }
  return result.data as T;
}

/**
 * A project of one test's own, so that what the test changes never
 * reaches another test's project; the key carries the time, so a retried
 * run does not meet its own project.
 */
export async function seedProject(prefix: string): Promise<string> {
  const key = `${prefix}-${Date.now().toString(36)}`;
  must(
    `seeding ${key}`,
    await createProject({ client: managementClient(), body: { key, name: `Project ${prefix}` } }),
  );
  return key;
}
