import type { LinkedIdentity } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import type { QueryKey } from "@tanstack/react-query";
import { UnlinkIcon } from "lucide-react";
import { type ReactNode, useMemo } from "react";
import { toast } from "sonner";

import { AuthProviderKind } from "@/components/auth-provider-badges";
import { RowAction, withRowActions } from "@/components/row-dialogs";
import { Badge } from "@/components/ui/badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDateTime } from "@/i18n/format";
import { useListMutation } from "@/lib/mutations";

export type IdentityListProps = {
  identities: readonly LinkedIdentity[] | undefined;
  /** What the list is, for a screen reader. */
  caption: string;
  /** Shown when the member has linked none. */
  empty: ReactNode;
  /** Opens the unlinking of one identity; none, and no column of actions, while absent. */
  onUnlink?: (identity: LinkedIdentity) => void;
};

/**
 * The identities a member linked at auth providers: the provider, the
 * account it knows them by, and when they linked it and last signed in
 * through it. An identity at a disabled provider is marked, since it is
 * no way in until the provider is enabled again.
 */
export function IdentityList({ identities, caption, empty, onUnlink }: IdentityListProps) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<LinkedIdentity>[]>(() => {
    const base: TableColumn<LinkedIdentity>[] = [
      {
        id: "provider",
        header: t`Provider`,
        cell: ({ row }) => {
          const identity = row.original;
          return (
            <span className="flex flex-col gap-0.5">
              <span className="flex flex-wrap items-center gap-2 font-medium">
                {identity.provider_name}
                {identity.provider_enabled ? null : (
                  <Badge variant="muted">
                    <Trans>Provider disabled</Trans>
                  </Badge>
                )}
              </span>
              <span className="text-xs text-muted-foreground">
                <AuthProviderKind kind={identity.kind} /> ·{" "}
                <code className="font-mono">{identity.provider}</code>
              </span>
            </span>
          );
        },
      },
      {
        id: "subject",
        header: t`Account`,
        cell: ({ row }) => (
          <span className="font-mono text-xs break-all">{row.original.subject}</span>
        ),
      },
      {
        id: "last-sign-in",
        header: t`Last sign-in`,
        cell: ({ row }) =>
          row.original.last_sign_in_at ? (
            <time dateTime={row.original.last_sign_in_at}>
              {formatDateTime(i18n, row.original.last_sign_in_at)}
            </time>
          ) : (
            t`Never`
          ),
      },
      {
        id: "linked",
        header: t`Linked`,
        cell: ({ row }) => (
          <time dateTime={row.original.linked_at}>
            {formatDateTime(i18n, row.original.linked_at)}
          </time>
        ),
      },
    ];
    return withRowActions(base, onUnlink !== undefined, (identity) => {
      const name = identity.provider_name;
      return (
        <RowAction
          label={t`Unlink ${name}`}
          icon={UnlinkIcon}
          onClick={() => onUnlink?.(identity)}
        />
      );
    });
  }, [i18n, onUnlink, t]);

  return (
    <Table
      caption={caption}
      hideCaption
      columns={columns}
      data={identities ?? []}
      loading={identities === undefined}
      empty={empty}
      getRowId={(identity) => identity.id}
    />
  );
}

export type UnlinkIdentityDialogProps = {
  /** The identity being unlinked; none while the dialog is closed. */
  identity: LinkedIdentity | undefined;
  onClose: () => void;
  /** Unlinks the identity, for the member themself or for an instance admin. */
  unlink: (identity: LinkedIdentity) => Promise<unknown>;
  /** The list the unlinking changes, read again once it is done. */
  list: QueryKey;
  /** What unlinking means for whose identity it is. */
  children: (identity: LinkedIdentity) => ReactNode;
};

/**
 * The confirmation of unlinking an identity. A refusal (the last usable
 * way in, the provider an email's domain is enforced to, a provisioned
 * member's identity) is explained here and keeps the dialog open.
 */
export function UnlinkIdentityDialog({
  identity,
  onClose,
  unlink,
  list,
  children,
}: UnlinkIdentityDialogProps) {
  const { t } = useLingui();
  const mutation = useListMutation(
    async (target: LinkedIdentity) => {
      await unlink(target);
      return target;
    },
    list,
    (unlinked) => {
      onClose();
      const unlinkedName = unlinked.provider_name;
      toast.success(t`${unlinkedName} is unlinked.`);
    },
  );
  const name = identity?.provider_name ?? "";
  return (
    <ConfirmDialog
      open={identity !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          mutation.reset();
          onClose();
        }
      }}
      title={t`Unlink ${name}`}
      confirmLabel={<Trans>Unlink</Trans>}
      destructive
      pending={mutation.isPending}
      refusal={mutation.error}
      onConfirm={() => identity && mutation.mutate(identity)}
    >
      {identity ? children(identity) : null}
    </ConfirmDialog>
  );
}
