import type { AuditEntry } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { type ReactNode, useId } from "react";

import { QueryError } from "@/components/query-error";
import { Sheet } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { formatInstant } from "@/i18n/format";
import { actionLabel, actorKindLabel, changeRows, entityKindLabel } from "@/lib/audit";
import { cn } from "@/lib/cn";

export type AuditEntrySheetProps = {
  onClose: () => void;
  /** The entry, once read. */
  entry: AuditEntry | undefined;
  pending?: boolean;
  error?: unknown;
  onRetry?: () => void;
  /** Where the entry happened, such as its environment's name, when it says. */
  place?: { label: string; value: ReactNode };
};

/**
 * One audit entry beside the log: who acted and when, in the member's
 * language and time zone, the action as the console names it with the
 * recorded action beside it, the entity, and its fields before and after.
 */
export function AuditEntrySheet({
  onClose,
  entry,
  pending = false,
  error,
  onRetry,
  place,
}: AuditEntrySheetProps) {
  const { t } = useLingui();
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()} title={t`Audit entry`}>
      {error ? (
        <QueryError error={error} onRetry={onRetry} />
      ) : pending || !entry ? (
        <div className="flex flex-col gap-3" aria-busy>
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-64" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : (
        <EntryDetail entry={entry} place={place} />
      )}
    </Sheet>
  );
}

function EntryDetail({
  entry,
  place,
}: {
  entry: AuditEntry;
  place: AuditEntrySheetProps["place"];
}) {
  const { i18n, t } = useLingui();
  const changeHeading = useId();
  const rows = changeRows(entry.before, entry.after);
  const none = (
    <span className="font-sans italic">
      {t({
        message: "none",
        comment: "An audit entry's value before a creation or after a deletion.",
      })}
    </span>
  );
  const termClassName = "text-xs font-medium text-muted-foreground";
  return (
    <div className="flex flex-col gap-6 text-sm">
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-3">
        <dt className={termClassName}>
          <Trans>Time</Trans>
        </dt>
        <dd>
          <time dateTime={entry.occurred_at} title={entry.occurred_at}>
            {formatInstant(i18n, entry.occurred_at)}
          </time>
        </dd>
        <dt className={termClassName}>
          <Trans>Actor</Trans>
        </dt>
        <dd>
          {entry.actor.name}{" "}
          <span className="text-muted-foreground">({actorKindLabel(i18n, entry.actor.kind)})</span>
        </dd>
        <dt className={termClassName}>
          <Trans>Action</Trans>
        </dt>
        <dd className="flex flex-col">
          <span>{actionLabel(i18n, entry.action)}</span>
          <code className="font-mono text-xs text-muted-foreground">{entry.action}</code>
        </dd>
        <dt className={termClassName}>
          <Trans>Entity</Trans>
        </dt>
        <dd className="flex flex-col">
          <span>
            {entityKindLabel(i18n, entry.entity.kind)}{" "}
            <code className="font-mono text-xs">{entry.entity.key}</code>
          </span>
          <code className="font-mono text-xs break-all text-muted-foreground">
            {entry.entity.id}
          </code>
        </dd>
        {place ? (
          <>
            <dt className={termClassName}>{place.label}</dt>
            <dd>{place.value}</dd>
          </>
        ) : null}
        {entry.request_id ? (
          <>
            <dt className={termClassName}>
              <Trans>Request ID</Trans>
            </dt>
            <dd>
              <code className="font-mono text-xs break-all">{entry.request_id}</code>
            </dd>
          </>
        ) : null}
      </dl>
      <section aria-labelledby={changeHeading} className="flex flex-col gap-2">
        <h3 id={changeHeading} className="font-semibold">
          <Trans>Before and after</Trans>
        </h3>
        {rows.length === 0 ? (
          <p className="text-muted-foreground">
            <Trans>The entry records no values.</Trans>
          </p>
        ) : (
          <div className="relative max-w-full overflow-x-auto">
            <table className="w-full border-collapse text-xs">
              <caption className="sr-only">{t`Values before and after the change`}</caption>
              <thead>
                <tr className="border-b text-left text-muted-foreground">
                  <th scope="col" className="py-1.5 pr-3 font-medium">
                    <Trans>Field</Trans>
                  </th>
                  <th scope="col" className="py-1.5 pr-3 font-medium">
                    <Trans>Before</Trans>
                  </th>
                  <th scope="col" className="py-1.5 font-medium">
                    <Trans>After</Trans>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr
                    key={row.field}
                    className={cn("border-b align-top", !row.changed && "text-muted-foreground")}
                  >
                    <th scope="row" className="py-1.5 pr-3 text-left font-mono font-normal">
                      {row.field}
                      {row.changed ? (
                        <span className="ml-1.5 rounded-sm bg-accent px-1 py-0.5 font-sans text-[0.65rem] text-accent-foreground">
                          <Trans>changed</Trans>
                        </span>
                      ) : null}
                    </th>
                    <td className="py-1.5 pr-3 font-mono break-all">{row.before ?? none}</td>
                    <td className="py-1.5 font-mono break-all">{row.after ?? none}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
