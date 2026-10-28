import type { WebhookDelivery } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EyeIcon, RotateCcwIcon } from "lucide-react";
import { useMemo } from "react";
import { toast } from "sonner";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { RowAction, withRowActions } from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { Table, type TableColumn } from "@/components/ui/table";
import { WebhookDeliverySheet } from "@/components/webhook-delivery-sheet";
import { DeliveryStatusBadge } from "@/components/webhook-status";
import {
  deliveriesKey,
  deliveriesQuery,
  deliveryStatus,
  deliveryStatuses,
  isDeliveryFiltered,
  latestResponse,
  type WebhookScope,
  type WebhookSearch,
  webhookApi,
  webhookEventTypesQuery,
} from "@/lib/webhooks";

/** The select's value for a filter left unset: Radix keeps the empty string for itself. */
const any = "any";

/**
 * One subscription's delivery log: the newest first, filtered by status,
 * event type and day, every delivery opening its attempts beside the log
 * and redeliverable. The filters and the open delivery are in the address.
 */
export function WebhookDeliveries({
  scope,
  webhook,
  search,
  onSearch,
}: {
  scope: WebhookScope;
  webhook: string;
  search: WebhookSearch;
  onSearch: (search: WebhookSearch) => void;
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const { delivery: open, tab: _tab, ...filters } = search;
  const log = useInfiniteQuery(deliveriesQuery(scope, webhook, filters));
  const catalogue = useQuery(webhookEventTypesQuery);
  const deliveries = useMemo(() => log.data?.pages.flatMap((page) => page.items) ?? [], [log.data]);
  const { mutate: redeliver } = useMutation({
    mutationFn: (delivery: WebhookDelivery) => webhookApi(scope).redeliver(webhook, delivery.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: deliveriesKey(scope, webhook) });
      toast.success(t`The event is queued to be sent again.`);
    },
  });

  const filter = (patch: Partial<typeof filters>) => onSearch({ ...search, ...patch });
  const columns = useMemo<TableColumn<WebhookDelivery>[]>(() => {
    const base: TableColumn<WebhookDelivery>[] = [
      {
        id: "queued",
        header: t`Queued`,
        cell: ({ row }) => <Moment value={row.original.created_at} />,
      },
      {
        id: "event",
        header: t`Event`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.event_type}</code>,
      },
      {
        id: "status",
        header: t`Status`,
        cell: ({ row }) => <DeliveryStatusBadge status={row.original.status} />,
      },
      {
        id: "attempts",
        header: t`Attempts`,
        cell: ({ row }) => row.original.attempts.length,
      },
      {
        id: "response",
        header: t`Latest response`,
        cell: ({ row }) => latestResponse(i18n, row.original),
      },
      {
        id: "next",
        header: t`Next attempt`,
        cell: ({ row }) =>
          row.original.next_attempt_at ? <Moment value={row.original.next_attempt_at} /> : "—",
      },
    ];
    return withRowActions(base, true, (delivery) => {
      const type = delivery.event_type;
      return (
        <>
          <RowAction
            label={t`Open the ${type} delivery`}
            icon={EyeIcon}
            onClick={() => onSearch({ ...search, delivery: delivery.id })}
          />
          <RowAction
            label={t`Redeliver the ${type} delivery`}
            icon={RotateCcwIcon}
            onClick={() => redeliver(delivery)}
          />
        </>
      );
    });
  }, [i18n, onSearch, redeliver, search, t]);

  const types = new Set((catalogue.data ?? []).map((info) => info.type));
  if (filters.event_type) types.add(filters.event_type);
  types.add("ping");
  const filtered = isDeliveryFiltered(filters);

  return (
    <div className="flex flex-col gap-4">
      <div
        role="search"
        aria-label={t`Filter the deliveries`}
        className="flex flex-wrap items-end gap-4"
      >
        <Field label={<Trans>Status</Trans>} className="w-40">
          <Select
            value={filters.status ?? any}
            onValueChange={(value) =>
              filter({ status: deliveryStatuses.find((status) => status === value) })
            }
          >
            <SelectItem value={any}>{t`Any status`}</SelectItem>
            {deliveryStatuses.map((status) => (
              <SelectItem key={status} value={status}>
                {deliveryStatus(i18n, status).label}
              </SelectItem>
            ))}
          </Select>
        </Field>
        <Field label={<Trans>Event type</Trans>} className="w-64">
          <Select
            value={filters.event_type ?? any}
            onValueChange={(value) => filter({ event_type: value === any ? undefined : value })}
          >
            <SelectItem value={any}>{t`Any event type`}</SelectItem>
            {[...types].sort().map((type) => (
              <SelectItem key={type} value={type}>
                {type}
              </SelectItem>
            ))}
          </Select>
        </Field>
        <Field label={<Trans>From</Trans>} className="w-40">
          <Input
            type="date"
            value={filters.from ?? ""}
            onChange={(event) => filter({ from: event.target.value || undefined })}
          />
        </Field>
        <Field label={<Trans>To</Trans>} className="w-40">
          <Input
            type="date"
            value={filters.to ?? ""}
            onChange={(event) => filter({ to: event.target.value || undefined })}
          />
        </Field>
        {filtered ? (
          <Button variant="outline" onClick={() => onSearch({ tab: search.tab })}>
            <Trans>Clear the filters</Trans>
          </Button>
        ) : null}
      </div>
      {log.isError ? (
        <QueryError error={log.error} onRetry={() => void log.refetch()} />
      ) : (
        <Table
          caption={t`Deliveries`}
          hideCaption
          columns={columns}
          data={deliveries}
          loading={log.isPending}
          empty={
            filtered ? (
              <Trans>No delivery matches the filters.</Trans>
            ) : (
              <Trans>Nothing has been sent to this webhook yet.</Trans>
            )
          }
          getRowId={(delivery) => delivery.id}
        />
      )}
      {log.hasNextPage ? (
        <div>
          <Button
            variant="outline"
            disabled={log.isFetchingNextPage}
            onClick={() => void log.fetchNextPage()}
          >
            <Trans>Load older deliveries</Trans>
          </Button>
        </div>
      ) : null}
      {open ? (
        <WebhookDeliverySheet
          scope={scope}
          webhook={webhook}
          id={open}
          listed={deliveries.find((delivery) => delivery.id === open)}
          redeliver={(delivery) => redeliver(delivery)}
          onClose={() => onSearch({ ...search, delivery: undefined })}
        />
      ) : null}
    </div>
  );
}
