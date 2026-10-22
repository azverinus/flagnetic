import type { SdkInventoryItem } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { useMemo } from "react";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Table } from "@/components/ui/table";
import { kindName } from "@/lib/sdk-keys";
import { formatNumber } from "@/i18n/format";
import { sdkInventoryQuery } from "@/lib/queries";

export type SdkInventoryProps = {
  project: string;
  environment: string;
};

/** What a row shows where the SDK sent nothing. */
const none = <span className="text-muted-foreground">—</span>;

/**
 * The SDKs seen using an environment: each with its version, platform,
 * the application version it was serving, the key it presented, when it
 * was first and last seen and the ruleset version it last held. The
 * list is the server's record of requests, a minute behind at most; it
 * does not say who is connected right now.
 */
export function SdkInventory({ project, environment }: SdkInventoryProps) {
  const { i18n, t } = useLingui();
  const inventory = useQuery(sdkInventoryQuery(project, environment));

  const columns = useMemo<ColumnDef<SdkInventoryItem, unknown>[]>(
    () => [
      {
        id: "sdk",
        header: t`SDK`,
        cell: ({ row }) =>
          row.original.sdk_name === "unknown" ? (
            <span className="text-muted-foreground">
              <Trans>Unknown</Trans>
            </span>
          ) : (
            <code className="font-mono text-xs">{row.original.sdk_name}</code>
          ),
      },
      {
        id: "version",
        header: t`Version`,
        cell: ({ row }) => row.original.sdk_version || none,
      },
      { id: "platform", header: t`Platform`, cell: ({ row }) => row.original.platform ?? none },
      {
        id: "app_version",
        header: t`App version`,
        cell: ({ row }) => row.original.app_version ?? none,
      },
      {
        id: "key",
        header: t`Key`,
        cell: ({ row }) => {
          const { name, kind, key_id: keyId } = row.original.sdk_key;
          return (
            <span className="flex flex-col">
              <span>{name}</span>
              <span className="text-xs text-muted-foreground">
                {kindName(i18n, kind)} · <code className="font-mono">{keyId}</code>
              </span>
            </span>
          );
        },
      },
      {
        id: "first_seen",
        header: t`First seen`,
        cell: ({ row }) => <Moment value={row.original.first_seen} />,
      },
      {
        id: "last_seen",
        header: t`Last seen`,
        cell: ({ row }) => <Moment value={row.original.last_seen} />,
      },
      {
        id: "ruleset_version",
        header: t`Last ruleset version`,
        cell: ({ row }) =>
          row.original.last_ruleset_version === null
            ? none
            : formatNumber(i18n, row.original.last_ruleset_version),
      },
    ],
    [i18n, t],
  );

  return (
    <div className="flex flex-col gap-4">
      <p className="max-w-prose text-sm text-muted-foreground">
        <Trans>
          The SDKs that have asked this environment for its ruleset or for values, most recently
          seen first. A row appears within a minute of an SDK’s request; whether an SDK is connected
          right now is not shown.
        </Trans>
      </p>
      {inventory.isError ? (
        <QueryError error={inventory.error} onRetry={() => void inventory.refetch()} />
      ) : (
        <Table
          caption={t`SDKs`}
          hideCaption
          columns={columns}
          data={inventory.data ?? []}
          loading={inventory.isPending}
          empty={
            <Trans>
              No SDK has used this environment yet. SDKs appear here after their first request, made
              with one of its SDK keys.
            </Trans>
          }
        />
      )}
    </div>
  );
}
