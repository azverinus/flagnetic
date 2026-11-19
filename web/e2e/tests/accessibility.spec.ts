import { readFileSync } from "node:fs";

import {
  createAuthProvider,
  createChangeRequest,
  createEnvironment,
  createExperiment,
  createProject,
  createInstanceWebhook,
  createSegment,
  createSink,
  createWebhook,
  issuePasswordReset,
  listProjectAuditEntries,
} from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { seriousViolations, useTheme } from "../support/axe";
import {
  managementClient,
  must,
  pathOf,
  seedInvitation,
  seedMember,
  signIn,
} from "../support/console";
import { seedFlag, stringFlag } from "../support/flags";
import { contentWidth, overlappingControls, sidewaysOverflow } from "../support/layout";

/**
 * The accessibility sweep: axe on every routed screen in light and dark
 * with no serious or critical violation, no screen scrolling sideways from
 * 390 px up, no control covered by another at the widths the console is laid
 * out for, and content that stops at 1600 px on a wide screen. It runs over
 * a stress seed: long names and keys, many environments and tags, a long
 * webhook URL and a long change request title. Each screen is checked
 * softly, so one run lists every failure instead of the first.
 */

/** Names and keys at the limits the API accepts, so a screen is laid out for the worst case. */
const longName = "A very long name that a person typed once and nobody ever shortened ".repeat(3);
const longKey = (prefix: string) => `${prefix}-${"long-key-".repeat(8)}`.slice(0, 64);
const longUrl = `https://hooks.example.com/${"a-long-path-segment/".repeat(12)}flagnetic`;
const longTitle =
  "Switch the sweep flag on for every customer in every region once the long review is over";

/**
 * What the sweep's screens are about: a project, an invited member, their
 * invitation's path and an audit entry of the project, whose flag is
 * {@link sweepFlag}.
 */
type Seeded = {
  project: string;
  member: string;
  invitation: string;
  passwordReset: string;
  auditEntry: string;
  changeRequest: string;
  webhook: string;
  sink: string;
  instanceWebhook: string;
  authProvider: string;
};

/** The key of the flag the sweep creates in its project. */
const sweepFlag = "sweep_flag";

/** The key of the segment the sweep creates in its project. */
const sweepSegment = "sweep_segment";

/** The key of the experiment the sweep creates on its flag. */
const sweepExperiment = "sweep_experiment";

/** The key of the environment every project starts with, whose page the sweep opens. */
const sweepEnvironment = "staging";

/** A screen to visit: the route of the console it is, and its address. */
type Route = { name: string; route: string; path: (seeded: Seeded) => string };

/** Screens a visitor reaches before signing in. */
const signedOut: Route[] = [
  { name: "sign-in", route: "/sign-in", path: () => "/sign-in" },
  { name: "invitation", route: "/invitations/$secret", path: (s) => s.invitation },
  {
    name: "invitation (unknown secret)",
    route: "/invitations/$secret",
    path: () => "/invitations/not-a-secret",
  },
  { name: "forgotten password", route: "/forgot-password", path: () => "/forgot-password" },
  { name: "password reset", route: "/password-reset/$secret", path: (s) => s.passwordReset },
  {
    name: "password reset (unknown secret)",
    route: "/password-reset/$secret",
    path: () => "/password-reset/not-a-secret",
  },
];

