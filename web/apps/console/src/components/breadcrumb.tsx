import { useLingui } from "@lingui/react/macro";
import { Link, useMatches } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";

import { useProjectInAddress } from "@/lib/queries";

/**
 * The crumb of the route of one project, which the breadcrumb calls by the
 * project's name rather than by a fixed label.
 */
export const projectNameCrumb: unique symbol = Symbol();

/**
 * Where the member is, from the crumb each matched route declares in its
 * static data; the last crumb is the page itself.
 */
export function Breadcrumb() {
  const { i18n, t } = useLingui();
  const { key, project } = useProjectInAddress();
  const crumbs = useMatches().flatMap((match) => {
    const crumb = match.staticData.crumb;
    if (crumb === undefined) return [];
    const label = crumb === projectNameCrumb ? (project?.name ?? key ?? "") : i18n._(crumb);
    return [{ id: match.id, to: match.pathname, label }];
  });
  if (crumbs.length === 0) return null;
  return (
    <nav aria-label={t`Breadcrumb`} className="min-w-0">
      <ol className="flex min-w-0 items-center gap-1 text-sm">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1;
          return (
            <li key={crumb.id} className="flex min-w-0 items-center gap-1">
              {index > 0 ? (
                <ChevronRightIcon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
              ) : null}
              {last ? (
                <span aria-current="page" className="truncate font-medium" title={crumb.label}>
                  {crumb.label}
                </span>
              ) : (
                <Link
                  to={crumb.to}
                  className="truncate text-muted-foreground hover:text-foreground"
                  title={crumb.label}
                >
                  {crumb.label}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
