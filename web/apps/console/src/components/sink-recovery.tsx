import type { Sink, SinkDeadLetter, SinkEventKind, SinkGap, SinkReplay } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { TriangleAlertIcon } from "lucide-react";
import { type FormEvent, type ReactNode, useMemo, useState } from "react";
import { toast } from "sonner";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDateTime, formatNumber } from "@/i18n/format";
import { ApiError } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";
import {
  deadLetterStatusName,
  deadLettersQuery,
  eventKindName,
  failureText,
  gapsQuery,
  latestFailure,
  replayBodyOf,
  sinkApi,
  sinkEventKinds,
  sinkKey,
  sinksKey,
} from "@/lib/sinks";

/** Makes what a recovery action changed read again: the sink, its tables and the list. */
function useRefreshSink(project: string, sink: string) {
  const queryClient = useQueryClient();
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: sinkKey(project, sink) }),
      queryClient.invalidateQueries({ queryKey: sinksKey(project), exact: true }),
    ]);
}

function Alert({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div
      role="alert"
      className="flex items-start gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm"
    >
      <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <p className="font-medium">{title}</p>
        {children}
      </div>
    </div>
  );
}

/**
 * What a sink needs from the maintainer: why it stopped, with the way to
 * resume it, whether retention is about to drop events it has not
 * delivered, and the gaps nobody dismissed.
 */
export function SinkAlerts({
  project,
  sink,
  onResumed,
}: {
  project: string;
  sink: Sink;
  onResumed: (sink: Sink) => void;
}) {
  const { i18n, t } = useLingui();
  const { name } = sink;
  const resume = useMutation({
    mutationFn: () => sinkApi(project).resume(sink.id),
    onSuccess: (resumed) => {
      onResumed(resumed);
      toast.success(t`${name} is resumed.`);
    },
  });
  const failure = latestFailure(sink);
  const stream = failure ? eventKindName(i18n, failure.eventKind) : "";
  const lastFailure = failure ? failureText(i18n, failure.failure) : "";
  return (
    <>
      {sink.health === "stopped" ? (
        <Alert title={t`Stopped by the platform`}>
          <p>
            <Trans>
              Its target kept rejecting batches, so the sink delivers nothing until it is resumed.
              Resuming does not send the dead letters again; redeliver them once the target takes
              batches.
            </Trans>
          </p>
          {failure ? (
            <p className="text-muted-foreground">
              <Trans>
                Last failure on the {stream} stream: {lastFailure}
              </Trans>
            </p>
          ) : null}
          <div>
            <Button size="sm" disabled={resume.isPending} onClick={() => resume.mutate()}>
              <Trans>Resume</Trans>
            </Button>
          </div>
        </Alert>
      ) : null}
      {sink.at_risk ? (
        <Alert title={t`At risk of losing events`}>
          <p>
            <Trans>
              Retention is about to drop events this sink has not delivered. Get it delivering again
              before they are gone, or replay them from a time still within retention.
            </Trans>
          </p>
        </Alert>
      ) : null}
      {sink.gaps > 0 ? (
        <Alert title={t`Events were lost`}>
          <p>
            <Trans>
              Retention dropped events before this sink delivered them. Dismiss each gap once you
              have seen it.
            </Trans>{" "}
            <Link
              to="/projects/$project/sinks/$sink"
              params={{ project, sink: sink.id }}
              search={{ tab: "gaps" }}
              className="font-medium underline underline-offset-4"
            >
              <Trans>See the gaps</Trans>
            </Link>
          </p>
        </Alert>
      ) : null}
    </>
  );
}

/** When the rows of a batch were received: one moment, or the span between two. */
function Period({ from, to }: { from: string; to: string }) {
  return from === to ? (
    <Moment value={from} />
  ) : (
    <span className="flex flex-col">
      <Moment value={from} />
      <span className="text-muted-foreground">
        <Moment value={to} />
      </span>
    </span>
  );
}

/**
 * The batches the target rejected and the sink set aside: each can be
 * sent again, repeatedly, while retention still holds its rows, or
 * discarded.
 */
