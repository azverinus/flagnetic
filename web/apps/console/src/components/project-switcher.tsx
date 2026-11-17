import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CheckIcon, ChevronsUpDownIcon, FlagIcon } from "lucide-react";
import { useState } from "react";

import { QueryError } from "@/components/query-error";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import { useSidebar } from "@/components/ui/sidebar";
import { cn } from "@/lib/cn";
import { projectQuery, projectsQuery } from "@/lib/queries";
import { useShellProject } from "@/lib/use-shell-project";

/**
 * The project the sidebar shows, and the way to another: a search over the
 * projects the member holds a grant in, read only when the switcher opens,
 * and the full list behind "All projects". Outside a project's own screens
 * it shows the project this browser showed last, or invites the member to
 * choose one.
 */
export function ProjectSwitcher() {
  const { t } = useLingui();
  const { collapsed } = useSidebar();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const { key: shown, inAddress } = useShellProject();
  const { data: project } = useQuery({
    ...projectQuery(shown ?? ""),
    enabled: shown !== undefined,
  });
  const projects = useQuery({ ...projectsQuery, enabled: open });

  const label = shown ? (project?.name ?? shown) : t`Choose a project`;
  const needle = search.trim().toLowerCase();
  const matching = (projects.data ?? []).filter(
    (candidate) =>
      needle === "" ||
      candidate.key.toLowerCase().includes(needle) ||
      candidate.name.toLowerCase().includes(needle),
  );
  const change = (next: boolean) => {
    setOpen(next);
    if (!next) setSearch("");
  };

  return (
    <Popover open={open} onOpenChange={change}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left text-sm transition-colors hover:bg-foreground/[0.06]",
            collapsed && "justify-center px-0",
          )}
        >
          <span
            aria-hidden
            className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"
          >
            <FlagIcon className="size-4" />
          </span>
          {/* The visible text is the button's name, so it is read out in the rail too. */}
          <span className={cn("flex min-w-0 flex-1 flex-col", collapsed && "sr-only")}>
            <span className="sr-only">{t`Project:`} </span>
            <span className="truncate leading-4 font-medium">{label}</span>{" "}
            {shown ? (
              <span className="truncate font-mono text-xs text-foreground-secondary">{shown}</span>
            ) : null}
          </span>
          {collapsed ? null : (
            <ChevronsUpDownIcon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="flex w-72 flex-col gap-2 p-2">
        <Input
          aria-label={t`Search projects`}
          placeholder={t`Search projects`}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
        {projects.isPending ? (
          <div className="flex flex-col gap-1 p-1" aria-busy>
            <Skeleton className="h-7 w-full" />
            <Skeleton className="h-7 w-full" />
          </div>
        ) : projects.isError ? (
          <QueryError error={projects.error} onRetry={() => void projects.refetch()} />
        ) : matching.length === 0 ? (
          <p className="px-2 py-1.5 text-sm text-muted-foreground">
            <Trans>No project matches that.</Trans>
          </p>
        ) : (
          <ul aria-label={t`Projects`} className="flex max-h-72 flex-col gap-0.5 overflow-y-auto">
            {matching.map((candidate) => (
              <li key={candidate.id}>
                <Link
                  to="/projects/$project"
                  params={{ project: candidate.key }}
                  onClick={() => change(false)}
                  aria-current={inAddress && candidate.key === shown ? "page" : undefined}
                  className="flex items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
                >
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate">{candidate.name}</span>
                    <span className="truncate font-mono text-xs text-muted-foreground">
                      {candidate.key}
                    </span>
                  </span>
                  {candidate.key === shown ? (
                    <CheckIcon aria-hidden className="size-4 shrink-0" />
                  ) : null}
                </Link>
              </li>
            ))}
          </ul>
        )}
        <div className="border-t pt-2">
          <Link
            to="/"
            search={{ all: true }}
            onClick={() => change(false)}
            className="block rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
          >
            <Trans>All projects</Trans>
          </Link>
        </div>
      </PopoverContent>
    </Popover>
  );
}
