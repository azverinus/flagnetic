import {
  createProjectToken,
  type ProjectToken,
  type Role,
  revokeProjectToken,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { PlusIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { QueryError } from "@/components/query-error";
import { TokenCreateForm } from "@/components/token-create-form";
import { useRevocableTokens } from "@/components/token-list";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { RevealSecretDialog } from "@/components/ui/reveal-secret";
import { Table } from "@/components/ui/table";
import { roleName } from "@/lib/access-names";
import { unwrap } from "@/lib/errors";
import { projectTokenScopes, type TokenPolicy } from "@/lib/personal-tokens";
import { madeBy } from "@/lib/project-members";
import { projectTokensQuery } from "@/lib/queries";

export type ProjectTokenListProps = {
  project: string;
  /** Every role, in the server's order; a token holds one of them. */
  roles: readonly Role[];
  /** The instance's bound on a token's expiry, which a new token's expiries follow. */
  policy: TokenPolicy;
  /**
   * The names of the members the console knows, by identifier, for who
   * created each token; none while they are read, when the list waits for
   * them rather than name a creator wrongly.
   */
  names: ReadonlyMap<string, string> | undefined;
};

/**
 * A project's tokens, by their hint, with the role each holds and who
 * created it, creating one (its value shown once) and revoking one.
 */
export function ProjectTokenList({ project, roles, policy, names }: ProjectTokenListProps) {
  const { i18n, t } = useLingui();
  const list = projectTokensQuery(project);
  const tokens = useQuery(list);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<{ value: string }>();
  const kindColumns = useMemo<ColumnDef<ProjectToken, unknown>[]>(
    () => [
      { id: "role", header: t`Role`, cell: ({ row }) => roleName(i18n, row.original.role) },
      {
        id: "creator",
        header: t`Created by`,
        cell: ({ row }) => madeBy(i18n, names ?? new Map(), row.original.created_by),
      },
    ],
    [i18n, names, t],
  );
  const { columns, revokeDialog } = useRevocableTokens(
    tokens.data,
    (token) => unwrap(revokeProjectToken({ path: { project, token: token.id } })),
    list.queryKey,
    kindColumns,
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          <Trans>
            A project token belongs to the project, not to whoever created it: it keeps working when
            they leave. Only a signed-in session creates one.
          </Trans>
        </p>
        <Button onClick={() => setCreating(true)}>
          <PlusIcon aria-hidden />
          <Trans>New project token</Trans>
        </Button>
      </div>
      {tokens.isError ? (
        <QueryError error={tokens.error} onRetry={() => void tokens.refetch()} />
      ) : (
        <Table
          caption={t`Project tokens`}
          hideCaption
          columns={columns}
          data={tokens.data ?? []}
          loading={tokens.isPending || names === undefined}
          empty={<Trans>This project has no token yet.</Trans>}
          getRowId={(token) => token.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New project token`}>
        <TokenCreateForm
          kind="project"
          scopes={projectTokenScopes}
          policy={policy}
          roles={roles}
          create={(body) => unwrap(createProjectToken({ path: { project }, body }))}
          list={list.queryKey}
          onCancel={() => setCreating(false)}
          onCreated={(token) => {
            setCreating(false);
            setCreated(token);
          }}
        />
      </Dialog>
      <RevealSecretDialog
        open={created !== undefined}
        title={t`The new project token`}
        value={created?.value ?? ""}
        kept="hash"
        onClose={() => setCreated(undefined)}
      >
        <Trans>
          Send it as a bearer token in the Authorization header. Keep it where only the pipeline
          that uses it can read it.
        </Trans>
      </RevealSecretDialog>
      {revokeDialog}
    </div>
  );
}
