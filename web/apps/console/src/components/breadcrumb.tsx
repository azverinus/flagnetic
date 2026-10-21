import { useLingui } from "@lingui/react/macro";
import { Link, useMatches } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";

/**
 * Where the member is, from the crumb each matched route declares in its
 * static data; the last crumb is the page itself.
 */
export function Breadcrumb() {
  const { i18n, t } = useLingui();
  const crumbs = useMatches()
    .filter((match) => match.staticData.crumb !== undefined)
    .map((match) => ({ id: match.id, to: match.pathname, label: i18n._(match.staticData.crumb!) }));
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