export function DeadLettersTab({ project, sink }: { project: string; sink: Sink }) {
  const { i18n, t } = useLingui();
  const letters = useQuery(deadLettersQuery(project, sink.id));
  const refresh = useRefreshSink(project, sink.id);
  const [discarding, setDiscarding] = useState<SinkDeadLetter>();
  const redeliver = useMutation({
    mutationFn: (letter: SinkDeadLetter) => sinkApi(project).redeliver(sink.id, letter.id),
    meta: { toast: false },
    onSuccess: async () => {
      await refresh();
    },
  });
  const rejectedAgain =
    redeliver.data !== undefined && redeliver.data.status === "pending"
      ? redeliver.data
      : undefined;
  const discard = useMutation({
    mutationFn: (letter: SinkDeadLetter) => sinkApi(project).discard(sink.id, letter.id),
    meta: { toast: false },
    onSuccess: async () => {
      await refresh();
      setDiscarding(undefined);
    },
  });
  const columns = useMemo<TableColumn<SinkDeadLetter>[]>(
    () => [
      {
        id: "kind",
        header: t`Event kind`,
        cell: ({ row }) => eventKindName(i18n, row.original.event_kind),
      },
      {
        id: "received",
        header: t`Received`,
        cell: ({ row }) => (
          <Period from={row.original.first_received_at} to={row.original.last_received_at} />
        ),
      },
      {
        id: "rows",
        header: t`Rows`,
        cell: ({ row }) => formatNumber(i18n, row.original.rows),
      },
      { id: "attempts", header: t`Attempts`, cell: ({ row }) => row.original.attempts },
      {
        id: "failure",
        header: t`Last failure`,
        cell: ({ row }) => failureText(i18n, row.original.last_failure),
      },
      {
        id: "status",
        header: t`Status`,
        cell: ({ row }) => deadLetterStatusName(i18n, row.original.status),
      },
      {
        id: "actions",
        header: () => <span className="sr-only">{t`Actions`}</span>,
        cell: ({ row }) => {
          const letter = row.original;
          const eventKind = eventKindName(i18n, letter.event_kind);
          return (
            <span className="flex justify-end gap-2">
              {letter.status === "expired" ? null : (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={redeliver.isPending}
                  aria-label={t`Redeliver the ${eventKind} dead letter`}
                  onClick={() => redeliver.mutate(letter)}
                >
                  <Trans>Redeliver</Trans>
                </Button>
              )}
              <Button
                variant="outline"
                size="sm"
                aria-label={t`Discard the ${eventKind} dead letter`}
                onClick={() => setDiscarding(letter)}
              >
                <Trans>Discard</Trans>
              </Button>
            </span>
          );
        },
      },
    ],
    [i18n, t, redeliver],
  );
  if (letters.isError) {
    return <QueryError error={letters.error} onRetry={() => void letters.refetch()} />;
  }
  return (
    <section className="flex flex-col gap-3">
      <p className="text-sm text-muted-foreground">
        <Trans>
          A batch the target rejected over and over is set aside here so the stream can go on.
          Redelivering sends its rows again, outside the stream; discarding forgets them.
        </Trans>
      </p>
      {redeliver.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {redeliver.error instanceof ApiError
            ? problemMessage(i18n, redeliver.error)
            : t`The dead letter could not be redelivered.`}
        </p>
      ) : null}
      {rejectedAgain ? (
        <p role="status" className="text-sm text-destructive">
          <Trans>The target rejected the dead letter again.</Trans>
        </p>
      ) : null}
      <Table
        caption={t`Dead letters`}
        hideCaption
        columns={columns}
        data={letters.data ?? []}
        loading={letters.isPending}
        getRowId={(letter) => letter.id}
        empty={<Trans>No dead letters.</Trans>}
      />
      <ConfirmDialog
        open={discarding !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            discard.reset();
            setDiscarding(undefined);
          }
        }}
        title={t`Discard this dead letter`}
        confirmLabel={<Trans>Discard dead letter</Trans>}
        destructive
        pending={discard.isPending}
        refusal={discard.error}
        onConfirm={() => discarding && discard.mutate(discarding)}
      >
        <p>
          <Trans>
            Its rows are not sent to the target. They can still reach it by a replay while retention
            holds them. This cannot be undone.
          </Trans>
        </p>
      </ConfirmDialog>
    </section>
  );
}

/**
 * The events retention dropped before the sink delivered them, which
 * stay listed after a member has seen them and dismissed them.
 */
export function GapsTab({ project, sink }: { project: string; sink: Sink }) {
  const { i18n, t } = useLingui();
  const gaps = useQuery(gapsQuery(project, sink.id));
  const refresh = useRefreshSink(project, sink.id);
  const dismiss = useMutation({
    mutationFn: (gap: SinkGap) => sinkApi(project).dismissGap(sink.id, gap.id),
    onSuccess: async () => {
      await refresh();
    },
  });
  const columns = useMemo<TableColumn<SinkGap>[]>(
    () => [
      {
        id: "kind",
        header: t`Event kind`,
        cell: ({ row }) => eventKindName(i18n, row.original.event_kind),
      },
      {
        id: "period",
        header: t`Received`,
        cell: ({ row }) => <Period from={row.original.period_start} to={row.original.period_end} />,
      },
      {
        id: "rows",
        header: t`Rows lost`,
        cell: ({ row }) => formatNumber(i18n, row.original.rows),
      },
      {
        id: "dropped",
        header: t`Dropped`,
        cell: ({ row }) => <Moment value={row.original.dropped_at} />,
      },
      {
        id: "dismissed",
        header: t`Dismissed`,
        cell: ({ row }) => {
          const gap = row.original;
          if (gap.dismissed_at !== null) return <Moment value={gap.dismissed_at} />;
          const eventKind = eventKindName(i18n, gap.event_kind);
          return (
            <Button
              variant="outline"
              size="sm"
              disabled={dismiss.isPending}
              aria-label={t`Dismiss the ${eventKind} gap`}
              onClick={() => dismiss.mutate(gap)}
            >
              <Trans>Dismiss</Trans>
            </Button>
          );
        },
      },
    ],
    [i18n, t, dismiss],
  );
  if (gaps.isError) return <QueryError error={gaps.error} onRetry={() => void gaps.refetch()} />;
  return (
    <section className="flex flex-col gap-3">
      <p className="text-sm text-muted-foreground">
        <Trans>
          Retention drops raw events once they are older than the retention window, whether or not
          every sink has delivered them. A gap says which were lost; dismissing it only stops it
          counting towards the warnings.
        </Trans>
      </p>
      <Table
        caption={t`Gaps`}
        hideCaption
        columns={columns}
        data={gaps.data ?? []}
        loading={gaps.isPending}
        getRowId={(gap) => gap.id}
        empty={<Trans>No events were lost.</Trans>}
      />
    </section>
  );
}

