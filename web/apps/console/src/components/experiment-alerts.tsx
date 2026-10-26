import { acknowledgeExperimentAlert, type Experiment, type ExperimentAlert } from "@flagnetic/api";
import { plural } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { TriangleAlertIcon } from "lucide-react";
import { type ReactNode, useMemo } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDay, formatNumber } from "@/i18n/format";
import { unwrap } from "@/lib/errors";
import { alertKindName, awaitingAcknowledgement, pValueText } from "@/lib/experiment-alerts";
import {
  auditQueryKey,
  environmentsQuery,
  experimentAlertsQuery,
  experimentsQueryKey,
} from "@/lib/queries";

/** An environment's name by its key; the key itself when the project no longer has it. */
type EnvironmentName = (key: string) => string;

/** The environments' names, read once for both the banners and the history. */
function useEnvironmentName(project: string): EnvironmentName {
  const environments = useQuery(environmentsQuery(project));
  return useMemo(() => {
    const names = new Map((environments.data ?? []).map(({ key, name }) => [key, name]));
    return (key: string) => names.get(key) ?? key;
  }, [environments.data]);
}

/**
 * The banners of an experiment's active alerts nobody has acknowledged,
 * each with its acknowledgement, shown at the top of its page; nothing
 * while there is none. An acknowledged alert stays in the history until
 * it clears.
 */
