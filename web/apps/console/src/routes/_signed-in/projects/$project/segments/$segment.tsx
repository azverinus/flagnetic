import { type AuditEntry, deleteSegment, type Segment, updateSegment } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { CircleAlertIcon } from "lucide-react";
import { useId, useState } from "react";
import { flushSync } from "react-dom";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { ConflictBanner } from "@/components/conflict-banner";
import { QueryError } from "@/components/query-error";
import { SegmentFields } from "@/components/segment-fields";
import { SegmentDiff } from "@/components/structural-diff-view";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import { useCan, useMissingPermissionReason } from "@/lib/access";
import type { AttributeOption, AttributeTypes } from "@/lib/conditions";
import { ApiError, unwrap } from "@/lib/errors";
import { entryOfVersion } from "@/lib/flag-detail";
import { ifMatch } from "@/lib/http";
import { problemMessage } from "@/lib/problem-messages";
import {
  segmentEntriesQuery,
  segmentQuery,
  segmentsQuery,
  segmentUsagesQuery,
} from "@/lib/queries";
import {
  placeSegmentRefusal,
  type SegmentDraft,
  segmentDraft,
  segmentFieldIssues,
  segmentIssues,
  segmentPatchOf,
  segmentViolationMessage,
} from "@/lib/segments";
import { diffSegments } from "@/lib/structural-diff";
import { useConditionAttributes } from "@/lib/use-condition-attributes";

export const Route = createFileRoute("/_signed-in/projects/$project/segments/$segment")({
  staticData: { crumb: { param: "segment" } },
  component: SegmentDetail,
});

function SegmentDetail() {
  const { project, segment: key } = Route.useParams();
  const segment = useQuery(segmentQuery(project, key));
  const declarations = useConditionAttributes(project);

  if (segment.isError && segment.error instanceof ApiError && segment.error.status === 404) {
    return <SegmentNotFound project={project} />;
  }
  const failed = segment.isError
    ? { error: segment.error, retry: () => void segment.refetch() }
    : declarations.state === "failed"
      ? declarations
      : undefined;
  if (failed) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <QueryError error={failed.error} onRetry={failed.retry} />
      </div>
    );
  }
  if (segment.data === undefined || declarations.state !== "ready") {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  // Another segment's page starts afresh: its draft and dialogs are its own.
  return (
    <SegmentPage
      key={`${project}/${key}`}
      project={project}
      segment={segment.data}
      attributes={declarations.options}
      types={declarations.types}
    />
  );
}

function SegmentNotFound({ project }: { project: string }) {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Segment not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>The project has no segment with this key. It may have been deleted.</Trans>
      </p>
      <Button asChild variant="outline">
        <Link to="/projects/$project/segments" params={{ project }}>
          <Trans>Go to the segments</Trans>
        </Link>
      </Button>
    </div>
  );
}

/** The environment a flag's rule names the segment in. */
function UsageEnvironment({ environment }: { environment: string }) {
  return (
    <span className="text-muted-foreground">
      {" "}
      <Trans>in {environment}</Trans>
    </span>
  );
}

/** A save met a newer version: that version, and the entry that made it. */
type Conflict = { current: Segment; entry: AuditEntry | undefined };

/** A refusal of a save, by where the page shows each of its violations. */
type Refusal = { at: Map<string, string>; rest: string[] };

/**
 * One segment as a draft: its fields and conditions, saved together
 * under the version the draft was filled from. A save while flags' rules
 * use the segment names them first; a save that meets a newer version
 * shows who saved it and what they changed.
 */
