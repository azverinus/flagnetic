import type { MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { Link, type LinkProps } from "@tanstack/react-router";
import {
  BoxesIcon,
  FlagIcon,
  FlaskConicalIcon,
  HistoryIcon,
  LayersIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  type LucideIcon,
  RotateCwIcon,
  ServerIcon,
  SettingsIcon,
  TagsIcon,
  UsersIcon,
  UsersRoundIcon,
} from "lucide-react";
import { useId } from "react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { Permission } from "@/lib/access-names";
import { cn } from "@/lib/cn";
import { ApiError } from "@/lib/errors";
import { meQuery, projectMeQuery } from "@/lib/queries";

type Screen = {
  label: MessageDescriptor;
  icon: LucideIcon;
  exact?: boolean;
};

/** The routes of one project whose only parameter is the project's key. */
type ProjectScreenRoute = Exclude<
  Extract<NonNullable<LinkProps["to"]>, `/projects/$project${string}`>,
  `/projects/$project${string}$${string}`
>;

type ProjectScreen = Screen & {
  /** A screen of the project, opened with the project's key alone. */
  to: ProjectScreenRoute;
  /** The permission the screen needs; the screen is listed only to a caller holding it. */
  permission: Permission;
};

/** The screens of one project, in the order the sidebar lists them. */
const projectScreens = [
  {
    to: "/projects/$project",
    label: msg`Overview`,
    icon: LayoutDashboardIcon,
    permission: "project.read",
    exact: true,
  },
  {
    to: "/projects/$project/flags",
    label: msg`Flags`,
    icon: FlagIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/experiments",
    label: msg`Experiments`,
    icon: FlaskConicalIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/segments",
    label: msg`Segments`,
    icon: UsersRoundIcon,
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
    to: "/projects/$project/audit",
    label: msg`Audit log`,
    icon: HistoryIcon,
    permission: "project.read",
  },
  {
    to: "/projects/$project/settings",
    label: msg`Settings`,
    icon: SettingsIcon,
    permission: "project.update",
  },
] satisfies ProjectScreen[];

type InstanceScreen = Screen & {
  to: "/admin/projects" | "/admin/members" | "/admin/auth-settings" | "/admin/audit";
};

/** The screens of the instance, listed to instance admins. */
const instanceScreens: InstanceScreen[] = [
  { to: "/admin/projects", label: msg`Projects`, icon: LayersIcon },
  { to: "/admin/members", label: msg`Members`, icon: UsersIcon },
  { to: "/admin/auth-settings", label: msg`Auth settings`, icon: KeyRoundIcon },
  { to: "/admin/audit", label: msg`Audit log`, icon: HistoryIcon },
];

const linkClassName =
  "flex h-9 items-center gap-2 rounded-md px-2.5 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground data-[status=active]:bg-accent data-[status=active]:font-medium data-[status=active]:text-foreground";

/** What a screen's link shows: its icon, and its name unless the sidebar is a rail. */
function ScreenLabel({ screen, collapsed }: { screen: Screen; collapsed: boolean }) {
  const { i18n } = useLingui();
  const Icon = screen.icon;
  return (
    <>
      <Icon aria-hidden className="size-4 shrink-0" />
      <span className={cn("truncate", collapsed ? "sr-only" : "sr-only md:not-sr-only")}>
        {i18n._(screen.label)}
      </span>
    </>
  );
}

/**
 * The screens of the open project the caller may obtain something from,
 * by the permissions the server answers for them there.
 */
export function ProjectNavigation({ project, collapsed }: { project: string; collapsed: boolean }) {
  const { t } = useLingui();
  const me = useQuery(projectMeQuery(project));
  if (me.data === undefined) {
    // A first read that failed is announced by the page, with its own way
    // to ask again; the navigation says in place that its screens are
    // missing and offers to read them again, without a second alert. An
    // unknown project and one the caller holds no grant in have no screens
    // to list.
    if (me.isError) {
      const refused =
        me.error instanceof ApiError && (me.error.status === 403 || me.error.status === 404);
      if (refused) return null;
      return (
        <nav aria-label={t`Project`} className="flex flex-col gap-1">
          <p
            className={cn(
              "px-2.5 text-xs text-muted-foreground",
              collapsed ? "sr-only" : "sr-only md:not-sr-only",
            )}
          >
            {t`The project's screens could not be read.`}
          </p>
          <Button
            variant="ghost"
            className="h-9 justify-start gap-2 px-2.5"
            title={t`Try again`}
            onClick={() => void me.refetch()}
          >
            <RotateCwIcon aria-hidden className="size-4 shrink-0" />
            <span
              className={cn("truncate", collapsed ? "sr-only" : "sr-only md:not-sr-only")}
            >{t`Try again`}</span>
          </Button>
        </nav>
      );
    }
    return (
      <div className="flex flex-col gap-1" aria-busy>
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  }
  const held = new Set<string>(me.data.permissions);
  return (
    <nav aria-label={t`Project`} className="flex flex-col gap-1">
      {projectScreens
        .filter((screen) => held.has(screen.permission))
        .map((screen) => (
          <Link
            key={screen.to}
            to={screen.to}
            params={{ project }}
            activeOptions={{ exact: screen.exact ?? false }}
            className={linkClassName}
          >
            <ScreenLabel screen={screen} collapsed={collapsed} />
          </Link>
        ))}
    </nav>
  );
}

/** The instance's screens, for an instance admin only. */
export function InstanceNavigation({ collapsed }: { collapsed: boolean }) {
  const { t } = useLingui();
  const heading = useId();
  const { data: me } = useSuspenseQuery(meQuery);
  if (!me.member?.instance_admin) return null;
  return (
    <nav aria-labelledby={heading} className="flex flex-col gap-1">
      <h2
        id={heading}
        className={cn(
          "px-2.5 pt-2 text-xs font-medium tracking-wide text-muted-foreground uppercase",
          collapsed ? "sr-only" : "sr-only md:not-sr-only",
        )}
      >
        {t`Instance`}
      </h2>
      {instanceScreens.map((screen) => (
        <Link
          key={screen.to}
          to={screen.to}
          activeOptions={{ exact: screen.exact ?? false }}
          className={linkClassName}
        >
          <ScreenLabel screen={screen} collapsed={collapsed} />
        </Link>
      ))}
    </nav>
  );
}
