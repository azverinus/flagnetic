import {
  archiveExperiment,
  cloneExperiment,
  deleteExperiment,
  type Experiment,
  type ExperimentStop,
  type Flag,
  setExperimentDecisionNote,
  startExperiment,
  stopExperiment,
  updateExperiment,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { CircleAlertIcon, TriangleAlertIcon } from "lucide-react";
import { type FormEvent, type ReactNode, useId, useState } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogActions } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { formatDateTime, formatDay } from "@/i18n/format";
import { ApiError, unwrap } from "@/lib/errors";
import {
  cloneIssues,
  type ExperimentAction,
  experimentActions,
  experimentRefusalMessage,
  growIssue,
  type HoldingRule,
  holdingRules,
  maxNoteLength,
  noteIssue,
  startBlockers,
  type StopDraft,
  stopBodyOf,
  stopIssues,
} from "@/lib/experiment-transitions";
import { holdingEnvironments, parsePercent, percentText } from "@/lib/experiments";
import { ifMatch } from "@/lib/http";
import {
  auditQueryKey,
  environmentsQuery,
  experimentQuery,
  experimentsQueryKey,
  flagQuery,
  flagsQueryKey,
} from "@/lib/queries";

/** A write of one experiment under the version the page shows. */
type ExperimentWrite =
  | { action: "start" }
  | { action: "grow"; allocation: number }
  | { action: "stop"; body: ExperimentStop }
  | { action: "note"; note: string }
  | { action: "archive" }
  | { action: "delete" };

/**
 * Sends one write of an experiment under the version the page shows and
 * hands the experiment it answers to the page. Starting, growing and
 * stopping change the rulesets of the environments holding it, and a stop
 * rewrites their configurations, so the flag is read again. A refusal for
 * a newer version reads that version, so that confirming again is based
 * on it.
 */
function useExperimentWrite(
  project: string,
  experiment: Experiment,
  onChanged: (saved: Experiment) => void,
) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { i18n, t } = useLingui();
  const key = experiment.key;
  const detailKey = experimentQuery(project, key).queryKey;

  return useMutation({
    mutationFn: async (write: ExperimentWrite) => {
      const options = {
        path: { project, experiment: key },
        headers: ifMatch(experiment.version),
      };
      switch (write.action) {
        case "start":
          return unwrap(startExperiment(options));
        case "grow":
          return unwrap(
            updateExperiment({ ...options, body: { traffic_allocation: write.allocation } }),
          );
        case "stop":
          return unwrap(stopExperiment({ ...options, body: write.body }));
        case "note":
          return unwrap(setExperimentDecisionNote({ ...options, body: { note: write.note } }));
        case "archive":
          return unwrap(archiveExperiment(options));
        case "delete":
          await unwrap(deleteExperiment(options));
          return undefined;
      }
    },
    meta: { toast: false },
    onSuccess: async (saved, write) => {
      void queryClient.invalidateQueries({ queryKey: experimentsQueryKey(project) });
      void queryClient.invalidateQueries({ queryKey: flagsQueryKey(project) });
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      if (write.action === "delete" || saved === undefined) {
        toast.success(t`The experiment ${key} is deleted.`);
        await navigate({ to: "/projects/$project/experiments", params: { project } });
        queryClient.removeQueries({ queryKey: detailKey });
        return;
      }
      queryClient.setQueryData(detailKey, saved);
      onChanged(saved);
      toast.success(doneMessage(i18n, key, write.action));
    },
    onError: async (error) => {
      if (!(error instanceof ApiError) || error.code !== "version_mismatch") return;
      const current = await queryClient
        .fetchQuery({ ...experimentQuery(project, key), staleTime: 0 })
        .catch(() => undefined);
      if (current) onChanged(current);
    },
  });
}

function doneMessage(
  i18n: I18n,
  key: string,
  action: Exclude<ExperimentWrite["action"], "delete">,
): string {
  switch (action) {
    case "start":
      return i18n._(msg`The experiment ${key} is running.`);
    case "grow":
      return i18n._(msg`The traffic allocation of ${key} grew.`);
    case "stop":
      return i18n._(msg`The experiment ${key} is stopped.`);
    case "note":
      return i18n._(msg`The decision note is saved.`);
    case "archive":
      return i18n._(msg`The experiment ${key} is archived.`);
  }
}

