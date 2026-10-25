import type { Experiment, ExperimentAssignment } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { CheckIcon, CopyIcon } from "lucide-react";
import { useId, useState } from "react";

import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { formatNumber } from "@/i18n/format";
import { unitIdIssue } from "@/lib/experiment-health";
import { experimentAssignmentQuery } from "@/lib/queries";

export type ExperimentAssignmentLookupProps = {
  project: string;
  experiment: Experiment;
};

/**
 * One unit ID in, how the experiment treats it out: its two buckets,
 * whether it is included and the variation it gets, with the ClickHouse
 * query that recomputes them in a warehouse. The server answers from the
 * experiment's salts and allocation in whatever status, so the console
 * carries no evaluator of its own; the lookup reads no event and records
 * nothing.
 */
export function ExperimentAssignmentLookup({
  project,
  experiment,
}: ExperimentAssignmentLookupProps) {
  const { i18n, t } = useLingui();
  const [typed, setTyped] = useState("");
  const [unitId, setUnitId] = useState<string>();
  const [checked, setChecked] = useState(false);
  const unitType = experiment.unit_type;
  const issue = checked ? unitIdIssue(i18n, typed) : undefined;
  const assignment = useQuery({
    ...experimentAssignmentQuery(project, experiment, unitId ?? ""),
    enabled: unitId !== undefined,
  });

  return (
    <section aria-labelledby="experiment-assignment" className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id="experiment-assignment" className="text-lg font-semibold">
          <Trans>Assignment lookup</Trans>
        </h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          <Trans>
            Which variation the experiment gives a unit, from its salts and traffic allocation as
            they are now, and the query that recomputes it in ClickHouse. Looking up records
            nothing.
          </Trans>
        </p>
      </div>
      <form
        noValidate
        className="flex flex-wrap items-start gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          setChecked(true);
          if (unitIdIssue(i18n, typed) === undefined) setUnitId(typed);
        }}
      >
        <Field label={t`Unit ID (${unitType})`} error={issue} className="min-w-0 flex-1 basis-64">
          <Input
            value={typed}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
            onChange={(event) => setTyped(event.target.value)}
          />
        </Field>
        {/* Level with the input, below its label, whether or not an error follows it. */}
        <Button type="submit" variant="outline" className="mt-7">
          <Trans>Look up</Trans>
        </Button>
      </form>
      {unitId === undefined ? null : assignment.isError ? (
        <QueryError error={assignment.error} onRetry={() => void assignment.refetch()} />
      ) : assignment.data === undefined ? (
        <Skeleton className="h-32 w-full" />
      ) : (
        <AssignmentAnswer assignment={assignment.data} />
      )}
    </section>
  );
}

export function AssignmentAnswer({ assignment }: { assignment: ExperimentAssignment }) {
  const { i18n } = useLingui();
  const unit = assignment.unit_id;
  const inclusion = formatNumber(i18n, assignment.inclusion_bucket);
  const allocation = formatNumber(i18n, assignment.traffic_allocation);
  const variationBucket = formatNumber(i18n, assignment.variation_bucket);
  const variation = assignment.variation;

  return (
    <div className="flex flex-col gap-3 rounded-md border p-4 text-sm">
      <p role="status" className="font-medium">
        {assignment.included && variation !== null ? (
          <Trans>
            <code className="font-mono">{unit}</code> gets{" "}
            <code className="font-mono">{variation}</code>.
          </Trans>
        ) : (
          <Trans>
            <code className="font-mono">{unit}</code> is left out of the experiment.
          </Trans>
        )}
      </p>
      <dl className="grid gap-x-8 gap-y-2 sm:grid-cols-2">
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Inclusion bucket</Trans>
          </dt>
          <dd className="tabular-nums">
            {assignment.included ? (
              <Trans>
                {inclusion}, below the traffic allocation of {allocation}: included
              </Trans>
            ) : (
              <Trans>
                {inclusion}, not below the traffic allocation of {allocation}: left out
              </Trans>
            )}
          </dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Variation bucket</Trans>
          </dt>
          <dd className="tabular-nums">
            {assignment.included ? (
              variationBucket
            ) : (
              <Trans>{variationBucket}, the bucket it would take should the allocation grow</Trans>
            )}
          </dd>
        </div>
      </dl>
      <CopyableQuery sql={assignment.sql} />
    </div>
  );
}

/** The query to recompute the assignment in ClickHouse, shown whole and copied in one click. */
function CopyableQuery({ sql }: { sql: string }) {
  const { t } = useLingui();
  const id = useId();
  const [copy, setCopy] = useState<"idle" | "copied" | "refused">("idle");

  const toClipboard = async () => {
    try {
      await navigator.clipboard.writeText(sql);
      setCopy("copied");
    } catch {
      // A browser that refuses the clipboard leaves the query on the
      // screen, selectable as a whole.
      setCopy("refused");
    }
  };

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between gap-2">
        <span id={id} className="text-muted-foreground">
          <Trans>ClickHouse query</Trans>
        </span>
        <Button variant="outline" size="sm" onClick={() => void toClipboard()}>
          <CopyIcon aria-hidden />
          <Trans>Copy query</Trans>
        </Button>
      </div>
      <pre
        aria-labelledby={id}
        tabIndex={0}
        className="max-h-80 overflow-auto rounded-md bg-muted/40 p-3 font-mono text-xs whitespace-pre select-all"
      >
        {sql}
      </pre>
      <p role="status" className="min-h-4 text-xs text-muted-foreground">
        {copy === "copied" ? (
          <span className="inline-flex items-center gap-1 text-primary">
            <CheckIcon aria-hidden className="size-3" />
            <Trans>Copied to the clipboard.</Trans>
          </span>
        ) : copy === "refused" ? (
          t`The browser refused the clipboard. Select the query and copy it by hand.`
        ) : null}
      </p>
    </div>
  );
}
