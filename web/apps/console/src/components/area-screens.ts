import type { MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import type { LinkProps } from "@tanstack/react-router";
import {
  ActivityIcon,
  ArrowLeftRightIcon,
  BoxesIcon,
  DatabaseZapIcon,
  FlagIcon,
  FlaskConicalIcon,
  GitPullRequestArrowIcon,
  HistoryIcon,
  KeyRoundIcon,
  LayersIcon,
  LayoutDashboardIcon,
  type LucideIcon,
  MailIcon,
  ServerIcon,
  SettingsIcon,
  ShieldCheckIcon,
  TagsIcon,
  UserRoundCogIcon,
  UsersIcon,
  UsersRoundIcon,
  WebhookIcon,
} from "lucide-react";

import type { Permission } from "@/lib/access-names";

type Screen = {
  label: MessageDescriptor;
  icon: LucideIcon;
};

/** The routes of one project whose only parameter is the project's key. */
type ProjectScreenRoute = Exclude<
  Extract<NonNullable<LinkProps["to"]>, `/projects/$project${string}`>,
  `/projects/$project${string}$${string}`
>;

export type ProjectScreen = Screen & {
  /** A screen of the project, opened with the project's key alone. */
  to: ProjectScreenRoute;
  /** The permission the screen needs; the screen is listed only to a caller holding it. */
  permission: Permission;
  /** Whether only the screen's own address is current, not the ones below it. */
  exact?: boolean;
};

/** The project's daily screens, in the order the sidebar lists them. */
export const dailyScreens = [
  {
    to: "/projects/$project",
    label: msg`Overview`,
    icon: LayoutDashboardIcon,
    permission: "project.read",
    exact: true,
  },
  { to: "/projects/$project/flags", label: msg`Flags`, icon: FlagIcon, permission: "project.read" },
  {
    to: "/projects/$project/segments",
    label: msg`Segments`,
    icon: UsersRoundIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/experiments",
    label: msg`Experiments`,
    icon: FlaskConicalIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/change-requests",
    label: msg`Change requests`,
    icon: GitPullRequestArrowIcon,
    permission: "project.read",
  },
] satisfies ProjectScreen[];

/** The screens under the sidebar's "Activity" label. */
export const activityScreens = [
  {
    to: "/projects/$project/audit",
    label: msg`Audit log`,
    icon: HistoryIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/event-volume",
    label: msg`Event volume`,
    icon: ActivityIcon,
    permission: "project.read",
  },
] satisfies ProjectScreen[];

/**
 * The screens behind the sidebar's "Project settings" link, in the order
 * the link tries them: it opens the first one the caller may open.
 */
export const projectSettingsScreens = [
  {
    to: "/projects/$project/settings",
    label: msg`General`,
    icon: SettingsIcon,
    permission: "project.update",
  },
  {
    to: "/projects/$project/import-export",
    label: msg`Import and export`,
    icon: ArrowLeftRightIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/environments",
    label: msg`Environments`,
    icon: ServerIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/unit-types",
    label: msg`Unit types`,
    icon: BoxesIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/attributes",
    label: msg`Attributes`,
    icon: TagsIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/members",
    label: msg`Members`,
    icon: UsersIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/tokens",
    label: msg`Project tokens`,
    icon: KeyRoundIcon,
    permission: "project_token.manage",
  },
  {
    to: "/projects/$project/webhooks",
    label: msg`Webhooks`,
    icon: WebhookIcon,
    permission: "webhook.manage",
  },
  {
    to: "/projects/$project/sinks",
    label: msg`Sinks`,
    icon: DatabaseZapIcon,
    permission: "sink.manage",
  },
] satisfies ProjectScreen[];

export type InstanceScreen = Screen & {
  to:
    | "/admin/projects"
    | "/admin/members"
    | "/admin/auth-settings"
    | "/admin/auth-providers"
    | "/admin/scim-tokens"
    | "/admin/mail"
    | "/admin/audit"
    | "/admin/webhooks";
};

/** The screens of the instance, listed to instance admins. */
export const instanceScreens: InstanceScreen[] = [
  { to: "/admin/projects", label: msg`Projects`, icon: LayersIcon },
  { to: "/admin/members", label: msg`Members`, icon: UsersIcon },
  { to: "/admin/auth-settings", label: msg`Auth settings`, icon: KeyRoundIcon },
  { to: "/admin/auth-providers", label: msg`Auth providers`, icon: ShieldCheckIcon },
  { to: "/admin/scim-tokens", label: msg`SCIM tokens`, icon: UserRoundCogIcon },
  { to: "/admin/mail", label: msg`Mail`, icon: MailIcon },
  { to: "/admin/webhooks", label: msg`Webhooks`, icon: WebhookIcon },
  { to: "/admin/audit", label: msg`Audit log`, icon: HistoryIcon },
];

/** The screens of a project's settings the caller may open, in the link's order. */
export function openableSettingsScreens(held: ReadonlySet<string>): ProjectScreen[] {
  return projectSettingsScreens.filter((screen) => held.has(screen.permission));
}

/** Whether the path is one of a project's settings screens, or below one. */
export function isProjectSettingsPath(project: string, pathname: string): boolean {
  const base = `/projects/${project}`;
  return projectSettingsScreens.some((screen) => {
    const path = screen.to.replace("/projects/$project", base);
    return pathname === path || pathname.startsWith(`${path}/`);
  });
}
