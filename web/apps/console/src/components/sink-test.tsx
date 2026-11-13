import type { SinkSampleKindResult, SinkTestResult } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { CircleAlertIcon, CircleCheckIcon, TriangleAlertIcon } from "lucide-react";

import { eventKindName, failureText, sinkEventKinds } from "@/lib/sinks";

/**
 * What testing a target found: whether it works, why not by class, status
 * and code, and what is wrong that does not stop delivery. The target's own
 * words never reach it.
 */
export function SinkTestOutcome({ result }: { result: SinkTestResult }) {
  const { i18n } = useLingui();
  return (
    <div role="status" className="flex flex-col gap-2 rounded-md border p-3 text-sm">
      {result.ok ? (
        <p className="flex items-start gap-2">
          <CircleCheckIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-primary" />
          <Trans>The target answered and can take a batch.</Trans>
        </p>
      ) : (
        <p className="flex items-start gap-2">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <span>
            <Trans>The test failed:</Trans> {failureText(i18n, result)}
          </span>
        </p>
      )}
      {result.warnings.length > 0 ? (
        <ul className="flex flex-col gap-1">
          {result.warnings.map((warning) => (
            <li key={warning} className="flex items-start gap-2">
              <TriangleAlertIcon
                aria-hidden
                className="mt-0.5 size-4 shrink-0 text-muted-foreground"
              />
              <span>{warning}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

/** What sending the samples found, one line per event kind. */
export function SinkSampleOutcome({ results }: { results: SinkSampleKindResult[] }) {
  const { i18n } = useLingui();
  return (
    <ul className="flex flex-col gap-1 rounded-md border p-3 text-sm">
      {results.map((result) => {
        const known = sinkEventKinds.find((kind) => kind === result.event_kind);
        const name = known ? eventKindName(i18n, known) : result.event_kind;
        return (
          <li key={result.event_kind} className="flex items-start gap-2">
            {result.ok ? (
              <CircleCheckIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-primary" />
            ) : (
              <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
            )}
            <span>
              <span className="font-medium">{name}</span>
              {": "}
              <span>{result.ok ? <Trans>sent</Trans> : failureText(i18n, result)}</span>
            </span>
          </li>
        );
      })}
    </ul>
  );
}
