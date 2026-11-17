import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams, useRouterState } from "@tanstack/react-router";

import {
  instanceScreens,
  isProjectSettingsPath,
  openableSettingsScreens,
} from "@/components/area-screens";
import { cn } from "@/lib/cn";
import { meQuery, projectMeQuery } from "@/lib/queries";

const rowClassName =
  "flex flex-wrap gap-1 border-b px-4 py-2 [&_a]:inline-flex [&_a]:h-8 [&_a]:items-center [&_a]:rounded-md [&_a]:px-2 [&_a]:text-sm [&_a]:text-foreground-secondary [&_a]:hover:bg-foreground/[0.06] [&_a]:hover:text-foreground [&_a[aria-current=page]]:bg-raised [&_a[aria-current=page]]:font-medium [&_a[aria-current=page]]:text-foreground";

/**
 * The screens of the area the page belongs to, as a row of links above it:
 * the project's settings and the instance's. The sidebar lists neither, and
 * the second-level column that will hold them is not there yet, so this is
 * how the member moves between them. The personal settings keep their own
 * row.
 */
export function AreaNavigation() {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const { project } = useParams({ strict: false });
  if (pathname === "/admin" || pathname.startsWith("/admin/")) return <InstanceScreens />;
  if (project !== undefined && isProjectSettingsPath(project, pathname)) {
    return <ProjectSettingsScreens project={project} />;
  }
  return null;
}

function ProjectSettingsScreens({ project }: { project: string }) {
  const { i18n, t } = useLingui();
  const me = useQuery(projectMeQuery(project));
  if (me.data === undefined) return null;
  const screens = openableSettingsScreens(new Set(me.data.permissions));
  return (
    // Named so that "Project", the sidebar's, still names exactly one
    // navigation in the window.
    <nav aria-label={t`Settings`} className={cn(rowClassName)}>
      {screens.map((screen) => (
        <Link key={screen.to} to={screen.to} params={{ project }}>
          {i18n._(screen.label)}
        </Link>
      ))}
    </nav>
  );
}

function InstanceScreens() {
  const { i18n, t } = useLingui();
  const me = useQuery(meQuery);
  if (!me.data?.member?.instance_admin) return null;
  return (
    <nav aria-label={t`Instance`} className={rowClassName}>
      {instanceScreens.map((screen) => (
        <Link key={screen.to} to={screen.to}>
          {i18n._(screen.label)}
        </Link>
      ))}
    </nav>
  );
}
