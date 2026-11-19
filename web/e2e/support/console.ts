import {
  acceptInvitation,
  type Client,
  createClient,
  createConfig,
  createGrant,
  createProject,
  getEnvironment,
  inviteMember,
  setEnvironmentProtection,
  signIn as signInOperation,
  signOut,
} from "@flagnetic/api";
import type { Browser, Page } from "@playwright/test";

import { admin, readState } from "./instance";

export type Credentials = { email: string; password: string };

/** The labels of the sign-in page in the language a spec reads it in. */
export type SignInWords = { email: string; password: string; submit: string; next: string };

const english: SignInWords = {
  email: "Email",
  password: "Password",
  submit: "Sign in",
  next: "Continue",
};

/**
 * Fills in and sends the sign-in page, leaving the page wherever the answer
 * takes it. While the instance has auth providers the page asks for the
 * email first, so the password comes after "Continue".
 */
export async function submitSignIn(
  page: Page,
  who: Credentials,
  words: SignInWords = english,
): Promise<void> {
  await page.goto("/sign-in");
  await page.getByLabel(words.email).fill(who.email);
  const next = page.getByRole("button", { name: words.next, exact: true });
  if (await next.isVisible()) await next.click();
  await page.getByLabel(words.password).fill(who.password);
  await page.getByRole("button", { name: words.submit }).click();
}

/** Signs in through the sign-in page and waits until the console has left it. */
export async function signIn(
  page: Page,
  who: Credentials = admin,
  words: SignInWords = english,
): Promise<void> {
  await submitSignIn(page, who, words);
  await page.waitForURL((url) => !url.pathname.startsWith("/sign-in"));
}

/**
 * The operations that create access refuse every API token, so a script's
 * token cannot leave behind a member, a role or a password that outlives it:
 * the instance admin performs them signed in.
 */
const sessionOnly = [
  /^POST \/api\/v1\/members$/,
  /^PATCH \/api\/v1\/members\/[^/]+$/,
  /^POST \/api\/v1\/members\/[^/]+\/(invitation|reactivate|password-reset)$/,
  /^POST \/api\/v1\/projects\/[^/]+\/(grants|invitations)$/,
  // What decides who signs in, and with which access.
  /^POST \/api\/v1\/instance\/auth-providers$/,
  /^PATCH \/api\/v1\/instance\/auth-providers\/[^/]+$/,
  /^POST \/api\/v1\/instance\/auth-providers\/[^/]+\/(enable|rotate-certificate\/complete)$/,
  /^PUT \/api\/v1\/instance\/auth-providers\/[^/]+\/group-mappings$/,
  /^PUT \/api\/v1\/instance\/auth-settings$/,
];

/**
 * The management API as the instance admin, for seeding and for checking:
 * with the bootstrap token, and with a session signed in afresh for the
 * operations only a session may perform.
 */
export function adminClient(baseURL: string, token: string): Client {
  const withToken: typeof fetch = (input, init) => fetch(input, init);
  const signedIn: typeof fetch = async (input, init) => {
    const request = new Request(input, init);
    const { pathname } = new URL(request.url);
    if (!sessionOnly.some((pattern) => pattern.test(`${request.method} ${pathname}`))) {
      return withToken(request);
    }
    const anonymous = createClient(createConfig({ baseUrl: `${baseURL}/api/v1` }));
    const opened = await signInOperation({
      client: anonymous,
      body: { email: admin.email, password: admin.password },
    });
    const cookie = opened.response?.headers.get("set-cookie")?.split(";")[0];
    if (!cookie)
      throw new Error(`signing the instance admin in failed: ${JSON.stringify(opened.error)}`);
    const headers = new Headers(request.headers);
    headers.delete("Authorization");
    headers.set("Cookie", cookie);
    return fetch(new Request(request, { headers }));
  };
  return createClient(
    createConfig({
      baseUrl: `${baseURL}/api/v1`,
      headers: { Authorization: `Bearer ${token}` },
      fetch: signedIn,
    }),
  );
}

/** The management API as the instance admin, for seeding and for checking. */
export function managementClient(): Client {
  const state = readState();
  return adminClient(state.baseURL, state.token);
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
 * reaches another test's project; the key carries the time and a random
 * suffix, so neither a retried run nor a repeat running at the same
 * moment in another worker meets a project already seeded.
 */
export async function seedProject(prefix: string): Promise<string> {
  const key = `${prefix}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  must(
    `seeding ${key}`,
    await createProject({ client: managementClient(), body: { key, name: `Project ${prefix}` } }),
  );
  return key;
}

/**
 * Stops protecting a seeded project's production, for a spec that writes
 * production directly: in a new project, production is protected, and a
 * write changing what it serves would need a change request.
 */
export async function unprotectProduction(project: string): Promise<void> {
  const client = managementClient();
  const path = { project, environment: "production" };
  const current = must(`reading production of ${project}`, await getEnvironment({ client, path }));
  must(
    `unprotecting production of ${project}`,
    await setEnvironmentProtection({
      client,
      path,
      headers: { "If-Match": `"${current.version}"` },
      body: { protected: false },
    }),
  );
}

/** Protects a project's production again, after a spec wrote it directly. */
export async function protectProduction(project: string): Promise<void> {
  const client = managementClient();
  const path = { project, environment: "production" };
  const current = must(`reading production of ${project}`, await getEnvironment({ client, path }));
  must(
    `protecting production of ${project}`,
    await setEnvironmentProtection({
      client,
      path,
      headers: { "If-Match": `"${current.version}"` },
      body: { protected: true },
    }),
  );
}

/**
 * Someone invited by the instance admin who has not accepted yet, with
 * the invitation's one-time link. The email carries the time and a random
 * suffix, so neither a retried run nor a repeat running at the same
 * moment in another worker meets a member already invited.
 */
export async function seedInvitation(
  prefix: string,
  name: string,
): Promise<{ id: string; email: string; name: string; link: string }> {
  const email = `${prefix}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}@flagnetic.test`;
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

/**
 * A second member of the project, a developer with a browser of their
 * own: what they save while the first member's page is open is the change
 * that page meets, made through the console as a colleague would make it.
 */
export async function colleague(
  browser: Browser,
  project: string,
  prefix: string,
  name: string,
): Promise<{ page: Page; who: Credentials }> {
  const who = await seedMember(prefix, name, "harness-quill-lantern-2291");
  must(
    `granting developer in ${project}`,
    await createGrant({
      client: managementClient(),
      path: { project },
      body: { member: who.id, role: "developer" },
    }),
  );
  const context = await browser.newContext();
  return { page: await context.newPage(), who };
}

/**
 * Opens one of a project's settings screens as a member does: the sidebar's
 * "Project settings" link, then the screen in the area's list.
 */
export async function openProjectSetting(page: Page, screen: string): Promise<void> {
  await page
    .getByRole("navigation", { name: "Project" })
    .getByRole("link", { name: "Project settings", exact: true })
    .click();
  await page
    .getByRole("navigation", { name: "Settings", exact: true })
    .getByRole("link", { name: screen, exact: true })
    .click();
}

/**
 * Opens one of the instance's screens as an instance admin does: the
 * sidebar's "Instance" link, then the screen in the area's list.
 */
export async function openInstanceScreen(page: Page, screen: string): Promise<void> {
  await page.getByRole("link", { name: "Instance", exact: true }).click();
  await page
    .getByRole("navigation", { name: "Instance", exact: true })
    .getByRole("link", { name: screen, exact: true })
    .click();
}
