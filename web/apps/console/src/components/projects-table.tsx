import type { Project } from "@flagnetic/api";
import { useLingui } from "@lingui/react/macro";
import { Link } from "@tanstack/react-router";
import { type ReactNode, useMemo } from "react";

import { Table, type TableColumn } from "@/components/ui/table";
import { formatNumber } from "@/i18n/format";

export type ProjectsTableProps = {
  caption: string;
  projects: Project[];
  loading: boolean;
  /** What the table says when there is no project. */
  empty?: ReactNode;
};

/** Projects by name, each opening its overview, with its key and how many members it has. */
export function ProjectsTable({ caption, projects, loading, empty }: ProjectsTableProps) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<Project>[]>(
    () => [
      {
        id: "name",
        header: t`Name`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project"
            params={{ project: row.original.key }}
            className="font-medium hover:underline"
          >
            {row.original.name}
          </Link>
        ),
      },
      {
        id: "key",
        header: t`Key`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.key}</code>,
      },
      {
        id: "members",
        header: t`Members`,
        cell: ({ row }) => formatNumber(i18n, row.original.member_count),
      },
    ],
    [i18n, t],
  );
  return (
    <Table
      caption={caption}
      hideCaption
      columns={columns}
      data={projects}
      loading={loading}
      empty={empty}
      getRowId={(project) => project.id}
    />
  );
}
