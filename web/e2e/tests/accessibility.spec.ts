import { readFileSync } from "node:fs";

import {
  createChangeRequest,
  createExperiment,
  createInstanceWebhook,
  createSegment,
  createWebhook,
  listProjectAuditEntries,
} from "@flagnetic/api";
import { expect, type Page, test } from "@playwright/test";

import { seriousViolations, useTheme } from "../support/axe";
import {
  managementClient,
  must,
  pathOf,
  seedInvitation,
  seedProject,
  signIn,
} from "../support/console";
import { seedFlag, stringFlag } from "../support/flags";

/**
 * The accessibility sweep: axe on every routed screen in light and dark
 * with no serious or critical violation, and no screen scrolling sideways
 * at the 768 px floor the console is laid out for. Each screen is checked
 * softly, so one run lists every failure instead of the first.
 */

/**
 * What the sweep's screens are about: a project, an invited member, their
 * invitation's path and an audit entry of the project, whose flag is
 * {@link sweepFlag}.
 */
type Seeded = {
  project: string;
  member: string;
  invitation: string;
  auditEntry: string;
  changeRequest: string;
  webhook: string;
  instanceWebhook: string;
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
    name: "personal settings: sessions",
    route: "/settings/sessions",
    path: () => "/settings/sessions",
  },
  { name: "instance members", route: "/admin/members", path: () => "/admin/members" },
  ...["sessions", "tokens"].map((tab) => ({
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
    "audit",
    "event-volume",
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

/** A project and an invited member of the sweep's own, so no other spec's changes show. */
async function seed(): Promise<Seeded> {
  const project = await seedProject("sweep");
  const invited = await seedInvitation("sweep", "Sweep Invitee");
  await seedFlag(project, stringFlag(sweepFlag));
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
        title: "Switch the sweep flag on",
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
        url: "https://hooks.example.com/flagnetic",
        events: ["flag.kill_switch"],
        enabled: false,
      },
    }),
  );
  const instanceWebhook = must(
    "creating an instance webhook",
    await createInstanceWebhook({
      client: managementClient(),
      body: {
        name: "Sweep instance webhook",
        url: "https://hooks.example.com/flagnetic",
        events: ["audit.entry"],
        enabled: false,
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
    auditEntry,
    changeRequest: changeRequest.id,
    webhook: webhook.id,
    instanceWebhook: instanceWebhook.id,
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

  test("nothing scrolls sideways at the 768 px floor", async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 });
    await walk(page, seeded, async (route) => {
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      );
      expect
        .soft(overflow, `${route.name} scrolls sideways by ${overflow}px`)
        .toBeLessThanOrEqual(0);
    });
  });
});
