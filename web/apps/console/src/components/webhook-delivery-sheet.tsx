import type { WebhookDelivery } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { type ReactNode } from "react";

import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Sheet } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { DeliveryStatusBadge } from "@/components/webhook-status";
import { formatDateTime, formatNumber } from "@/i18n/format";
import { type WebhookScope, deliveryQuery } from "@/lib/webhooks";

function Fact({ term, children }: { term: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-xs text-muted-foreground">{term}</dt>
      <dd className="text-sm break-all">{children}</dd>
    </div>
  );
}

/**
 * One delivery beside the log: the event it carries, where it stands and
 * every call made to the receiver, with what the receiver answered. The
 * delivery is the log's own row when the log lists it, and otherwise read
 * by its identifier, so an address naming an older one still opens it.
 */
export function WebhookDeliverySheet({
  scope,
  webhook,
  id,
  listed,
  redeliver,
  onClose,
}: {
  scope: WebhookScope;
  webhook: string;
  id: string;
  /** The delivery as the log lists it, when it does. */
  listed: WebhookDelivery | undefined;
  /** Sends the delivery's event again. */
  redeliver: (delivery: WebhookDelivery) => void;
  onClose: () => void;
}) {
  const { i18n, t } = useLingui();
  const read = useQuery({ ...deliveryQuery(scope, webhook, id), enabled: listed === undefined });
  const delivery = listed ?? read.data;
  return (
    <Sheet
      open
      onOpenChange={(open) => !open && onClose()}
      title={delivery?.event_type ?? t`Delivery`}
    >
      {delivery === undefined ? (
        read.isError ? (
          <QueryError error={read.error} onRetry={() => void read.refetch()} />
        ) : (
          <div className="flex flex-col gap-3" aria-busy>
            <Skeleton className="h-4 w-48" />
            <Skeleton className="h-24 w-full" />
          </div>
        )
      ) : (
        <>
          <div>
            <Button variant="outline" size="sm" onClick={() => redeliver(delivery)}>
              <Trans>Redeliver</Trans>
            </Button>
          </div>
          <dl className="flex flex-col gap-3">
            <Fact term={<Trans>Status</Trans>}>
              <DeliveryStatusBadge status={delivery.status} />
            </Fact>
            <Fact term={<Trans>Event ID</Trans>}>
              <code className="font-mono text-xs">{delivery.event_id}</code>
            </Fact>
            <Fact term={<Trans>Delivery ID</Trans>}>
              <code className="font-mono text-xs">{delivery.id}</code>
            </Fact>
            <Fact term={<Trans>Queued</Trans>}>{formatDateTime(i18n, delivery.created_at)}</Fact>
            {delivery.next_attempt_at ? (
              <Fact term={<Trans>Next attempt</Trans>}>
                {formatDateTime(i18n, delivery.next_attempt_at)}
              </Fact>
            ) : null}
          </dl>
          <section aria-labelledby="attempts" className="flex flex-col gap-2">
            <h3 id="attempts" className="text-sm font-semibold">
              <Trans>Attempts</Trans>
            </h3>
            {delivery.attempts.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                <Trans>No attempt has been made yet.</Trans>
              </p>
            ) : (
              <ol className="flex flex-col gap-3">
                {delivery.attempts.map((attempt) => {
                  const number = attempt.n;
                  const latency = formatNumber(i18n, attempt.latency_ms);
                  return (
                    <li
                      key={attempt.n}
                      aria-label={t`Attempt ${number}`}
                      className="flex flex-col gap-1 rounded-md border p-3 text-sm"
                    >
                      <span className="font-medium">
                        <Trans>Attempt {number}</Trans>
                        {" · "}
                        {attempt.status_code === null ? (
                          <Trans>No response</Trans>
                        ) : (
                          attempt.status_code
                        )}
                      </span>
                      <span className="text-muted-foreground">
                        {formatDateTime(i18n, attempt.at)}
                        {" · "}
                        {t`${latency} ms`}
                      </span>
                      {attempt.error ? (
                        <span className="text-destructive">{attempt.error}</span>
                      ) : null}
                      {attempt.response_snippet ? (
                        <pre className="max-h-40 overflow-auto rounded-sm bg-muted/40 p-2 font-mono text-xs break-all whitespace-pre-wrap">
                          {attempt.response_snippet}
                        </pre>
                      ) : null}
                    </li>
                  );
                })}
              </ol>
            )}
          </section>
        </>
      )}
    </Sheet>
  );
}