function SegmentPage({
  project,
  segment,
  attributes,
  types,
}: {
  project: string;
  segment: Segment;
  attributes: AttributeOption[];
  types: AttributeTypes;
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const usagesHeading = useId();
  const canEdit = useCan("segment.edit");
  const missing = useMissingPermissionReason("segment.edit");
  const usages = useQuery(segmentUsagesQuery(project, segment.key));
  const detailKey = segmentQuery(project, segment.key).queryKey;
  const key = segment.key;

  // The segment the draft was filled from, which a save compares it
  // with to send only what the member changed, and the version the save
  // names: apart, since keeping one's edits over a newer version saves
  // onto that version what was changed from the older one.
  const [base, setBase] = useState(segment);
  const [version, setVersion] = useState(segment.version);
  const [draft, setDraft] = useState<SegmentDraft>(() => segmentDraft(segment, types));
  const [checked, setChecked] = useState(false);
  const [refusal, setRefusal] = useState<Refusal>();
  const [conflict, setConflict] = useState<Conflict>();
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleted, setDeleted] = useState(false);

  const patch = segmentPatchOf(base, draft, types);
  const dirty = canEdit && !deleted && Object.keys(patch).length > 0;
  const issues = new Map([
    ...segmentFieldIssues(i18n, draft),
    ...segmentIssues(i18n, base, draft, types),
  ]);
  const errors = new Map<string, string>([...(refusal?.at ?? []), ...(checked ? issues : [])]);

  const restart = (from: Segment) => {
    setBase(from);
    setVersion(from.version);
    setDraft(segmentDraft(from, types));
    setChecked(false);
    setRefusal(undefined);
    setConflict(undefined);
  };

  const save = useMutation({
    mutationFn: ({ sent, at }: { sent: SegmentDraft; at: number }) =>
      unwrap(
        updateSegment({
          path: { project, segment: segment.key },
          body: segmentPatchOf(base, sent, types),
          headers: ifMatch(at),
        }),
      ),
    meta: { toast: false },
    onSuccess: (saved) => {
      queryClient.setQueryData(detailKey, saved);
      void queryClient.invalidateQueries({ queryKey: segmentsQuery(project).queryKey });
      restart(saved);
      toast.success(t`The segment is saved.`);
    },
    onError: async (error, { sent }) => {
      if (!(error instanceof ApiError)) return;
      if (error.code === "version_mismatch") {
        let current: Segment;
        try {
          current = await queryClient.fetchQuery({
            ...segmentQuery(project, segment.key),
            staleTime: 0,
          });
        } catch (reading) {
          setRefusal({
            at: new Map(),
            rest: [
              reading instanceof ApiError
                ? problemMessage(i18n, reading)
                : t`Someone saved the segment meanwhile; reload the page to see their change.`,
            ],
          });
          return;
        }
        const entries = await queryClient
          .fetchQuery({ ...segmentEntriesQuery(project, segment.id, 20), staleTime: 0 })
          .catch(() => [] as AuditEntry[]);
        setConflict({ current, entry: entryOfVersion(entries, current.version) });
        return;
      }
      if (error.violations.length > 0) {
        const placed = placeSegmentRefusal(error.violations, sent);
        setRefusal({
          at: new Map(
            [...placed.at].map(([at, violation]) => [at, segmentViolationMessage(i18n, violation)]),
          ),
          rest: placed.rest.map((violation) => segmentViolationMessage(i18n, violation)),
        });
        return;
      }
      setRefusal({ at: new Map(), rest: [problemMessage(i18n, error)] });
    },
  });

  const send = (at: number) => {
    setConfirming(false);
    setConflict(undefined);
    setRefusal(undefined);
    save.mutate({ sent: draft, at });
  };
  const submit = async () => {
    setChecked(true);
    setRefusal(undefined);
    if (issues.size > 0) return;
    // Rules naming the segment follow every change to it, so their flags
    // are named before it is saved: read afresh, since a rule may have
    // started naming it since the page opened, and asked about whenever
    // they cannot be read, since a used segment must never be saved
    // without asking.
    const fresh = await usages.refetch();
    if (fresh.data === undefined || fresh.data.length > 0) setConfirming(true);
    else send(version);
  };

  const remove = useMutation({
    mutationFn: () =>
      unwrap(
        deleteSegment({
          path: { project, segment: segment.key },
          headers: ifMatch(segment.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: async () => {
      // The draft goes with the segment, so leaving does not ask about it.
      flushSync(() => setDeleted(true));
      toast.success(t`The segment ${key} is deleted.`);
      void queryClient.invalidateQueries({ queryKey: segmentsQuery(project).queryKey });
      await navigate({ to: "/projects/$project/segments", params: { project } });
      queryClient.removeQueries({ queryKey: detailKey });
    },
    onError: (error) => {
      // The dialog explains the refusal; what refused it is read again,
      // so confirming once more is based on it.
      if (error instanceof ApiError && error.code === "version_mismatch") {
        void queryClient.invalidateQueries({ queryKey: detailKey });
      }
      if (error instanceof ApiError && error.code === "in_use") void usages.refetch();
    },
  });

  const usageList = (
    <ul className="flex flex-col gap-1 text-sm">
      {(usages.data ?? []).map((usage) => (
        <li key={`${usage.kind}/${usage.key}/${usage.environment ?? ""}`}>
          {usage.kind === "flag" ? (
            <Link
              to="/projects/$project/flags/$flag"
              params={{ project, flag: usage.key }}
              search={usage.environment ? { environment: usage.environment } : {}}
              className="font-mono underline-offset-4 hover:underline"
            >
              {usage.key}
            </Link>
          ) : (
            <code className="font-mono">{usage.key}</code>
          )}
          {usage.environment ? <UsageEnvironment environment={usage.environment} /> : null}
        </li>
      ))}
    </ul>
  );

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={segment.name}
        description={<code className="font-mono text-sm">{segment.key}</code>}
        actions={
          <Can permission="segment.delete" whenMissing="disable">
            <Button
              variant="destructive"
              onClick={() => {
                remove.reset();
                setDeleting(true);
              }}
            >
              <Trans>Delete</Trans>
            </Button>
          </Can>
        }
      />
      {canEdit ? null : (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          {missing}
        </p>
      )}
      {conflict ? (
        <ConflictBanner
          subject="form"
          entry={conflict.entry}
          diff={<SegmentDiff changes={diffSegments(base, conflict.current)} />}
          actions={
            <>
              <Button type="button" variant="outline" onClick={() => restart(conflict.current)}>
                <Trans>Reload and lose my edits</Trans>
              </Button>
              <Button
                type="button"
                disabled={save.isPending}
                onClick={() => {
                  setVersion(conflict.current.version);
                  send(conflict.current.version);
                }}
              >
                <Trans>Keep my edits</Trans>
              </Button>
            </>
          }
        />
      ) : null}
      <section aria-labelledby={usagesHeading} className="flex flex-col gap-2">
        <h2 id={usagesHeading} className="text-sm font-medium">
          <Trans>Used by</Trans>
        </h2>
        {usages.isError ? (
          <QueryError error={usages.error} onRetry={() => void usages.refetch()} />
        ) : usages.data === undefined ? (
          <Skeleton className="h-5 w-48" />
        ) : usages.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            <Trans>No flag's rules use this segment.</Trans>
          </p>
        ) : (
          usageList
        )}
      </section>
      <div className="flex w-full max-w-3xl flex-col gap-5">
        <SegmentFields
          draft={draft}
          onChange={(next) => {
            // A refusal points at conditions by their places, which adding
            // or removing one moves; it is dropped rather than shown on a
            // condition it was not about.
            if (next.conditions.length !== draft.conditions.length) setRefusal(undefined);
            setDraft(next);
          }}
          attributes={attributes}
          errors={errors}
          disabled={!canEdit}
        />
      </div>
      {checked && issues.size > 0 ? (
        <p role="alert" className="flex items-start gap-2 text-sm">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <Trans>Fix what is marked above before saving.</Trans>
        </p>
      ) : null}
      {refusal && refusal.rest.length > 0 ? (
        <div role="alert" className="flex items-start gap-2 text-sm">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <ul className="flex flex-col gap-1">
            {refusal.rest.map((message, index) => (
              <li key={index}>{message}</li>
            ))}
          </ul>
        </div>
      ) : null}
      <UnsavedChangesBar
        dirty={dirty}
        onDiscard={() => restart(base)}
        onSave={() => void submit()}
        pending={save.isPending}
      />
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t`Save with usages?`}
        confirmLabel={<Trans>Save anyway</Trans>}
        pending={save.isPending}
        onConfirm={() => send(version)}
      >
        {usages.data === undefined ? (
          <p>
            <Trans>
              Which flags use the segment could not be read; the rules of any that do evaluate by
              what it holds once saved.
            </Trans>
          </p>
        ) : (
          <>
            <p>
              <Trans>
                Rules of these flags use the segment and evaluate by what it holds once saved:
              </Trans>
            </p>
            {usageList}
          </>
        )}
      </ConfirmDialog>
      <ConfirmDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={t`Delete ${key}`}
        confirmLabel={<Trans>Delete segment</Trans>}
        destructive
        pending={remove.isPending}
        refusal={remove.error}
        onConfirm={() => remove.mutate()}
      >
        <p>
          <Trans>
            The segment is deleted. This cannot be taken back; its audit entries stay. Refused while
            a rule of any flag, archived ones included, names it.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}
