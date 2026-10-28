import type { ChangeRequest, ScheduleFailure } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { CircleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

import { formatDateTime } from "@/i18n/format";

function Fact({ term, children }: { term: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground">{term}</dt>
      <dd>{children}</dd>
    </div>
  );
}

/**
 * Why the scheduler could not apply the change request at its time. It
 * leaves the state and version as they were, so the change request stays
 * approved; once the scheduler stops, only cancelling it and copying it
 * proposes it again.
 */
export function ScheduleFailureNotice({
  failure,
  state,
}: {
  failure: ScheduleFailure;
  state: ChangeRequest["state"];
}) {
  const { code, message, attempts } = failure;
  return (
    <section
      aria-labelledby="schedule-failure"
      className="flex flex-col gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
    >
      <h2 id="schedule-failure" className="flex items-center gap-2 text-base font-semibold">
        <CircleAlertIcon aria-hidden className="size-4 shrink-0 text-destructive" />
        <Trans>The scheduled application failed</Trans>
      </h2>
      <dl className="grid gap-x-8 gap-y-2 sm:grid-cols-3">
        <Fact term={<Trans>Last attempt</Trans>}>
          <TimeOf at={failure.at} />
        </Fact>
        <Fact term={<Trans>Attempts failed in a row</Trans>}>{attempts}</Fact>
        <Fact term={<Trans>Code</Trans>}>
          <code className="font-mono text-xs">{code}</code>
        </Fact>
        <div className="flex flex-col gap-0.5 sm:col-span-3">
          <dt className="text-muted-foreground">
            <Trans>Reason</Trans>
          </dt>
          <dd className="whitespace-pre-wrap">{message}</dd>
        </div>
      </dl>
      <p>
        {state === "superseded" ? (
          <Trans>
            It was superseded afterwards, so it is not applied. Copy it to propose it again.
          </Trans>
        ) : failure.stopped ? (
          <Trans>
            The scheduler stopped after {attempts} failed attempts and will not try again. Cancel
            this change request and copy it to propose it again.
          </Trans>
        ) : (
          <Trans>
            The scheduler tries again at its next round and stops after repeated failures. To
            propose it again, cancel this change request and copy it.
          </Trans>
        )}
      </p>
    </section>
  );
}

function TimeOf({ at }: { at: string }) {
  const { i18n } = useLingui();
  return <time dateTime={at}>{formatDateTime(i18n, at)}</time>;
}