/**
 * Sending events again from a time still within retention: all the sink
 * exports or the kinds ticked, up to a time or on into the live stream.
 * It asks first, because the target receives every row again.
 */
export function ReplayTab({
  project,
  sink,
  onReplayed,
}: {
  project: string;
  sink: Sink;
  onReplayed: (sink: Sink) => void;
}) {
  const { i18n, t } = useLingui();
  const exported = sink.event_kinds.length === 0 ? sinkEventKinds : sink.event_kinds;
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [chosen, setChosen] = useState<SinkEventKind[]>([]);
  const [problems, setProblems] = useState<{ path: string; message: string }[]>([]);
  const [body, setBody] = useState<SinkReplay>();
  const replay = useMutation({
    mutationFn: (replayed: SinkReplay) => sinkApi(project).replay(sink.id, replayed),
    meta: { toast: false },
    onSuccess: (replayed) => {
      setBody(undefined);
      onReplayed(replayed);
      const { name } = sink;
      toast.success(t`The replay is queued. ${name} sends the events again from its next batch.`);
    },
  });
  const problemAt = (path: string) => problems.find((problem) => problem.path === path)?.message;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const result = replayBodyOf(i18n, { from, to, event_kinds: chosen });
    if ("problems" in result) {
      setProblems(result.problems);
      return;
    }
    setProblems([]);
    setBody(result.body);
  };
  const { name } = sink;
  return (
    <section className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        <Trans>
          Sends the events received since a time again, in order, then the stream goes on. Use it to
          fill a target after it lost data or after a dead letter was discarded.
        </Trans>
      </p>
      <form noValidate onSubmit={submit} className="flex max-w-xl flex-col gap-4">
        <Field
          label={<Trans>From</Trans>}
          hint={<Trans>In your time zone, within the retention window.</Trans>}
          error={problemAt("from")}
        >
          <Input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field
          label={<Trans>To</Trans>}
          hint={<Trans>Leave empty to replay on into the live stream.</Trans>}
          error={problemAt("to")}
        >
          <Input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
        <fieldset className="flex flex-col gap-2">
          <legend className="text-sm font-medium">
            <Trans>Event kinds</Trans>
          </legend>
          <p className="text-sm text-muted-foreground">
            <Trans>None ticked means every kind the sink exports.</Trans>
          </p>
          {exported.map((kind) => (
            <label key={kind} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={chosen.includes(kind)}
                onChange={(event) =>
                  setChosen(
                    event.target.checked
                      ? [...chosen, kind]
                      : chosen.filter((item) => item !== kind),
                  )
                }
                className="size-4 accent-primary"
              />
              {eventKindName(i18n, kind)}
            </label>
          ))}
        </fieldset>
        <div>
          <Button type="submit">
            <Trans>Replay…</Trans>
          </Button>
        </div>
      </form>
      <Dialog
        open={body !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            replay.reset();
            setBody(undefined);
          }
        }}
        title={t`Replay ${name}`}
      >
        {body ? (
          <DialogForm
            onSubmit={(event) => {
              event.preventDefault();
              replay.mutate(body);
            }}
            onCancel={() => setBody(undefined)}
            submitLabel={<Trans>Replay</Trans>}
            pending={replay.isPending}
            refusal={replay.error}
          >
            <ReplayWarning body={body} exported={exported} />
          </DialogForm>
        ) : null}
      </Dialog>
    </section>
  );
}

/** What a replay will do, said before it is confirmed. */
function ReplayWarning({
  body,
  exported,
}: {
  body: SinkReplay;
  exported: readonly SinkEventKind[];
}) {
  const { i18n } = useLingui();
  const kinds = (body.event_kinds ?? exported)
    .map((kind) => eventKindName(i18n, kind as SinkEventKind))
    .join(", ");
  const since = formatDateTime(i18n, body.from);
  const until = body.to === undefined ? undefined : formatDateTime(i18n, body.to);
  return (
    <div className="flex flex-col gap-2 text-sm">
      <p>
        {until === undefined ? (
          <Trans>
            The target receives again every row of {kinds} received at or after {since}, and the
            stream then goes on.
          </Trans>
        ) : (
          <Trans>
            The target receives again every row of {kinds} received at or after {since} and at or
            before {until}.
          </Trans>
        )}
      </p>
      <p className="text-muted-foreground">
        <Trans>
          The delivery in progress is dropped. A target that does not deduplicate sees these rows
          twice.
        </Trans>
      </p>
    </div>
  );
}
