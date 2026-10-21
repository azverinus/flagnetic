import {
  acceptInvitation,
  type Client,
  createClient,
  createConfig,
  createProject,
  inviteMember,
  signOut,
} from "@flagnetic/api";
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

/**
 * Someone invited by the instance admin who has not accepted yet, with
 * the invitation's one-time link. The email carries the time, so a
 * retried run does not meet its own member.
 */
export async function seedInvitation(
  prefix: string,
  name: string,
): Promise<{ id: string; email: string; name: string; link: string }> {
  const email = `${prefix}-${Date.now().toString(36)}@flagnetic.test`;
  const invited = must(
    `inviting ${email}`,
    await inviteMember({ client: managementClient(), body: { email, display_name: name } }),
  );
  return { id: invited.member.id, email, name, link: invited.invitation?.url ?? "" };
}

/** The path of an invitation's link, which a page of the suite opens. */
export function pathOf(link: string): string {
  return new URL(link).pathname;
}

/**
 * A member of one spec's own, invited by the instance admin and signed up
 * through the invitation, so that what the spec changes of them (their
 * name, password, sessions and tokens) never reaches another spec. The
 * email carries the time, so a retried run does not meet its own member.
 */
export async function seedMember(
  prefix: string,
  name: string,
  password: string,
): Promise<Credentials & { id: string; name: string }> {
  const { id, email, link } = await seedInvitation(prefix, name);
  const anonymous = createClient(createConfig({ baseUrl: `${readState().baseURL}/api/v1` }));
  const accepted = await acceptInvitation({
    client: anonymous,
    path: { secret: link.slice(link.lastIndexOf("/") + 1) },
    body: { password },
  });
  must(`accepting the invitation of ${email}`, accepted);
  // Accepting signs the member in; that session is ended, so the member's
  // devices are only those the spec signs in on.
  const cookie = accepted.response?.headers.get("set-cookie")?.split(";")[0] ?? "";
  must(
    `signing ${email} out of the acceptance`,
    await signOut({ client: anonymous, headers: { Cookie: cookie } }),
  );
  return { id, email, password, name };
}