/** A refusal of an experiment's write, explained in the dialog that sent it. */
function ExperimentRefusal({ refusal }: { refusal: unknown }) {
  const { i18n } = useLingui();
  if (!(refusal instanceof ApiError)) return null;
  return (
    <p
      role="alert"
      className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
    >
      <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
      {experimentRefusalMessage(i18n, refusal)}
    </p>
  );
}

/** Names environments by their keys, with the names the project gives them once read. */
function useEnvironmentNames(project: string): (key: string) => string {
  const environments = useQuery(environmentsQuery(project));
  return (key) => environments.data?.find((environment) => environment.key === key)?.name ?? key;
}

export type ExperimentTransitionsProps = {
  project: string;
  /** The experiment as the page shows it; every write names its version. */
  experiment: Experiment;
  /** The unit types the project declares, which a start needs. */
  unitTypes: readonly { key: string }[];
  /**
   * Whether the form holds unsaved edits: transitions wait until they are
   * saved or discarded, since a transition answers a new version the
   * edits were not made on.
   */
  editing: boolean;
  /** Takes the experiment a transition answered, or the newer one it met. */
  onChanged: (saved: Experiment) => void;
};

/**
 * The header's transitions of an experiment, those its status allows,
 * the primary one last, each confirming what it changes in which environments: start, grow the
 * allocation, stop with a decision, archive, clone, delete a draft.
 */
export function ExperimentTransitions({
  project,
  experiment,
  unitTypes,
  editing,
  onChanged,
}: ExperimentTransitionsProps) {
  const { t } = useLingui();
  const [asking, setAsking] = useState<ExperimentAction>();
  const write = useExperimentWrite(project, experiment, onChanged);
  const flag = useQuery(flagQuery(project, experiment.flag));
  const environmentName = useEnvironmentNames(project);
  const actions = experimentActions(experiment.status);
  const key = experiment.key;

  const ask = (action: ExperimentAction) => {
    write.reset();
    setAsking(action);
  };
  const dialog = (action: ExperimentAction) => ({
    open: asking === action,
    onOpenChange: (open: boolean) => !open && setAsking(undefined),
  });
  const close = () => setAsking(undefined);
  const send = (next: ExperimentWrite) => write.mutate(next, { onSuccess: close });
  const holders = flag.data ? holdingEnvironments(flag.data, key).map(environmentName) : undefined;

  const action = (
    name: ExperimentAction,
    label: ReactNode,
    variant: "default" | "outline" | "destructive" = "outline",
  ) =>
    actions.has(name) ? (
      <Can
        key={name}
        permission={name === "delete" ? "experiment.delete" : "experiment.edit"}
        whenMissing="disable"
      >
        <Button variant={variant} disabled={editing && name !== "clone"} onClick={() => ask(name)}>
          {label}
        </Button>
      </Can>
    ) : null;

  return (
    <>
      {action("grow", <Trans>Grow allocation</Trans>)}
      {action("clone", <Trans>Clone</Trans>)}
      {action("archive", <Trans>Archive</Trans>)}
      {action("delete", <Trans>Delete</Trans>, "destructive")}
      {action("stop", <Trans>Stop</Trans>, "default")}
      {action("start", <Trans>Start</Trans>, "default")}

      <Dialog {...dialog("start")} title={t`Start ${key}`}>
        {asking === "start" ? (
          <StartConfirmation
            experiment={experiment}
            flag={flag.data}
            unitTypes={unitTypes}
            holders={holders}
            pending={write.isPending}
            refusal={write.error}
            onCancel={close}
            onStart={() => send({ action: "start" })}
          />
        ) : null}
      </Dialog>
      <Dialog {...dialog("grow")} title={t`Grow the allocation of ${key}`}>
        {asking === "grow" ? (
          <GrowForm
            experiment={experiment}
            holders={holders}
            pending={write.isPending}
            refusal={write.error}
            onCancel={close}
            onGrow={(allocation) => send({ action: "grow", allocation })}
          />
        ) : null}
      </Dialog>
      <StopDialog
        {...dialog("stop")}
        experiment={experiment}
        flag={flag.data}
        environmentName={environmentName}
        pending={write.isPending}
        refusal={write.error}
        onStop={(body) => send({ action: "stop", body })}
      />
      <ConfirmDialog
        {...dialog("archive")}
        title={t`Archive ${key}`}
        confirmLabel={<Trans>Archive experiment</Trans>}
        pending={write.isPending}
        refusal={write.error}
        onConfirm={() => send({ action: "archive" })}
      >
        <p>
          {experiment.status === "draft" ? (
            <Trans>
              The draft never starts and nothing about it changes any more; no ruleset changes.
              Archived experiments are listed only on request; it can still be cloned.
            </Trans>
          ) : (
            <Trans>
              Nothing about the experiment changes any more, its decision note included; no ruleset
              changes. Archived experiments are listed only on request; it can still be cloned.
            </Trans>
          )}
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        {...dialog("delete")}
        title={t`Delete ${key}`}
        confirmLabel={<Trans>Delete experiment</Trans>}
        destructive
        pending={write.isPending}
        refusal={write.error}
        onConfirm={() => send({ action: "delete" })}
      >
        <p>
          <Trans>
            The draft is deleted. This cannot be taken back; its audit entries stay. Refused while a
            rule of its flag serves it.
          </Trans>
        </p>
      </ConfirmDialog>
      <CloneDialog {...dialog("clone")} project={project} experiment={experiment} />
    </>
  );
}

