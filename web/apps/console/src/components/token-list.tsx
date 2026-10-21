import { type ApiToken, type CreatedApiToken, revokeMyToken } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { PlusIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { QueryError } from "@/components/query-error";
import { RowAction, withRowActions } from "@/components/row-dialogs";
import { TokenCreateForm } from "@/components/token-create-form";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { RevealSecretDialog } from "@/components/ui/reveal-secret";
import { Table } from "@/components/ui/table";
import { formatDateTime } from "@/i18n/format";
import { unwrap } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { type TokenPolicy, type TokenState, tokenState } from "@/lib/personal-tokens";
import { myTokensQuery } from "@/lib/queries";
import { useNowPassing } from "@/lib/use-now";

/**
 * The member's personal access tokens, by their hint, with creating one
 * (its value shown once) and revoking one.
 */
export function TokenList({
  instanceAdmin,
  policy,
}: {
  instanceAdmin: boolean;
  /** The instance's bound on a token's expiry, which a new token's expiries follow. */
  policy: TokenPolicy;
}) {
  const { i18n, t } = useLingui();
  const tokens = useQuery(myTokensQuery);
  // A token that expires while the page is open shows so as it does.
  const now = useNowPassing(
    (tokens.data ?? []).filter((token) => !token.revoked_at).map((token) => token.expires_at),
  );
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedApiToken>();
  const [revoking, setRevoking] = useState<ApiToken>();
  const revoke = useListMutation(
    (token: ApiToken) => unwrap(revokeMyToken({ path: { token: token.id } })),
    myTokensQuery.queryKey,
    () => {
      setRevoking(undefined);
      toast.success(t`The token is revoked.`);
    },
  );

  const columns = useMemo<ColumnDef<ApiToken, unknown>[]>(() => {
    const stateName: Record<TokenState, string> = {
      active: t`Active`,
      expired: t`Expired`,
      revoked: t`Revoked`,
    };
    const columns: ColumnDef<ApiToken, unknown>[] = [
      {
        id: "name",
        header: t`Name`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            <span>{row.original.name}</span>
            <code className="font-mono text-xs text-muted-foreground">{row.original.hint}</code>
          </span>
        ),
      },
      {
        id: "scopes",
        header: t`Scopes`,
        cell: ({ row }) => (
          <span className="flex flex-wrap gap-1">
            {row.original.scopes.map((scope) => (
              <code key={scope} className="rounded-sm border px-1.5 py-0.5 font-mono text-xs">
                {scope}
              </code>
            ))}
          </span>
        ),
      },
      {
        id: "expires",
        header: t`Expires`,
        cell: ({ row }) =>
          row.original.expires_at ? formatDateTime(i18n, row.original.expires_at) : t`Never`,
      },
      {
        id: "created",
        header: t`Created`,
        cell: ({ row }) => formatDateTime(i18n, row.original.created_at),
      },
      {
        id: "used",
        header: t`Last used`,
        cell: ({ row }) => {
          const { last_used_at: at, last_used_ip: ip } = row.original;
          if (!at) return t`Never`;
          const when = formatDateTime(i18n, at);
          return ip ? t`${when} from ${ip}` : when;
        },
      },
      {
        id: "state",
        header: t`State`,
        cell: ({ row }) => stateName[tokenState(row.original, now)],
      },
    ];
    return withRowActions(columns, true, (token) => {
      if (token.revoked_at) return null;
      const { name } = token;
      return (
        <RowAction label={t`Revoke ${name}`} icon={Trash2Icon} onClick={() => setRevoking(token)} />
      );
    });
  }, [i18n, now, t]);

  const revokingName = revoking?.name ?? "";
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          <Trans>
            A token lets a script act for you, narrowed to its scopes. Only a signed-in session
            creates or revokes one.
          </Trans>
        </p>
        <Button onClick={() => setCreating(true)}>
          <PlusIcon aria-hidden />
          <Trans>New API token</Trans>
        </Button>
      </div>
      {tokens.isError ? (
        <QueryError error={tokens.error} onRetry={() => void tokens.refetch()} />
      ) : (
        <Table
          caption={t`Your API tokens`}
          hideCaption
          columns={columns}
          data={tokens.data ?? []}
          loading={tokens.isPending}
          empty={<Trans>You have no API token yet.</Trans>}
          getRowId={(token) => token.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New API token`}>
        <TokenCreateForm
          instanceAdmin={instanceAdmin}
          policy={policy}
          onCancel={() => setCreating(false)}
          onCreated={(token) => {
            setCreating(false);
            setCreated(token);
          }}
        />
      </Dialog>
      <RevealSecretDialog
        open={created !== undefined}
        title={t`Your new API token`}
        value={created?.value ?? ""}
        kept="hash"
        onClose={() => setCreated(undefined)}
      >
        <Trans>
          Send it as a bearer token in the Authorization header. Keep it where only what uses it can
          read it.
        </Trans>
      </RevealSecretDialog>
      <ConfirmDialog
        open={revoking !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            revoke.reset();
            setRevoking(undefined);
          }
        }}
        title={t`Revoke ${revokingName}`}
        confirmLabel={<Trans>Revoke token</Trans>}
        destructive
        pending={revoke.isPending}
        refusal={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking)}
      >
        <p>
          <Trans>Whatever uses the token is refused from now on. This cannot be undone.</Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}