const signedIn: Route[] = [
  { name: "projects", route: "/", path: () => "/" },
  {
    name: "personal settings: profile",
    route: "/settings/profile",
    path: () => "/settings/profile",
  },
  { name: "personal settings: tokens", route: "/settings/tokens", path: () => "/settings/tokens" },
  {
    name: "personal settings: sign-in methods",
    route: "/settings/sign-in-methods",
    path: () => "/settings/sign-in-methods",
  },
  {
    name: "personal settings: sessions",
    route: "/settings/sessions",
    path: () => "/settings/sessions",
  },
  { name: "instance members", route: "/admin/members", path: () => "/admin/members" },
  ...["sessions", "tokens", "identities"].map((tab) => ({
    name: `instance member: ${tab}`,
    route: "/admin/members/$member",
    path: (s: Seeded) => `/admin/members/${s.member}?tab=${tab}`,
  })),
  { name: "instance projects", route: "/admin/projects", path: () => "/admin/projects" },
  {
    name: "instance auth settings",
    route: "/admin/auth-settings",
    path: () => "/admin/auth-settings",
  },
  {
    name: "instance auth providers",
    route: "/admin/auth-providers",
    path: () => "/admin/auth-providers",
  },
  {
    name: "instance auth provider",
    route: "/admin/auth-providers/$provider",
    path: (s: Seeded) => `/admin/auth-providers/${s.authProvider}`,
  },
  {
    name: "instance SCIM tokens",
    route: "/admin/scim-tokens",
    path: () => "/admin/scim-tokens",
  },
  { name: "instance mail", route: "/admin/mail", path: () => "/admin/mail" },
  { name: "instance audit log", route: "/admin/audit", path: () => "/admin/audit" },
  { name: "instance webhooks", route: "/admin/webhooks", path: () => "/admin/webhooks" },
  ...["settings", "deliveries"].map((tab) => ({
    name: `instance webhook: ${tab}`,
    route: "/admin/webhooks/$webhook",
    path: (s: Seeded) => `/admin/webhooks/${s.instanceWebhook}?tab=${tab}`,
  })),
  {
    name: "instance audit entry",
    route: "/admin/audit/$entry",
    path: (s) => `/admin/audit/${s.auditEntry}?project_key=${s.project}`,
  },
  { name: "overview", route: "/projects/$project", path: (s) => `/projects/${s.project}` },
  ...[
    "flags",
    "flags/-/new",
    "experiments",
    "experiments/-/new",
    "stale-flags",
    "segments",
    "segments/-/new",
    "change-requests",
    "environments",
    "unit-types",
    "attributes",
    "members",
    "tokens",
    "settings",
    "webhooks",
    "sinks",
    "sinks/new",
    "audit",
    "event-volume",
    "import-export",
  ].map((screen) => ({
    name: `project ${screen}`,
    route: `/projects/$project/${screen}`,
    path: (s: Seeded) => `/projects/${s.project}/${screen}`,
  })),
  {
    name: "project flag",
    route: "/projects/$project/flags/$flag",
    path: (s) => `/projects/${s.project}/flags/${sweepFlag}`,
  },
  {
    name: "project environment",
    route: "/projects/$project/environments/$environment",
    path: (s) => `/projects/${s.project}/environments/${sweepEnvironment}`,
  },
  {
    name: "project environment: SDKs",
    route: "/projects/$project/environments/$environment",
    path: (s) => `/projects/${s.project}/environments/${sweepEnvironment}?tab=sdks`,
  },
  {
    name: "project attributes: discovered",
    route: "/projects/$project/attributes",
    path: (s) => `/projects/${s.project}/attributes?tab=discovered`,
  },
  {
    name: "project experiment",
    route: "/projects/$project/experiments/$experiment",
    path: (s) => `/projects/${s.project}/experiments/${sweepExperiment}`,
  },
  {
    name: "project change requests: awaiting my decision",
    route: "/projects/$project/change-requests",
    path: (s) => `/projects/${s.project}/change-requests?tab=awaiting`,
  },
  {
    name: "project change request",
    route: "/projects/$project/change-requests/$changeRequest",
    path: (s) => `/projects/${s.project}/change-requests/${s.changeRequest}`,
  },
  {
    name: "project segment",
    route: "/projects/$project/segments/$segment",
    path: (s) => `/projects/${s.project}/segments/${sweepSegment}`,
  },
  ...["settings", "deliveries"].map((tab) => ({
    name: `project webhook: ${tab}`,
    route: "/projects/$project/webhooks/$webhook",
    path: (s: Seeded) => `/projects/${s.project}/webhooks/${s.webhook}?tab=${tab}`,
  })),
  {
    name: "project sink",
    route: "/projects/$project/sinks/$sink",
    path: (s) => `/projects/${s.project}/sinks/${s.sink}`,
  },
  {
    name: "project audit entry",
    route: "/projects/$project/audit/$entry",
    path: (s) => `/projects/${s.project}/audit/${s.auditEntry}`,
  },
  { name: "not found", route: "/$", path: () => "/no-such-screen" },
];

/**
 * Routes of the console that are no screen of their own: the instance's
 * layout, which only frames its screens, and the personal settings, which
 * open on the profile.
 */
const notScreens = ["/admin", "/settings"];

/**
 * Every address the console's router can navigate to, read from its
 * generated route tree, so a screen added without a place in the sweep
 * fails it.
 */