/** The environments a write changes the rulesets of, or a warning that it changes none. */
function HolderList({
  holders,
  some,
  none,
}: {
  holders: readonly string[] | undefined;
  some: ReactNode;
  none: ReactNode;
}) {
  const { t } = useLingui();
  if (holders === undefined) {
    return (
      <p>
        <Trans>Reading which environments hold it…</Trans>
      </p>
    );
  }
  if (holders.length === 0) {
    return (
      <p className="flex items-start gap-2">
        <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
        {none}
      </p>
    );
  }
  return (
    <>
      <p>{some}</p>
      <ul aria-label={t`Environments changed`} className="list-disc pl-5 text-foreground">
        {holders.map((name) => (
          <li key={name}>{name}</li>
        ))}
      </ul>
    </>
  );
}

function StartConfirmation({
  experiment,
  flag,
  unitTypes,
  holders,
  pending,
  refusal,
  onCancel,
  onStart,
}: {
  experiment: Experiment;
  flag: Flag | undefined;
  unitTypes: readonly { key: string }[];
  holders: readonly string[] | undefined;
  pending: boolean;
  refusal: unknown;
  onCancel: () => void;
  onStart: () => void;
}) {
  const { i18n, t } = useLingui();
  const blockers = flag ? startBlockers(i18n, experiment, { flag, unitTypes }) : [];
  return (
    <>
      <div className="flex flex-col gap-2 text-sm text-muted-foreground">
        {blockers.length > 0 ? (
          <>
            <p className="text-foreground">
              <Trans>It cannot start until these are fixed:</Trans>
            </p>
            <ul aria-label={t`Missing to start`} className="list-disc pl-5 text-foreground">
              {blockers.map((blocker) => (
                <li key={blocker}>{blocker}</li>
              ))}
            </ul>
          </>
        ) : null}
        <HolderList
          holders={holders}
          some={
            <Trans>
              Starting puts it into the ruleset of each of these environments at once, and its flag,
              unit type and weights stay as they are while it runs:
            </Trans>
          }
          none={
            <Trans>
              No rule serves it yet: it starts, but no SDK receives it until a rule of its flag
              serves it in an environment.
            </Trans>
          }
        />
      </div>
      <ExperimentRefusal refusal={refusal} />
      <DialogActions>
        <Button variant="outline" onClick={onCancel}>
          <Trans>Cancel</Trans>
        </Button>
        <Button disabled={pending || flag === undefined || blockers.length > 0} onClick={onStart}>
          <Trans>Start experiment</Trans>
        </Button>
      </DialogActions>
    </>
  );
}

function GrowForm({
  experiment,
  holders,
  pending,
  refusal,
  onCancel,
  onGrow,
}: {
  experiment: Experiment;
  holders: readonly string[] | undefined;
  pending: boolean;
  refusal: unknown;
  onCancel: () => void;
  onGrow: (allocation: number) => void;
}) {
  const { i18n } = useLingui();
  const [text, setText] = useState("");
  const [checked, setChecked] = useState(false);
  const issue = growIssue(i18n, text, experiment.traffic_allocation);
  const current = percentText(i18n, experiment.traffic_allocation);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    const points = parsePercent(text);
    if (issue === undefined && points !== undefined) onGrow(points);
  };

  return (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel}
      submitLabel={<Trans>Grow allocation</Trans>}
      pending={pending}
    >
      <Field
        label={<Trans>New traffic allocation (%)</Trans>}
        hint={
          <Trans>Now {current} %. It only grows: every unit included so far stays included.</Trans>
        }
        error={checked ? issue : undefined}
        className="w-64"
      >
        <Input inputMode="decimal" value={text} onChange={(event) => setText(event.target.value)} />
      </Field>
      <div className="flex flex-col gap-2 text-sm text-muted-foreground">
        <HolderList
          holders={holders}
          some={<Trans>The ruleset of each of these environments changes at once:</Trans>}
          none={<Trans>No rule serves it: no ruleset changes.</Trans>}
        />
      </div>
      <ExperimentRefusal refusal={refusal} />
    </DialogForm>
  );
}

