import type { Webhook } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PlusIcon, PowerIcon, Trash2Icon, WebhookIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { DeleteRowDialog, RowAction, withRowActions } from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Table, type TableColumn } from "@/components/ui/table";
import { WebhookEditor } from "@/components/webhook-editor";
import { WebhookLink, useWebhookNavigation } from "@/components/webhook-link";
import { WebhookSecretDialog } from "@/components/webhook-secret-dialog";
import { WebhookStateBadge } from "@/components/webhook-status";
import {
  describeEvents,
  type WebhookScope,
  webhookApi,
  webhooksKey,
  webhooksQuery,
} from "@/lib/webhooks";

/**
 * A scope's webhook subscriptions: what each is sent and where, whether it
 * is enabled, creating one (its secret shown once), enabling a disabled
 * one in a click and deleting one.
 */
export function WebhookList({ scope }: { scope: WebhookScope }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigation = useWebhookNavigation(scope);
  const webhooks = useQuery(webhooksQuery(scope));
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<{ id: string; secret: string }>();
  const [deleting, setDeleting] = useState<string>();
  const { mutate: enable } = useMutation({
    mutationFn: (webhook: Webhook) => webhookApi(scope).enable(webhook.id),
    onSuccess: async (_, webhook) => {
      await queryClient.invalidateQueries({ queryKey: webhooksKey(scope) });
      const { name } = webhook;
      toast.success(t`${name} is enabled.`);
    },
  });

  const columns = useMemo<TableColumn<Webhook>[]>(() => {
    const base: TableColumn<Webhook>[] = [
      {
        id: "name",
        header: t`Name`,
        cell: ({ row }) => (
          <span className="flex flex-col gap-1">
            <WebhookLink
              scope={scope}
              webhook={row.original.id}
              className="font-medium hover:underline"
            >
              {row.original.name}
            </WebhookLink>
            <span>
              <WebhookStateBadge webhook={row.original} />
            </span>
          </span>
        ),
      },
      {
        id: "url",
        header: t`URL`,
        cell: ({ row }) => <code className="font-mono text-xs break-all">{row.original.url}</code>,
      },
      {
        id: "events",
        header: t`Events`,
        cell: ({ row }) => describeEvents(i18n, row.original.events),
      },
      {
        id: "success",
        header: t`Last success`,
        cell: ({ row }) => <Moment value={row.original.last_success_at} />,
      },
      {
        id: "failures",
        header: t`Failures in a row`,
        cell: ({ row }) => row.original.consecutive_failures,
      },
    ];
    return withRowActions(base, true, (webhook) => {
      const { name } = webhook;
      return (
        <>
          {webhook.enabled ? null : (
            <RowAction
              label={webhook.disabled_reason ? t`Re-enable ${name}` : t`Enable ${name}`}
              icon={PowerIcon}
              onClick={() => enable(webhook)}
            />
          )}
          <RowAction
            label={t`Delete ${name}`}
            icon={Trash2Icon}
            onClick={() => setDeleting(webhook.id)}
          />
        </>
      );
    });
  }, [enable, i18n, scope, t]);

  const nameOf = (id: string) => webhooks.data?.find((webhook) => webhook.id === id)?.name ?? "";
  const instance = scope === "instance";

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={instance ? t`Instance webhooks` : t`Webhooks`}
        description={
          instance ? (
            <Trans>
              An instance webhook is sent every audit entry that names no project, signed with its
              secret.
            </Trans>
          ) : (
            <Trans>
              A webhook posts the events you choose to a URL, signed with its secret, retried when
              the receiver fails, and logged.
            </Trans>
          )
        }
        actions={
          <Button onClick={() => setCreating(true)}>
            <PlusIcon aria-hidden />
            <Trans>New webhook</Trans>
          </Button>
        }
      />
      {webhooks.isError ? (
        <QueryError error={webhooks.error} onRetry={() => void webhooks.refetch()} />
      ) : webhooks.data?.length === 0 ? (
        <EmptyState
          icon={<WebhookIcon />}
          title={<Trans>No webhook yet.</Trans>}
          description={
            <Trans>
              Create one to have events posted to a chat channel, an archive or a deployment tool.
            </Trans>
          }
        />
      ) : (
        <Table
          caption={instance ? t`Instance webhooks` : t`Webhooks`}
          hideCaption
          columns={columns}
          data={webhooks.data ?? []}
          loading={webhooks.isPending}
          getRowId={(webhook) => webhook.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New webhook`}>
        <WebhookEditor
          scope={scope}
          layout="dialog"
          onCancel={() => setCreating(false)}
          onSaved={(saved) => {
            setCreating(false);
            if ("secret" in saved) setCreated({ id: saved.id, secret: saved.secret });
          }}
        />
      </Dialog>
      <WebhookSecretDialog
        secret={created?.secret}
        rotated={false}
        onClose={() => {
          // The new subscription's screen is where its deliveries will show.
          if (created) navigation.open(created.id);
          setCreated(undefined);
        }}
      />
      <DeleteRowDialog
        rowKey={deleting}
        onDone={() => setDeleting(undefined)}
        title={(id) => {
          const name = nameOf(id);
          return t`Delete ${name}`;
        }}
        confirmLabel={<Trans>Delete webhook</Trans>}
        remove={(id) => webhookApi(scope).remove(id)}
        list={webhooksKey(scope)}
      >
        {() => (
          <p>
            <Trans>Nothing more is sent under it. This cannot be undone.</Trans>
          </p>
        )}
      </DeleteRowDialog>
    </div>
  );
}
