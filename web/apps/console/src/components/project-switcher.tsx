import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CheckIcon, ChevronsUpDownIcon, FolderIcon } from "lucide-react";
import { useState } from "react";

import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/cn";
import { projectsQuery, useProjectInAddress } from "@/lib/queries";

/**
 * The project the member is in, and the way to another: a search over the
 * projects they hold a grant in, read only when the switcher opens, and
 * the full list behind "All projects".
 */
export function ProjectSwitcher({ collapsed }: { collapsed: boolean }) {
  const { t } = useLingui();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const { key: current, project: currentProject } = useProjectInAddress();
  const projects = useQuery({ ...projectsQuery, enabled: open });

  const label = current
    ? (currentProject?.name ?? current)
    : t({ message: "none chosen", comment: "The project switcher, when no project is open." });
  const needle = search.trim().toLowerCase();
  const matching = (projects.data ?? []).filter(
    (project) =>
      needle === "" ||
      project.key.toLowerCase().includes(needle) ||
      project.name.toLowerCase().includes(needle),
  );
  const change = (next: boolean) => {
    setOpen(next);
    if (!next) setSearch("");
  };

  return (
    <Popover open={open} onOpenChange={change}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          aria-label={t`Project: ${label}`}
          className={cn("w-full justify-start px-2.5", collapsed && "justify-center px-0")}
        >
          <FolderIcon aria-hidden />
          <span
            className={cn(
              "min-w-0 flex-1 truncate text-left",
              collapsed ? "sr-only" : "sr-only md:not-sr-only",
            )}
          >
            {label}
          </span>
          <ChevronsUpDownIcon
            aria-hidden
            className={cn("text-muted-foreground", collapsed ? "hidden" : "hidden md:block")}
          />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="flex w-72 flex-col gap-2 p-2">
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
            {matching.map((project) => (
              <li key={project.id}>
                <Link
                  to="/projects/$project"
                  params={{ project: project.key }}
                  onClick={() => change(false)}
                  aria-current={project.key === current ? "page" : undefined}
                  className="flex items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
                >
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate">{project.name}</span>
                    <span className="truncate font-mono text-xs text-muted-foreground">
                      {project.key}
                    </span>
                  </span>
                  {project.key === current ? (
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
