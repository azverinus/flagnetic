import type { SegmentListItem } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { PlusIcon, UsersRoundIcon } from "lucide-react";
import { useMemo } from "react";

import { Can } from "@/components/can";
import { QueryError } from "@/components/query-error";
import { SearchField } from "@/components/search-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Table } from "@/components/ui/table";
import { formatNumber } from "@/i18n/format";
import { segmentsQuery } from "@/lib/queries";
import { filterSegments, type SegmentsSearch, validateSegmentsSearch } from "@/lib/segments";

export const Route = createFileRoute("/_signed-in/projects/$project/segments/")({
  validateSearch: validateSegmentsSearch,
  component: Segments,
});

/** The private filter's values; Radix keeps the empty string for itself. */
const privateChoices = { any: "any", only: "private", none: "not_private" } as const;

/**
 * A project's segments with how many flags' rules name each, filtered by
 * what the address holds; creating one is offered to those who may.
 */
function Segments() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const segments = useQuery(segmentsQuery(project));
  const filter = (patch: Partial<SegmentsSearch>) =>
    void navigate({ search: { ...search, ...patch }, replace: true });

  const newSegment = (
    <Can permission="segment.edit" whenMissing="disable">
      <Button asChild>
        <Link to="/projects/$project/segments/-/new" params={{ project }}>
          <PlusIcon aria-hidden />
          <Trans>New segment</Trans>
        </Link>
      </Button>
    </Can>
  );

  let body;
  if (segments.isError) {
    body = <QueryError error={segments.error} onRetry={() => void segments.refetch()} />;
  } else if (segments.data?.length === 0) {
    body = (
      <EmptyState
        icon={<UsersRoundIcon />}
        title={<Trans>This project has no segment yet.</Trans>}
        description={
          <Trans>A segment names a group of contexts once, for the rules of every flag.</Trans>
        }
        action={newSegment}
      />
    );
  } else {
    const shown = filterSegments(segments.data ?? [], search);
    body =
      segments.data && shown.length === 0 ? (
        <EmptyState
          icon={<UsersRoundIcon />}
          title={<Trans>No segment matches these filters.</Trans>}
          action={
            <Button variant="outline" onClick={() => void navigate({ search: {}, replace: true })}>
              <Trans>Clear filters</Trans>
            </Button>
          }
        />
      ) : (
        <SegmentsTable project={project} segments={shown} loading={segments.isPending} />
      );
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Segments`}
        description={
          <Trans>
            Reusable sets of attribute conditions; a rule targets a segment by name and follows
            every change to it.
          </Trans>
        }
        actions={newSegment}
      />
      <div
        role="search"
        aria-label={t`Filter the segments`}
        className="flex flex-wrap items-end gap-4"
      >
        <SearchField
          value={search.q ?? ""}
          placeholder={t`Key or name`}
          onChange={(q) => filter({ q: q.trim() || undefined })}
        />
        <Field label={<Trans>Private</Trans>} className="w-48">
          <Select
            value={
              search.private === undefined
                ? privateChoices.any
                : search.private
                  ? privateChoices.only
                  : privateChoices.none
            }
            onValueChange={(value) =>
              filter({
                private:
                  value === privateChoices.only
                    ? true
                    : value === privateChoices.none
                      ? false
                      : undefined,
              })
            }
          >
            <SelectItem value={privateChoices.any}>{t`Any`}</SelectItem>
            <SelectItem value={privateChoices.only}>{t`Private only`}</SelectItem>
            <SelectItem value={privateChoices.none}>{t`Not private`}</SelectItem>
          </Select>
        </Field>
      </div>
      {body}
    </div>
  );
}

function SegmentsTable({
  project,
  segments,
  loading,
}: {
  project: string;
  segments: SegmentListItem[];
  loading: boolean;
}) {
  const { i18n, t } = useLingui();
  const columns = useMemo<ColumnDef<SegmentListItem, unknown>[]>(
    () => [
      {
        id: "segment",
        header: t`Segment`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            <Link
              to="/projects/$project/segments/$segment"
              params={{ project, segment: row.original.key }}
              className="font-medium underline-offset-4 hover:underline"
            >
              {row.original.name}
            </Link>
            <code className="font-mono text-xs text-muted-foreground">{row.original.key}</code>
          </span>
        ),
      },
      {
        id: "private",
        header: t`Private`,
        cell: ({ row }) =>
          row.original.private ? (
            <Badge variant="muted">
              <Trans>Private</Trans>
            </Badge>
          ) : null,
      },
      {
        id: "conditions",
        header: t`Conditions`,
        cell: ({ row }) => formatNumber(i18n, row.original.conditions.length),
      },
      {
        id: "usage",
        header: t`Used by flags`,
        cell: ({ row }) => formatNumber(i18n, row.original.usage_count),
      },
    ],
    [i18n, project, t],
  );
  return (
    <Table
      caption={t`Segments`}
      hideCaption
      columns={columns}
      data={segments}
      loading={loading}
      getRowId={(segment) => segment.id}
    />
  );
}