export function ExperimentAlertBanners({
  project,
  experiment,
}: {
  project: string;
  experiment: Pick<Experiment, "key">;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const alerts = useQuery(experimentAlertsQuery(project, experiment.key));
  const environmentName = useEnvironmentName(project);
  const acknowledge = useMutation({
    mutationFn: (alert: string) =>
      unwrap(acknowledgeExperimentAlert({ path: { project, experiment: experiment.key, alert } })),
    onSuccess: async () => {
      // The list's markers read the active alerts under the experiment's key too.
      await queryClient.invalidateQueries({
        queryKey: [...experimentsQueryKey(project), "detail", experiment.key, "alerts"],
      });
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      toast.success(t`The alert is acknowledged.`);
    },
  });

  // The history below reports a failed read; the banners stay out of the way.
  if (!alerts.data) return null;
  return (
    <AlertBanners
      alerts={alerts.data.items}
      environmentName={environmentName}
      acknowledging={acknowledge.isPending ? acknowledge.variables : undefined}
      onAcknowledge={(alert) => acknowledge.mutate(alert)}
    />
  );
}

export type AlertBannersProps = {
  alerts: readonly ExperimentAlert[];
  environmentName: EnvironmentName;
  /** The alert whose acknowledgement is being sent, if any. */
  acknowledging: string | undefined;
  onAcknowledge: (alert: string) => void;
};

/** The banners of the active alerts nobody has acknowledged, in the order given. */
export function AlertBanners({
  alerts,
  environmentName,
  acknowledging,
  onAcknowledge,
}: AlertBannersProps) {
  const { t } = useLingui();
  const shown = awaitingAcknowledgement(alerts);
  if (shown.length === 0) return null;
  return (
    <ul aria-label={t`Active alerts`} className="flex flex-col gap-2">
      {shown.map((alert) => (
        <li
          key={alert.id}
          className="flex flex-wrap items-start gap-3 rounded-md border border-destructive/50 bg-destructive/5 p-3 text-sm"
        >
          <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <p className="font-medium">
              <AlertTitle alert={alert} environmentName={environmentName} />
            </p>
            <p>
              <AlertFinding alert={alert} />
            </p>
            <p className="text-muted-foreground">
              <Trans>
                Raised <Moment value={alert.raised_at} />. Acknowledging hides it until it clears.
              </Trans>
            </p>
          </div>
          <Can permission="experiment.edit" whenMissing="disable">
            <Button
              variant="outline"
              size="sm"
              disabled={acknowledging !== undefined}
              onClick={() => onAcknowledge(alert.id)}
            >
              <Trans>Acknowledge</Trans>
            </Button>
          </Can>
        </li>
      ))}
    </ul>
  );
}

function AlertTitle({
  alert,
  environmentName,
}: {
  alert: ExperimentAlert;
  environmentName: EnvironmentName;
}) {
  const { i18n } = useLingui();
  const kind = alertKindName(i18n, alert.kind);
  if (alert.environment === null) return <>{kind}</>;
  const environment = environmentName(alert.environment);
  return (
    <Trans>
      {kind} in {environment}
    </Trans>
  );
}

/** What the check found when it raised the alert, from the block of its kind. */
function AlertFinding({ alert }: { alert: ExperimentAlert }): ReactNode {
  const { i18n, t } = useLingui();
  const { sample_ratio, no_exposures, exposure_conflicts, past_expected_end } = alert.details;
  if (sample_ratio) {
    const p = pValueText(i18n, sample_ratio.p_value);
    const n = formatNumber(i18n, sample_ratio.n);
    const split = sample_ratio.variations
      .map(({ variation, observed, expected }) => {
        const seen = formatNumber(i18n, observed);
        const asked = formatNumber(i18n, Math.round(expected));
        return t`${variation} ${seen}, expected ${asked}`;
      })
      .join("; ");
    return t`p-value ${p}; units first exposed: ${n} (${split}).`;
  }
  if (no_exposures) {
    return no_exposures.last_exposure_at === null ? (
      <Trans>
        No exposure since the start, <Moment value={no_exposures.since} />.
      </Trans>
    ) : (
      <Trans>
        No exposure since <Moment value={no_exposures.since} />.
      </Trans>
    );
  }
  if (exposure_conflicts) {
    const units = formatNumber(i18n, exposure_conflicts.first_exposures);
    const threshold = formatNumber(i18n, exposure_conflicts.threshold);
    return t`${plural(exposure_conflicts.conflicts, {
      one: "# conflicting exposure",
      other: "# conflicting exposures",
    })} ${plural(exposure_conflicts.conflicting_units, {
      one: "from # unit",
      other: "from # units",
    })} reached the threshold, ${threshold}; units first exposed: ${units}.`;
  }
  if (past_expected_end) {
    const day = formatDay(i18n, past_expected_end.expected_end_at);
    return t`Expected to end on ${day}. Stop it with a decision, or move its expected end.`;
  }
  return null;
}

/**
 * Every alert an experiment has had, the most recently raised first:
 * where, when it was raised, cleared and acknowledged, and what the check
 * found. An acknowledged alert, hidden from the banners, is still here.
 */
export function ExperimentAlertsSection({
  project,
  experiment,
}: {
  project: string;
  experiment: Pick<Experiment, "key">;
}) {
  const alerts = useQuery(experimentAlertsQuery(project, experiment.key));
  const environmentName = useEnvironmentName(project);
  return (
    <section aria-labelledby="experiment-alerts" className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id="experiment-alerts" className="text-lg font-semibold">
          <Trans>Alerts</Trans>
        </h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          <Trans>
            A running experiment is checked every 10 minutes for a sample-ratio mismatch, a day
            without exposures, conflicting exposures and running past its expected end. An alert
            stays active until its condition clears; one returning raises a new alert.
          </Trans>
        </p>
      </div>
      {alerts.isError ? (
        <QueryError error={alerts.error} onRetry={() => void alerts.refetch()} />
      ) : alerts.data === undefined ? (
        <Skeleton className="h-24 w-full" />
      ) : (
        <AlertHistory alerts={alerts.data.items} environmentName={environmentName} />
      )}
    </section>
  );
}

export function AlertHistory({
  alerts,
  environmentName,
}: {
  alerts: readonly ExperimentAlert[];
  environmentName: EnvironmentName;
}) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<ExperimentAlert>[]>(
    () => [
      {
        id: "kind",
        header: t`Alert`,
        cell: ({ row }) => alertKindName(i18n, row.original.kind),
      },
      {
        id: "environment",
        header: t`Environment`,
        cell: ({ row }) =>
          row.original.environment === null
            ? t`Whole experiment`
            : environmentName(row.original.environment),
      },
      {
        id: "raised_at",
        header: t`Raised`,
        cell: ({ row }) => <Moment value={row.original.raised_at} />,
      },
      {
        id: "cleared_at",
        header: t`Cleared`,
        cell: ({ row }) =>
          row.original.cleared_at === null ? (
            <span className="font-medium text-destructive">{t`Active`}</span>
          ) : (
            <Moment value={row.original.cleared_at} />
          ),
      },
      {
        id: "acknowledged_at",
        header: t`Acknowledged`,
        cell: ({ row }) =>
          row.original.acknowledged_at === null ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            <Moment value={row.original.acknowledged_at} />
          ),
      },
      {
        id: "finding",
        header: t`Found`,
        cell: ({ row }) => <AlertFinding alert={row.original} />,
      },
    ],
    [i18n, t, environmentName],
  );
  return (
    <Table
      caption={t`Alerts`}
      hideCaption
      columns={columns}
      data={[...alerts]}
      getRowId={(alert) => alert.id}
      empty={<Trans>No alert has been raised.</Trans>}
    />
  );
}
