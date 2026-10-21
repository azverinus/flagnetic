import type { MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  BoxesIcon,
  LayersIcon,
  LayoutDashboardIcon,
  type LucideIcon,
  RotateCwIcon,
  ServerIcon,
  SettingsIcon,
  TagsIcon,
} from "lucide-react";
import { useId } from "react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { Permission } from "@/lib/access-names";
import { cn } from "@/lib/cn";
import { ApiError } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";
import { meQuery, projectMeQuery } from "@/lib/queries";

type Screen = {
  label: MessageDescriptor;
  icon: LucideIcon;
  exact?: boolean;
};

type ProjectScreen = Screen & {
  to:
    | "/projects/$project"
    | "/projects/$project/environments"
    | "/projects/$project/unit-types"
    | "/projects/$project/attributes"
    | "/projects/$project/settings";
  /** The permission the screen needs; the screen is listed only to a caller holding it. */
  permission: Permission;
};

/** The screens of one project, in the order the sidebar lists them. */
const projectScreens: ProjectScreen[] = [
  {
    to: "/projects/$project",
    label: msg`Overview`,
    icon: LayoutDashboardIcon,
    permission: "project.read",
    exact: true,
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
    to: "/projects/$project/settings",
    label: msg`Settings`,
    icon: SettingsIcon,
    permission: "project.update",
  },
];

type InstanceScreen = Screen & { to: "/admin/projects" };

/** The screens of the instance, listed to instance admins. */
const instanceScreens: InstanceScreen[] = [
  { to: "/admin/projects", label: msg`Projects`, icon: LayersIcon },
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
  const { i18n, t } = useLingui();
  const me = useQuery(projectMeQuery(project));
  if (me.isPending) {
    return (
      <div className="flex flex-col gap-1" aria-busy>
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  }
  if (me.isError) {
    // An unknown project and one the caller holds no grant in have no
    // screens to list; the page says which it is.
    if (me.error instanceof ApiError && (me.error.status === 403 || me.error.status === 404)) {
      return null;
    }
    return (
      <div role="alert" className="flex flex-col items-start gap-1 px-1">
        <p
          className={cn(
            "text-xs text-muted-foreground",
            collapsed ? "sr-only" : "sr-only md:not-sr-only",
          )}
        >
          {problemMessage(i18n, me.error instanceof ApiError ? me.error : new ApiError(0, {}))}
        </p>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t`Try again`}
          title={t`Try again`}
          onClick={() => void me.refetch()}
        >
          <RotateCwIcon aria-hidden />
        </Button>
      </div>
    );
  }
  const held = new Set(me.data.permissions);
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
