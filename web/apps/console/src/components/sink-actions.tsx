import type { Sink, SinkEventKind, SinkSampleKindResult } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useId, useState } from "react";

import { SinkSampleOutcome } from "@/components/sink-test";
import { Button } from "@/components/ui/button";
import { Dialog, DialogActions } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";
import {
  eventKindName,
  sinkApi,
  sinkEventKinds,
  sinkKey,
  sinksKey,
  startFromOf,
} from "@/lib/sinks";

/**
 * Enabling a sink: from now, or from a time still within retention, which
 * backfills. The server checks the target first, and its refusal, such as
 * `sink_not_ready` with the failure, is answered here without closing the
 * dialog.
 */
export function EnableSinkDialog({
  project,
  sink,
  open,
  onOpenChange,
  onEnabled,
}: {
  project: string;
  sink: Sink;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onEnabled: (sink: Sink) => void;
}) {
  const { t } = useLingui();
  const { name } = sink;
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t`Enable ${name}`}
      description={
        <Trans>The target is checked first; a sink that cannot deliver is not enabled.</Trans>
      }
    >
      {open ? (
        <EnableForm
          project={project}
          sink={sink}
          onCancel={() => onOpenChange(false)}
          onEnabled={onEnabled}
        />
      ) : null}
    </Dialog>
  );
}

function EnableForm({
  project,
  sink,
  onCancel,
  onEnabled,
}: {
  project: string;
  sink: Sink;
  onCancel: () => void;
  onEnabled: (sink: Sink) => void;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const group = useId();
  const [mode, setMode] = useState<"now" | "from">("now");
  const [from, setFrom] = useState("");
  const [missing, setMissing] = useState(false);
  const enable = useMutation({
    mutationFn: (startFrom?: string) => sinkApi(project).enable(sink.id, startFrom),
    meta: { toast: false },
    onSuccess: async (enabled) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: sinksKey(project), exact: true }),
        queryClient.invalidateQueries({ queryKey: sinkKey(project, sink.id) }),
      ]);
      onEnabled(enabled);
    },
  });
  const startFrom = mode === "from" ? startFromOf(from) : undefined;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (mode === "from" && startFrom === undefined) {
      setMissing(true);
      return;
    }
    enable.mutate(startFrom);
  };
  return (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel}
      submitLabel={<Trans>Enable sink</Trans>}
      pending={enable.isPending}
      refusal={enable.error}
    >
      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium">
          <Trans>Start delivering</Trans>
        </legend>
        <label className="flex items-start gap-2 text-sm">
          <input
            type="radio"
            name={group}
            checked={mode === "now"}
            onChange={() => setMode("now")}
            className="mt-0.5 size-4 accent-primary"
          />
          <span>
            <Trans>From now: only events received from now on.</Trans>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm">
          <input
            type="radio"
            name={group}
            checked={mode === "from"}
            onChange={() => setMode("from")}
            className="mt-0.5 size-4 accent-primary"
          />
          <span>
            <Trans>From a time: also the events received since then, still within retention.</Trans>
          </span>
        </label>
      </fieldset>
      {mode === "from" ? (
        <Field
          label={<Trans>Start from</Trans>}
          hint={<Trans>In your time zone. A stream the sink already has keeps its place.</Trans>}
          error={missing && startFrom === undefined ? t`Choose a time.` : undefined}
          refusal={enable.error}
          pointer="/start_from"
        >
          <Input
            type="datetime-local"
            value={from}
            onChange={(event) => setFrom(event.target.value)}
          />
        </Field>
      ) : null}
    </DialogForm>
  );
}

/**
 * Sending ten sample rows per event kind, which works on a disabled sink and
 * moves no cursor. Each kind is answered on its own.
 */
export function SampleSinkDialog({
  project,
  sink,
  open,
  onOpenChange,
}: {
  project: string;
  sink: Sink;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useLingui();
  const { name } = sink;
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t`Send a sample to ${name}`}
      description={
        <Trans>
          Ten rows of each kind you tick, marked as samples. Nothing is stored and no stream moves.
        </Trans>
      }
    >
      {open ? (
        <SampleForm project={project} sink={sink} onClose={() => onOpenChange(false)} />
      ) : null}
    </Dialog>
  );
}

function SampleForm({
  project,
  sink,
  onClose,
}: {
  project: string;
  sink: Sink;
  onClose: () => void;
}) {
  const { i18n } = useLingui();
  const exported = sink.event_kinds.length === 0 ? sinkEventKinds : sink.event_kinds;
  const [chosen, setChosen] = useState<SinkEventKind[]>([...exported]);
  const [results, setResults] = useState<SinkSampleKindResult[]>();
  const send = useMutation({
    mutationFn: () => sinkApi(project).sample(sink.id, chosen),
    meta: { toast: false },
    onSuccess: (answer) => setResults(answer.results),
  });
  const error = send.error instanceof ApiError ? send.error : undefined;
  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        send.mutate();
      }}
      className="flex flex-col gap-4"
    >
      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium">
          <Trans>Event kinds</Trans>
        </legend>
        {exported.map((kind) => (
          <label key={kind} className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={chosen.includes(kind)}
              onChange={(event) =>
                setChosen(
                  event.target.checked ? [...chosen, kind] : chosen.filter((item) => item !== kind),
                )
              }
              className="size-4 accent-primary"
            />
            {eventKindName(i18n, kind)}
          </label>
        ))}
      </fieldset>
      {results ? <SinkSampleOutcome results={results} /> : null}
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {problemMessage(i18n, error)}
        </p>
      ) : null}
      <DialogActions>
        <Button type="button" variant="outline" onClick={onClose}>
          <Trans>Close</Trans>
        </Button>
        <Button type="submit" disabled={send.isPending || chosen.length === 0}>
          <Trans>Send sample</Trans>
        </Button>
      </DialogActions>
    </form>
  );
}
