import { plural } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { Link, useRouterState } from "@tanstack/react-router";
import { RotateCwIcon, ServerCogIcon, SettingsIcon } from "lucide-react";
import type { ReactNode } from "react";

import {
  activityScreens,
  dailyScreens,
  isProjectSettingsPath,
  openableSettingsScreens,
  type ProjectScreen,
} from "@/components/area-screens";
import { SinkAttentionMarker } from "@/components/sink-banner";
import { Skeleton } from "@/components/ui/skeleton";
import {
  SidebarGroup,
  SidebarLinkLabel,
  sidebarLinkClassName,
  useSidebar,
} from "@/components/ui/sidebar";
import { WebhookAttentionMarker } from "@/components/webhook-disabled-banner";
import { cn } from "@/lib/cn";
import { ApiError } from "@/lib/errors";
import { awaitingCountLimit, awaitingDecisionQuery, meQuery, projectMeQuery } from "@/lib/queries";

/**
 * How many open change requests await the caller's decision, beside the
 * change requests screen; nothing when none does. Past the count it reads,
 * it says there are more. In the rail it is a dot on the icon.
 */
function AwaitingDecisionCount({ project }: { project: string }) {
  const { t } = useLingui();
  const { collapsed } = useSidebar();
  const awaiting = useQuery(awaitingDecisionQuery(project));
  const count = awaiting.data?.items.length ?? 0;
  if (count === 0) return null;
  const more = count > awaitingCountLimit;
  const shown = more ? `${awaitingCountLimit}+` : String(count);
  const label = more
    ? t`More than ${awaitingCountLimit} change requests await your decision`
    : t`${plural(count, {
        one: "# change request awaits your decision",
        other: "# change requests await your decision",
      })}`;
  return (
    <span
      className={cn(
        "rounded-full bg-primary text-xs leading-5 font-medium text-primary-foreground tabular-nums",
        collapsed ? "absolute top-0.5 right-0.5 size-2" : "ml-auto px-1.5",
      )}
      title={label}
    >
      <span aria-hidden className={cn(collapsed && "sr-only")}>
        {shown}
      </span>
      <span className="sr-only">{label}</span>
    </span>
  );
}

/** Puts what waits at a link on its icon in the rail, and at its end in the column. */
function Attention({ children }: { children: ReactNode }) {
  const { collapsed } = useSidebar();
  return <span className={cn(collapsed ? "absolute top-1 right-1" : "ml-auto")}>{children}</span>;
}

function ScreenLink({
  screen,
  project,
  held,
}: {
  screen: ProjectScreen;
  project: string;
  held: ReadonlySet<string>;
}) {
  const { i18n } = useLingui();
  const { collapsed } = useSidebar();
  const Icon = screen.icon;
  const name = i18n._(screen.label);
  // A member who decides change requests is taken to those awaiting their
  // decision, which the counter beside it counts.
  const decides =
    screen.to === "/projects/$project/change-requests" && held.has("change_request.approve");
  return (
    <Link
      to={screen.to}
      params={{ project }}
      {...(decides ? { search: { tab: "awaiting" } } : {})}
      activeOptions={{ exact: screen.exact ?? false, includeSearch: false }}
      title={collapsed ? name : undefined}
      className={sidebarLinkClassName(collapsed)}
    >
      <Icon aria-hidden />
      <SidebarLinkLabel>{name}</SidebarLinkLabel>
      {decides ? <AwaitingDecisionCount project={project} /> : null}
    </Link>
  );
}

/**
 * The link to the project's settings: it opens the first of their screens
 * the caller may open, and stays current on all of them. The attention of
 * the sinks and the webhooks shows on it, since their screens are one
 * level further in.
 */
function ProjectSettingsLink({ project, held }: { project: string; held: ReadonlySet<string> }) {
  const { t } = useLingui();
  const { collapsed } = useSidebar();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const first = openableSettingsScreens(held)[0];
  if (first === undefined) return null;
  const current = isProjectSettingsPath(project, pathname);
  const name = t`Project settings`;
  return (
    <Link
      to={first.to}
      params={{ project }}
      aria-current={current ? "page" : undefined}
      title={collapsed ? name : undefined}
      className={sidebarLinkClassName(collapsed)}
    >
      <SettingsIcon aria-hidden />
      <SidebarLinkLabel>{name}</SidebarLinkLabel>
      <Attention>
        <span className="flex items-center gap-1">
          {held.has("sink.manage") ? <SinkAttentionMarker project={project} /> : null}
          {held.has("webhook.manage") ? <WebhookAttentionMarker project={project} /> : null}
        </span>
      </Attention>
    </Link>
  );
}

/**
 * The screens of the project in the sidebar, by the permissions the server
 * answers for them there: the daily ones, the activity ones, and at the
 * foot the link to the project's settings. With no `current` project the
 * screens are those of the project this browser showed last, none of them
 * current.
 */
export function ProjectNavigation({ project }: { project: string }) {
  const { t } = useLingui();
  const { collapsed } = useSidebar();
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
      if (refused) return <div className="flex-1" />;
      return (
        <nav aria-label={t`Project`} className="flex flex-1 flex-col gap-1">
          <p className={cn("px-2 text-xs text-muted-foreground", collapsed && "sr-only")}>
            {t`The project's screens could not be read.`}
          </p>
          <button
            type="button"
            title={t`Try again`}
            onClick={() => void me.refetch()}
            className={sidebarLinkClassName(collapsed)}
          >
            <RotateCwIcon aria-hidden />
            <SidebarLinkLabel>{t`Try again`}</SidebarLinkLabel>
          </button>
        </nav>
      );
    }
    return (
      <div className="flex flex-1 flex-col gap-1 pt-2" aria-busy>
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
      </div>
    );
  }
  const held = new Set<string>(me.data.permissions);
  const listed = (screens: ProjectScreen[]) =>
    screens
      .filter((screen) => held.has(screen.permission))
      .map((screen) => (
        <ScreenLink key={screen.to} screen={screen} project={project} held={held} />
      ));
  const activity = listed(activityScreens);
  return (
    <nav aria-label={t`Project`} className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
      <SidebarGroup className="pt-2">{listed(dailyScreens)}</SidebarGroup>
      {activity.length > 0 ? <SidebarGroup label={t`Activity`}>{activity}</SidebarGroup> : null}
      <SidebarGroup className="mt-auto pt-2">
        <ProjectSettingsLink project={project} held={held} />
      </SidebarGroup>
    </nav>
  );
}

/** The link to the instance's screens, for an instance admin only. */
export function InstanceLink() {
  const { t } = useLingui();
  const { collapsed } = useSidebar();
  const { data: me } = useSuspenseQuery(meQuery);
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  if (!me.member?.instance_admin) return null;
  const name = t`Instance`;
  return (
    <Link
      to="/admin/projects"
      aria-current={pathname === "/admin" || pathname.startsWith("/admin/") ? "page" : undefined}
      title={collapsed ? name : undefined}
      className={sidebarLinkClassName(collapsed)}
    >
      <ServerCogIcon aria-hidden />
      <SidebarLinkLabel>{name}</SidebarLinkLabel>
    </Link>
  );
}