function routedPaths(): string[] {
  const tree = readFileSync(
    new URL("../../apps/console/src/routeTree.gen.ts", import.meta.url),
    "utf8",
  );
  const to = /^\s*to:\n((?:\s*\| '[^']*'\n)+)/m.exec(tree)?.[1];
  if (!to) throw new Error("the route tree lists no navigable route");
  return [...to.matchAll(/'([^']*)'/g)].map((match) => match[1]!);
}

/**
 * A project, an invited member and an active one with a password reset of
 * the sweep's own, so no other spec's changes show.
 */
async function seed(): Promise<Seeded> {
  const project = longKey(`sweep-${Date.now().toString(36)}`);
  must(
    `seeding ${project}`,
    await createProject({
      client: managementClient(),
      body: { key: project, name: longName.slice(0, 200) },
    }),
  );
  for (const n of [1, 2, 3, 4, 5, 6]) {
    must(
      `creating environment ${n}`,
      await createEnvironment({
        client: managementClient(),
        path: { project },
        body: { key: longKey(`env${n}`), name: `${n} ${longName}`.slice(0, 200) },
      }),
    );
  }
  const invited = await seedInvitation("sweep", longName.slice(0, 200));
  const active = await seedMember("sweep-reset", "Sweep Member", "harness-sweep-reset-6172");
  const reset = must(
    "issuing a password reset",
    await issuePasswordReset({ client: managementClient(), path: { member: active.id } }),
  );
  await seedFlag(
    project,
    stringFlag(sweepFlag, {
      name: longName.slice(0, 200),
      tags: Array.from({ length: 30 }, (_, n) => `a-fairly-long-tag-${n}`),
    }),
  );
  await seedFlag(
    project,
    stringFlag(`flag_${"long_key_".repeat(13)}end`, { name: longName.slice(0, 200) }),
  );
  must(
    `creating ${sweepSegment}`,
    await createSegment({
      client: managementClient(),
      path: { project },
      body: {
        key: sweepSegment,
        name: "Sweep segment",
        conditions: [{ kind: "attribute", attribute: "country", op: "in", value: ["NL", "DE"] }],
      },
    }),
  );
  must(
    `creating ${sweepExperiment}`,
    await createExperiment({
      client: managementClient(),
      path: { project },
      body: {
        key: sweepExperiment,
        name: "Sweep experiment",
        hypothesis: "The treatment converts better.",
        flag: sweepFlag,
        unit_type: "user",
        variations: [
          { variation: "control", weight: 5000 },
          { variation: "treatment", weight: 5000 },
        ],
        expected_end_at: "2027-01-31",
      },
    }),
  );
  const changeRequest = must(
    `proposing a change to ${sweepFlag}`,
    await createChangeRequest({
      client: managementClient(),
      path: { project },
      body: {
        kind: "flag_configuration",
        flag: sweepFlag,
        environment: "production",
        title: longTitle,
        description: "So the sweep sees a change request open for review.",
        proposed: { on: true, off_variation: "control", fallthrough: "treatment" },
        open: true,
      },
    }),
  );
  // Disabled, so nothing is ever posted to them and no delivery fails.
  const webhook = must(
    `creating a webhook in ${project}`,
    await createWebhook({
      client: managementClient(),
      path: { project },
      body: {
        name: "Sweep webhook",
        url: longUrl,
        events: ["flag.kill_switch"],
        enabled: false,
      },
    }),
  );
  // Disabled, so nothing is ever delivered to it.
  const sink = must(
    `creating a sink in ${project}`,
    await createSink({
      client: managementClient(),
      path: { project },
      body: {
        name: "Sweep sink",
        kind: "webhook",
        webhook: { url: longUrl },
      },
    }),
  );
  const instanceWebhook = must(
    "creating an instance webhook",
    await createInstanceWebhook({
      client: managementClient(),
      body: {
        name: "Sweep instance webhook",
        url: longUrl,
        events: ["audit.entry"],
        enabled: false,
      },
    }),
  );
  // Disabled, so the sign-in page of every spec after this one is as it was.
  const authProvider = `sweep-${Date.now().toString(36)}-idp`;
  must(
    "adding an auth provider",
    await createAuthProvider({
      client: managementClient(),
      body: {
        slug: authProvider,
        kind: "oidc",
        display_name: "Sweep provider",
        enabled: false,
        domains: ["sweep.test"],
        issuer: "https://login.sweep.test",
        client_id: "flagnetic",
        client_secret: "unused",
      },
    }),
  );
  const audit = must(
    `listing the audit log of ${project}`,
    await listProjectAuditEntries({ client: managementClient(), path: { project } }),
  );
  const auditEntry = audit.items[0]?.id;
  if (!auditEntry) throw new Error(`${project} has no audit entry`);
  return {
    project,
    member: invited.id,
    invitation: pathOf(invited.link),
    passwordReset: pathOf(reset.url),
    auditEntry,
    changeRequest: changeRequest.id,
    webhook: webhook.id,
    sink: sink.id,
    instanceWebhook: instanceWebhook.id,
    authProvider,
  };
}

/** A screen has loaded once its heading is up and no skeleton remains. */
async function settled(page: Page): Promise<void> {
  await expect(page.getByRole("heading").first()).toBeVisible();
  await expect(page.locator('[data-slot="skeleton"]')).toHaveCount(0);
}

/**
 * Visits every screen, signed out and then signed in as the instance
 * admin, and checks each once it has settled.
 */
async function walk(page: Page, seeded: Seeded, check: (route: Route) => Promise<void>) {
  const visit = async (route: Route) => {
    await test.step(route.name, async () => {
      await page.goto(route.path(seeded));
      await settled(page);
      await check(route);
    });
  };
  for (const route of signedOut) await visit(route);
  await signIn(page);
  for (const route of signedIn) await visit(route);
}

test.describe("accessibility", () => {
  test.describe.configure({ timeout: 180_000 });

  let seeded: Seeded;
  test.beforeAll(async () => {
    seeded = await seed();
  });

  test("the sweep visits every routed screen", () => {
    const visited = new Set([...signedOut, ...signedIn].map((route) => route.route));
    const missing = routedPaths().filter(
      (path) => !visited.has(path) && !notScreens.includes(path),
    );
    expect(missing, "routed screens the sweep does not visit").toEqual([]);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`every routed screen in ${theme} has no serious or critical violation`, async ({
      page,
    }) => {
      await useTheme(page, theme);
      await walk(page, seeded, async (route) => {
        expect.soft(await seriousViolations(page), `${route.name} (${theme})`).toEqual([]);
      });
    });
  }

  for (const width of [390, 768, 1024, 1440, 1920]) {
    test(`nothing scrolls sideways at ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: width < 768 ? 844 : 1024 });
      await walk(page, seeded, async (route) => {
        const overflow = await sidewaysOverflow(page);
        expect
          .soft(overflow, `${route.name} scrolls sideways by ${overflow}px at ${width} px`)
          .toBeLessThanOrEqual(0);
      });
    });
  }

  // The widths the console is laid out for; on a phone only the on-call
  // path's screens are held to it.
  for (const width of [1440, 1024, 768]) {
    test(`no control covers another at ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await walk(page, seeded, async (route) => {
        expect.soft(await overlappingControls(page), `${route.name} at ${width} px`).toEqual([]);
      });
    });
  }

  test("no control covers another at 390 px on the on-call path's screens", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const onCall = new Set(["sign-in", "project flags", "project flag", "project change request"]);
    await walk(page, seeded, async (route) => {
      if (!onCall.has(route.name)) return;
      expect.soft(await overlappingControls(page), `${route.name} at 390 px`).toEqual([]);
    });
  });

  test("content stops at 1600 px on a 1920 px screen", async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 });
    await walk(page, seeded, async (route) => {
      const width = await contentWidth(page);
      expect.soft(width, `${route.name} is ${width}px wide`).toBeLessThanOrEqual(1600);
    });
  });

  test("the context panel opens as a sheet by keyboard under 1024 px", async ({ page }) => {
    // The sheet replaces the column below 1024 px.
    await page.setViewportSize({ width: 1000, height: 900 });
    await signIn(page);
    await page.goto(`/projects/${seeded.project}/flags`);
    await expect(page.locator("main h1").first()).toBeVisible();

    const open = page.locator('header button[aria-haspopup="dialog"]');
    await open.focus();
    await page.keyboard.press("Enter");
    const sheet = page.getByRole("dialog");
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole("link").first()).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
    await expect(open).toBeFocused();
  });
});
