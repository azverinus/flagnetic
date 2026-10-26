import { type Experiment, updateExperiment } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { CircleAlertIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { ConflictBanner } from "@/components/conflict-banner";
import { ExperimentAlertBanners, ExperimentAlertsSection } from "@/components/experiment-alerts";
import { ExperimentAssignmentLookup } from "@/components/experiment-assignment";
import { ExperimentExposuresSection } from "@/components/experiment-exposures";
import { ExperimentFields } from "@/components/experiment-fields";
import { ExperimentOverview, ExperimentTransitions } from "@/components/experiment-transitions";
import { FormRefusal } from "@/components/ui/form-page";
import { ExperimentStatusBadge } from "@/components/experiment-status-badge";
import { QueryError } from "@/components/query-error";
import { ExperimentDiff } from "@/components/structural-diff-view";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import { useCan, useMissingPermissionReason } from "@/lib/access";
import { ApiError, unwrap } from "@/lib/errors";
import { validateExperimentSearch } from "@/lib/experiment-health";
import {
  experimentFieldPointer,
  experimentIssues,
  experimentPatchOf,
  experimentValues,
  type ExperimentValues,
} from "@/lib/experiments";
import { ifMatch } from "@/lib/http";
import { memberName } from "@/lib/members";
import { madeBy } from "@/lib/project-members";
import { experimentQuery, experimentsQueryKey } from "@/lib/queries";
import { diffExperiments } from "@/lib/structural-diff";
import { useExperimentChoices } from "@/lib/use-experiment-choices";
import { useFormConflict } from "@/lib/use-form-conflict";
import { useKnownMembers } from "@/lib/use-known-members";

export const Route = createFileRoute("/_signed-in/projects/$project/experiments/$experiment")({
  staticData: { crumb: { param: "experiment" } },
  validateSearch: validateExperimentSearch,
  component: ExperimentDetail,
});

function ExperimentDetail() {
  const { project, experiment: key } = Route.useParams();
  const experiment = useQuery(experimentQuery(project, key));
  const choices = useExperimentChoices(project);

  if (
    experiment.isError &&
    experiment.error instanceof ApiError &&
    experiment.error.status === 404
  ) {
    return <ExperimentNotFound project={project} />;
  }
  const failed = experiment.isError
    ? { error: experiment.error, retry: () => void experiment.refetch() }
    : choices.state === "failed"
      ? choices
      : undefined;
  if (failed) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <QueryError error={failed.error} onRetry={failed.retry} />
      </div>
    );
  }
  if (experiment.data === undefined || choices.state !== "ready") {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  // Another experiment's page starts afresh: its form is its own.
  return (
    <ExperimentPage
      key={`${project}/${key}`}
      project={project}
      experiment={experiment.data}
      flags={choices.flags}
      unitTypes={choices.unitTypes}
    />
  );
}

function ExperimentNotFound({ project }: { project: string }) {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Experiment not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>The project has no experiment with this key. It may have been deleted.</Trans>
      </p>
      <Button asChild variant="outline">
        <Link to="/projects/$project/experiments" params={{ project }}>
          <Trans>Go to the experiments</Trans>
        </Link>
      </Button>
    </div>
  );
}

/**
 * One experiment: its transitions, its active alerts, its overview, its
 * exposures per environment, its alerts, the assignment lookup and its
 * form, saved
 * under the version the form was filled from; fields its status locks are shown with the reason, and a
 * save that meets a newer version shows who saved it and what changed.
 */
