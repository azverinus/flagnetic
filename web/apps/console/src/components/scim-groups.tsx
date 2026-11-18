import type { ScimGroup } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useMemo } from "react";

import { QueryError } from "@/components/query-error";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatNumber } from "@/i18n/format";
import { scimGroupsQuery } from "@/lib/scim";

/**
 * The groups an auth provider's directory pushed through SCIM, with how
 * many of its provisioned members each holds. Read only: the directory owns
 * the groups and who is in them, and the provider's group mappings match
 * them by name.
 */
export function ScimGroups({ provider }: { provider: string }) {
  const { i18n, t } = useLingui();
  const groups = useQuery(scimGroupsQuery(provider));
  const columns = useMemo<TableColumn<ScimGroup>[]>(
    () => [
      {
        id: "name",
        header: t`Group`,
        cell: ({ row }) => <span className="font-medium">{row.original.display_name}</span>,
      },
      {
        id: "external-id",
        header: t`External ID`,
        cell: ({ row }) =>
          row.original.external_id === null ? (
            <span className="text-muted-foreground">{t`None`}</span>
          ) : (
            <code className="font-mono text-xs break-all">{row.original.external_id}</code>
          ),
      },
      {
        id: "members",
        header: t`Members`,
        cell: ({ row }) => formatNumber(i18n, row.original.member_count),
      },
    ],
    [i18n, t],
  );
  return (
    <Card aria-labelledby="scim-groups">
      <CardHeader
        titleId="scim-groups"
        title={<Trans>SCIM groups</Trans>}
        description={
          <Trans>
            The groups the identity provider pushed through SCIM, which its group mappings can name.
            The directory owns them and who is in them, so they are read only here. Pushing groups
            needs a{" "}
            <Link to="/admin/scim-tokens" className="underline">
              SCIM token
            </Link>{" "}
            of this provider.
          </Trans>
        }
      />
      {groups.isError ? (
        <CardContent>
          <QueryError error={groups.error} onRetry={() => void groups.refetch()} />
        </CardContent>
      ) : (
        <CardContent flush>
          <Table
            caption={t`SCIM groups`}
            hideCaption
            columns={columns}
            data={groups.data ?? []}
            loading={groups.isPending}
            empty={<Trans>The identity provider has pushed no group.</Trans>}
            getRowId={(group) => group.id}
          />
        </CardContent>
      )}
    </Card>
  );
}