export type StopDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  experiment: Experiment;
  /** The experiment's flag with its configurations, once read: its rules are what a stop rewrites. */
  flag: Flag | undefined;
  environmentName: (key: string) => string;
  pending: boolean;
  refusal: unknown;
  onStop: (body: ExperimentStop) => void;
};

/**
 * Stopping a running experiment with a decision: a winner, whose
 * variation each rule serving the experiment serves from then on, its id
 * and conditions kept, or the removal of those rules. Each rule it
 * rewrites is listed with what becomes of it.
 */
export function StopDialog({ open, onOpenChange, experiment, ...rest }: StopDialogProps) {
  const { t } = useLingui();
  const key = experiment.key;
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={t`Stop ${key}`}>
      {open ? (
        <StopForm experiment={experiment} onCancel={() => onOpenChange(false)} {...rest} />
      ) : null}
    </Dialog>
  );
}

function StopForm({
  experiment,
  flag,
  environmentName,
  pending,
  refusal,
  onStop,
  onCancel,
}: Omit<StopDialogProps, "open" | "onOpenChange"> & { onCancel: () => void }) {
  const { i18n, t } = useLingui();
  const name = useId();
  const [draft, setDraft] = useState<StopDraft>({ outcome: "winner", winner: "", note: "" });
  const [checked, setChecked] = useState(false);
  const issues = stopIssues(i18n, draft);
  const variationName = (variation: string) => {
    const known = flag?.variations.find(({ key }) => key === variation)?.name;
    return known && known !== variation ? `${known} (${variation})` : variation;
  };
  const rules = flag ? holdingRules(flag, experiment.key) : undefined;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    if (Object.keys(issues).length === 0) onStop(stopBodyOf(draft));
  };

  return (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel}
      submitLabel={<Trans>Stop experiment</Trans>}
      pending={pending}
    >
      <fieldset className="flex flex-col gap-2 text-sm">
        <legend className="mb-2 text-sm font-medium">
          <Trans>Decision</Trans>
        </legend>
        {(
          [
            ["winner", t`Ship a winner: its rules serve the winning variation`],
            ["removed", t`Remove: its rules are deleted, the flag's other rules decide`],
          ] as const
        ).map(([outcome, label]) => (
          <label key={outcome} className="flex items-center gap-2">
            <input
              type="radio"
              name={name}
              value={outcome}
              checked={draft.outcome === outcome}
              onChange={() => setDraft({ ...draft, outcome })}
              className="size-4 accent-primary"
            />
            {label}
          </label>
        ))}
      </fieldset>
      {draft.outcome === "winner" ? (
        <Field
          label={<Trans>Winning variation</Trans>}
          error={checked ? issues.winner : undefined}
          className="w-72"
        >
          <Select
            value={draft.winner}
            onValueChange={(winner) => setDraft({ ...draft, winner })}
            placeholder={<Trans>Choose a variation</Trans>}
          >
            {experiment.variations.map(({ variation }) => (
              <SelectItem key={variation} value={variation}>
                {variationName(variation)}
              </SelectItem>
            ))}
          </Select>
        </Field>
      ) : null}
      <Field
        label={<Trans>Note</Trans>}
        hint={
          <Trans>
            Why, for whoever reads the decision later. Optional; editable while stopped.
          </Trans>
        }
        error={checked ? issues.note : undefined}
      >
        <Textarea
          rows={3}
          maxLength={maxNoteLength}
          value={draft.note}
          onChange={(event) => setDraft({ ...draft, note: event.target.value })}
        />
      </Field>
      <div className="flex flex-col gap-2 text-sm text-muted-foreground">
        {rules === undefined ? (
          <p>
            <Trans>Reading which rules serve it…</Trans>
          </p>
        ) : rules.length === 0 ? (
          <p>
            <Trans>No rule serves it: no configuration or ruleset changes.</Trans>
          </p>
        ) : (
          <>
            <p>
              <Trans>
                Each of these rules is rewritten, and its environment's ruleset changes:
              </Trans>
            </p>
            <ul aria-label={t`Rules rewritten`} className="list-disc pl-5 text-foreground">
              {rules.map((rule) => (
                <li key={`${rule.environment}/${rule.id}`}>
                  <RuleRewrite
                    rule={rule}
                    environment={environmentName(rule.environment)}
                    winner={
                      draft.outcome === "winner" && draft.winner !== ""
                        ? variationName(draft.winner)
                        : undefined
                    }
                    removed={draft.outcome === "removed"}
                  />
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
      <ExperimentRefusal refusal={refusal} />
    </DialogForm>
  );
}

function RuleRewrite({
  rule,
  environment,
  winner,
  removed,
}: {
  rule: HoldingRule;
  environment: string;
  winner: string | undefined;
  removed: boolean;
}) {
  const position = rule.position;
  const description = rule.description;
  const which = description ? (
    <Trans>
      {environment}, rule {position} “{description}”
    </Trans>
  ) : (
    <Trans>
      {environment}, rule {position}
    </Trans>
  );
  return (
    <>
      {which}:{" "}
      {removed ? (
        <Trans>deleted</Trans>
      ) : winner ? (
        <Trans>serves {winner}, its conditions kept</Trans>
      ) : (
        <Trans>serves the winner, its conditions kept</Trans>
      )}
    </>
  );
}

function CloneDialog({
  open,
  onOpenChange,
  project,
  experiment,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  project: string;
  experiment: Experiment;
}) {
  const { t } = useLingui();
  const key = experiment.key;
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={t`Clone ${key}`}>
      {open ? (
        <CloneForm project={project} experiment={experiment} onDone={() => onOpenChange(false)} />
      ) : null}
    </Dialog>
  );
}

function CloneForm({
  project,
  experiment,
  onDone,
}: {
  project: string;
  experiment: Experiment;
  onDone: () => void;
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [draft, setDraft] = useState({ key: "", name: "" });
  const [checked, setChecked] = useState(false);
  const issues = cloneIssues(i18n, draft, experiment.key);
  const sourceName = experiment.name;

  const clone = useMutation({
    mutationFn: () =>
      unwrap(
        cloneExperiment({
          path: { project, experiment: experiment.key },
          body: {
            key: draft.key.trim(),
            ...(draft.name.trim() === "" ? {} : { name: draft.name.trim() }),
          },
        }),
      ),
    meta: { toast: false },
    onSuccess: async (created) => {
      const key = created.key;
      queryClient.setQueryData(experimentQuery(project, key).queryKey, created);
      void queryClient.invalidateQueries({ queryKey: experimentsQueryKey(project) });
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      toast.success(t`The draft ${key} is created.`);
      onDone();
      await navigate({
        to: "/projects/$project/experiments/$experiment",
        params: { project, experiment: key },
      });
    },
  });

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    if (Object.keys(issues).length === 0) clone.mutate();
  };

  return (
    <DialogForm
      onSubmit={submit}
      onCancel={onDone}
      submitLabel={<Trans>Clone experiment</Trans>}
      pending={clone.isPending}
      refusal={
        clone.error instanceof ApiError && clone.error.violations.length > 0
          ? undefined
          : clone.error
      }
    >
      <p className="text-sm text-muted-foreground">
        <Trans>
          A new draft with everything but its salts, decision and owner: new salts, so no unit keeps
          the assignment it had; you own it. Its rules are not copied.
        </Trans>
      </p>
      <Field
        label={<Trans>Key</Trans>}
        error={checked ? issues.key : undefined}
        refusal={clone.error}
        pointer="/key"
      >
        <Input
          autoComplete="off"
          value={draft.key}
          onChange={(event) => setDraft({ ...draft, key: event.target.value })}
        />
      </Field>
      <Field
        label={<Trans>Name</Trans>}
        hint={<Trans>Left empty, it keeps “{sourceName}”.</Trans>}
        error={checked ? issues.name : undefined}
        refusal={clone.error}
        pointer="/name"
      >
        <Input
          value={draft.name}
          onChange={(event) => setDraft({ ...draft, name: event.target.value })}
        />
      </Field>
    </DialogForm>
  );
}

export type ExperimentOverviewProps = {
  project: string;
  experiment: Experiment;
  nameOf: (member: string) => string;
  editing: boolean;
  onChanged: (saved: Experiment) => void;
};

/**
 * What an experiment is beside its form: both salts, so an analyst can
 * recompute any unit's buckets, its dates, and once stopped the decision
 * with its note, editable while it stays stopped.
 */
export function ExperimentOverview({
  project,
  experiment,
  nameOf,
  editing,
  onChanged,
}: ExperimentOverviewProps) {
  const { i18n, t } = useLingui();
  const [noting, setNoting] = useState(false);
  const write = useExperimentWrite(project, experiment, onChanged);
  const at = (value: string | null) => (value ? formatDateTime(i18n, value) : undefined);
  const decision = experiment.decision;
  const winner = decision?.winner_variation;
  const creator = experiment.created_by ? nameOf(experiment.created_by) : undefined;
  const created = formatDateTime(i18n, experiment.created_at);

  const dates: [string, string | undefined][] = [
    [t`Started`, at(experiment.started_at)],
    [
      t`Expected end`,
      experiment.expected_end_at ? formatDay(i18n, experiment.expected_end_at) : undefined,
    ],
    [t`Stopped`, at(experiment.stopped_at)],
    [t`Archived`, at(experiment.archived_at)],
  ];

  return (
    <section aria-label={t`Overview`} className="flex flex-col gap-3">
      <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-3">
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Inclusion salt</Trans>
          </dt>
          <dd>
            <code className="font-mono">{experiment.inclusion_salt}</code>
          </dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Variation salt</Trans>
          </dt>
          <dd>
            <code className="font-mono">{experiment.variation_salt}</code>
          </dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Created</Trans>
          </dt>
          <dd>
            {creator ? (
              <Trans>
                {created} by {creator}
              </Trans>
            ) : (
              created
            )}
          </dd>
        </div>
        {dates.map(([label, value]) =>
          value ? (
            <div key={label} className="flex flex-col gap-0.5">
              <dt className="text-muted-foreground">{label}</dt>
              <dd>{value}</dd>
            </div>
          ) : null,
        )}
        {decision ? (
          <div className="flex flex-col gap-0.5 sm:col-span-3">
            <dt className="text-muted-foreground">
              <Trans>Decision</Trans>
            </dt>
            <dd className="flex flex-col gap-1">
              <span>
                {decision.outcome === "winner" && winner ? (
                  <Trans>Shipped {winner}: its rules serve it.</Trans>
                ) : (
                  <Trans>Removed: its rules were deleted.</Trans>
                )}
              </span>
              {decision.note ? (
                <span className="whitespace-pre-wrap">{decision.note}</span>
              ) : (
                <span className="text-muted-foreground">
                  <Trans>No note.</Trans>
                </span>
              )}
              {experiment.status === "stopped" ? (
                <span>
                  <Can permission="experiment.edit" whenMissing="hide">
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={editing}
                      onClick={() => {
                        write.reset();
                        setNoting(true);
                      }}
                    >
                      <Trans>Edit note</Trans>
                    </Button>
                  </Can>
                </span>
              ) : null}
            </dd>
          </div>
        ) : null}
      </dl>
      <Dialog open={noting} onOpenChange={setNoting} title={t`Decision note`}>
        {noting ? (
          <NoteForm
            note={decision?.note ?? ""}
            pending={write.isPending}
            refusal={write.error}
            onCancel={() => setNoting(false)}
            onSave={(note) =>
              write.mutate({ action: "note", note }, { onSuccess: () => setNoting(false) })
            }
          />
        ) : null}
      </Dialog>
    </section>
  );
}

function NoteForm({
  note,
  pending,
  refusal,
  onCancel,
  onSave,
}: {
  note: string;
  pending: boolean;
  refusal: unknown;
  onCancel: () => void;
  onSave: (note: string) => void;
}) {
  const { i18n } = useLingui();
  const [text, setText] = useState(note);
  const issue = noteIssue(i18n, text);
  return (
    <DialogForm
      onSubmit={(event) => {
        event.preventDefault();
        if (issue === undefined) onSave(text.trim());
      }}
      onCancel={onCancel}
      submitLabel={<Trans>Save note</Trans>}
      pending={pending}
    >
      <Field label={<Trans>Note</Trans>} hint={<Trans>Empty clears it.</Trans>} error={issue}>
        <Textarea
          rows={4}
          maxLength={maxNoteLength}
          value={text}
          onChange={(event) => setText(event.target.value)}
        />
      </Field>
      <ExperimentRefusal refusal={refusal} />
    </DialogForm>
  );
}