function ExperimentPage({
  project,
  experiment,
  flags,
  unitTypes,
}: {
  project: string;
  experiment: Experiment;
  flags: Parameters<typeof ExperimentFields>[0]["flags"];
  unitTypes: Parameters<typeof ExperimentFields>[0]["unitTypes"];
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const canEdit = useCan("experiment.edit");
  const missing = useMissingPermissionReason("experiment.edit");
  const { projectMembers, names } = useKnownMembers(project);
  const detailKey = experimentQuery(project, experiment.key).queryKey;
  const conflicts = useFormConflict({
    project,
    kind: "experiment",
    initial: experiment,
    read: () =>
      queryClient.fetchQuery({ ...experimentQuery(project, experiment.key), staleTime: 0 }),
  });
  const { base, conflict } = conflicts;
  const [values, setValues] = useState<ExperimentValues>(() => experimentValues(i18n, experiment));
  const [checked, setChecked] = useState(false);

  const nameOf = (member: string) => (names ? madeBy(i18n, names, member) : member);
  const owners = useMemo(() => {
    const listed = (projectMembers.data ?? [])
      .filter((member) => member.status === "active")
      .map((member) => ({ value: member.member, label: memberName(member) }));
    // The owner stays offered when they no longer hold a grant here, so
    // the form shows who owns the experiment.
    if (!listed.some((member) => member.value === base.owner)) {
      listed.push({
        value: base.owner,
        label: names ? madeBy(i18n, names, base.owner) : base.owner,
      });
    }
    return listed.sort((a, b) => a.label.localeCompare(b.label, i18n.locale));
  }, [projectMembers.data, base.owner, names, i18n]);

  const restart = (from: Experiment) => {
    setValues(experimentValues(i18n, from));
    setChecked(false);
    conflicts.restart(from);
  };

  const save = useMutation({
    mutationFn: ({ sent, at }: { sent: ExperimentValues; at: number }) =>
      unwrap(
        updateExperiment({
          path: { project, experiment: experiment.key },
          body: experimentPatchOf(base, sent),
          headers: ifMatch(at),
        }),
      ),
    meta: { toast: false },
    onSuccess: (saved) => {
      queryClient.setQueryData(detailKey, saved);
      void queryClient.invalidateQueries({ queryKey: experimentsQueryKey(project) });
      restart(saved);
      toast.success(t`The experiment is saved.`);
    },
    onError: (error) => conflicts.meet(error),
  });

  const variations =
    flags.find((flag) => flag.key === values.flag)?.variations.map(({ key }) => key) ??
    (values.flag === base.flag ? base.variations.map(({ variation }) => variation) : []);
  const issues = experimentIssues(i18n, values, { variations, base });
  const dirty = canEdit && Object.keys(experimentPatchOf(base, values)).length > 0;
  const refusal = conflicts.refusal(save.error);
  const shownAtFields =
    refusal instanceof ApiError &&
    refusal.violations.some((violation) => experimentFieldPointer.test(violation.pointer));

  const send = (at: number) => {
    setChecked(true);
    if (issues.any) return;
    conflicts.clear();
    save.mutate({ sent: values, at });
  };

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={base.name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <code className="font-mono text-sm">{base.key}</code>
            <ExperimentStatusBadge status={base.status} />
          </span>
        }
        actions={
          <ExperimentTransitions
            project={project}
            experiment={base}
            unitTypes={unitTypes}
            editing={dirty}
            onChanged={restart}
          />
        }
      />
      <ExperimentAlertBanners project={project} experiment={base} />
      {dirty && base.status !== "archived" ? (
        <p role="status" className="text-sm text-muted-foreground">
          <Trans>Save or discard your edits before changing the experiment's status.</Trans>
        </p>
      ) : null}
      <ExperimentOverview
        project={project}
        experiment={base}
        nameOf={nameOf}
        editing={dirty}
        onChanged={restart}
      />
      <ExperimentExposuresSection
        project={project}
        experiment={base}
        environment={search.environment}
        onEnvironment={(environment) =>
          void navigate({ search: (prev) => ({ ...prev, environment }), replace: true })
        }
      />
      <ExperimentAlertsSection project={project} experiment={base} />
      <ExperimentAssignmentLookup project={project} experiment={base} />
      <h2 className="text-lg font-semibold">
        <Trans>Definition</Trans>
      </h2>
      {canEdit ? null : (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          {missing}
        </p>
      )}
      {conflict ? (
        <ConflictBanner
          subject="form"
          entry={conflict.entry}
          diff={
            <ExperimentDiff changes={diffExperiments(base, conflict.current)} nameOf={nameOf} />
          }
          actions={
            <>
              <Button
                type="button"
                variant="outline"
                disabled={save.isPending}
                onClick={() => {
                  queryClient.setQueryData(detailKey, conflict.current);
                  save.reset();
                  restart(conflict.current);
                }}
              >
                <Trans>Reload and lose my edits</Trans>
              </Button>
              <Button
                type="button"
                disabled={save.isPending}
                onClick={() => send(conflicts.keep(conflict.current))}
              >
                <Trans>Keep my edits</Trans>
              </Button>
            </>
          }
        />
      ) : null}
      <ExperimentFields
        values={values}
        onChange={(next) => {
          // A refusal points at weights by their places, which a new flag
          // or a row added or removed moves; it is dropped rather than
          // shown on a row it was not about.
          if (next.variations.length !== values.variations.length || next.flag !== values.flag) {
            save.reset();
          }
          setValues(next);
        }}
        issues={checked ? issues : undefined}
        refusal={refusal}
        base={base}
        flags={flags}
        unitTypes={unitTypes}
        owners={owners}
        disabled={!canEdit}
      />
      {checked && issues.any ? (
        <p role="alert" className="flex items-start gap-2 text-sm">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <Trans>Fix what is marked above before saving.</Trans>
        </p>
      ) : null}
      {shownAtFields ? null : <FormRefusal refusal={refusal} />}
      <UnsavedChangesBar
        dirty={dirty}
        onDiscard={() => restart(base)}
        onSave={() => send(conflicts.version)}
        pending={save.isPending}
      />
    </div>
  );
}
