import type { Webhook } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRoundIcon, PowerIcon, SendIcon, Trash2Icon, TriangleAlertIcon } from "lucide-react";
import { type ReactNode, useState } from "react";
import { toast } from "sonner";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { WebhookDeliveries } from "@/components/webhook-deliveries";
import { WebhookEditor } from "@/components/webhook-editor";
import { useWebhookNavigation } from "@/components/webhook-link";
import { WebhookSecretDialog } from "@/components/webhook-secret-dialog";
import { WebhookStateBadge } from "@/components/webhook-status";
import { ApiError } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import {
  deliveriesKey,
  describeEvents,
  isPlatformDisabled,
  type WebhookScope,
  type WebhookSearch,
  webhookApi,
  webhookKey,
  webhookQuery,
  webhooksKey,
} from "@/lib/webhooks";

function Fact({ term, children }: { term: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-xs text-muted-foreground">{term}</dt>
      <dd className="text-sm break-all">{children}</dd>
    </div>
  );
}

/**
 * One subscription: its settings and its delivery log in two tabs, with
 * sending a test, rotating its secret, enabling and deleting it. Which tab
 * is open, the log's filters and the delivery open beside it are in the
 * address.
 */
export function WebhookScreen({
  scope,
  webhook: id,
  search,
  onSearch,
}: {
  scope: WebhookScope;
  webhook: string;
  search: WebhookSearch;
  onSearch: (search: WebhookSearch) => void;
}) {
  const webhook = useQuery(webhookQuery(scope, id));
  if (webhook.isError) {
    // An identifier that is no UUID is refused as a bad request: it names
    // no subscription either.
    const missing =
      webhook.error instanceof ApiError &&
      (webhook.error.status === 404 || webhook.error.status === 400);
    return missing ? (
      <WebhookNotFound scope={scope} />
    ) : (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <QueryError error={webhook.error} onRetry={() => void webhook.refetch()} />
      </div>
    );
  }
  if (webhook.data === undefined) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  return <WebhookPage scope={scope} webhook={webhook.data} search={search} onSearch={onSearch} />;
}

function WebhookNotFound({ scope }: { scope: WebhookScope }) {
  const navigation = useWebhookNavigation(scope);
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Webhook not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>It does not exist here, or it was deleted.</Trans>
      </p>
      <Button variant="outline" onClick={navigation.list}>
        <Trans>Go to the webhooks</Trans>
      </Button>
    </div>
  );
}

function WebhookPage({
  scope,
  webhook,
  search,
  onSearch,
}: {
  scope: WebhookScope;
  webhook: Webhook;
  search: WebhookSearch;
  onSearch: (search: WebhookSearch) => void;
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigation = useWebhookNavigation(scope);
  const api = webhookApi(scope);
  const [rotating, setRotating] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [secret, setSecret] = useState<string>();

  /** Puts the subscription a write answered in place of the one read. */
  const remember = (saved: Webhook) => {
    queryClient.setQueryData(webhookQuery(scope, webhook.id).queryKey, saved);
    void queryClient.invalidateQueries({ queryKey: webhooksKey(scope), exact: true });
  };
  const enable = useMutation({
    mutationFn: () => api.enable(webhook.id),
    onSuccess: (saved) => {
      remember(saved);
      toast.success(t`The webhook is enabled.`);
    },
  });
  const test = useMutation({
    mutationFn: () => api.test(webhook.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: deliveriesKey(scope, webhook.id) });
      onSearch({ tab: "deliveries" });
    },
  });
  const rotate = useListMutation(
    () => api.rotateSecret(webhook.id),
    webhookKey(scope, webhook.id),
    () => undefined,
    { secret: true },
  );
  const remove = useMutation({
    mutationFn: () => api.remove(webhook.id),
    meta: { toast: false },
    onSuccess: async () => {
      // Only the list is read again: reading the deleted subscription or its
      // log would answer 404 and show "not found" before the list opens.
      await queryClient.invalidateQueries({ queryKey: webhooksKey(scope), exact: true });
      navigation.list();
    },
  });

  const disabledByPlatform = isPlatformDisabled(webhook);
  const name = webhook.name;
  const tab = search.tab ?? "settings";
  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={webhook.name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <WebhookStateBadge webhook={webhook} />
            <code className="font-mono text-xs break-all">{webhook.url}</code>
          </span>
        }
        actions={
          <>
            {webhook.enabled ? null : (
              <Button variant="outline" disabled={enable.isPending} onClick={() => enable.mutate()}>
                <PowerIcon aria-hidden />
                <Trans>Enable</Trans>
              </Button>
            )}
            <Button
              variant="outline"
              disabled={!webhook.enabled || test.isPending}
              title={webhook.enabled ? undefined : t`A disabled webhook is sent nothing.`}
              onClick={() => test.mutate()}
            >
              <SendIcon aria-hidden />
              <Trans>Send test</Trans>
            </Button>
            <Button variant="outline" onClick={() => setRotating(true)}>
              <KeyRoundIcon aria-hidden />
              <Trans>Rotate secret</Trans>
            </Button>
            <Button variant="outline" onClick={() => setDeleting(true)}>
              <Trash2Icon aria-hidden />
              <Trans>Delete</Trans>
            </Button>
          </>
        }
      />
      {disabledByPlatform ? (
        <div
          role="status"
          className="flex items-start gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm"
        >
          <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <p>
            <Trans>
              The platform disabled this webhook after its receiver kept failing, and dropped the
              deliveries that were waiting. Enable it once the receiver is back; a dropped delivery
              can then be redelivered from the log.
            </Trans>
          </p>
        </div>
      ) : null}
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Fact term={<Trans>Sent</Trans>}>{describeEvents(i18n, webhook.events)}</Fact>
        <Fact term={<Trans>Last success</Trans>}>
          <Moment value={webhook.last_success_at} />
        </Fact>
        <Fact term={<Trans>Failures in a row</Trans>}>{webhook.consecutive_failures}</Fact>
        <Fact term={<Trans>Previous secret</Trans>}>
          {webhook.previous_secret_expires_at ? (
            <Trans>
              Signs until <Moment value={webhook.previous_secret_expires_at} />
            </Trans>
          ) : (
            <Trans>None</Trans>
          )}
        </Fact>
      </dl>
      <Tabs
        label={t`Webhook`}
        value={tab}
        onValueChange={(next) => onSearch(next === "deliveries" ? { tab: "deliveries" } : {})}
        tabs={[
          {
            value: "settings",
            label: t`Settings`,
            content: (
              // A new version starts the form over from what was saved.
              <WebhookEditor
                key={webhook.version}
                scope={scope}
                webhook={webhook}
                layout="page"
                onSaved={(saved) => {
                  remember(saved);
                  const { name: savedName } = saved;
                  toast.success(t`${savedName} is saved.`);
                }}
              />
            ),
          },
          {
            value: "deliveries",
            label: t`Deliveries`,
            content: (
              <WebhookDeliveries
                scope={scope}
                webhook={webhook.id}
                search={{ ...search, tab: "deliveries" }}
                onSearch={onSearch}
              />
            ),
          },
        ]}
      />
      <ConfirmDialog
        open={rotating}
        onOpenChange={(open) => {
          if (!open) {
            rotate.reset();
            setRotating(false);
          }
        }}
        title={t`Rotate the secret of ${name}`}
        confirmLabel={<Trans>Rotate secret</Trans>}
        pending={rotate.isPending}
        refusal={rotate.error}
        onConfirm={() =>
          rotate.mutate(undefined, {
            onSuccess: ({ secret: issued, ...rotated }) => {
              setRotating(false);
              setSecret(issued);
              // The secret lives in the dialog alone: not in the cache, and
              // not in the finished mutation this screen would keep.
              remember(rotated);
              rotate.reset();
            },
          })
        }
      >
        <p>
          <Trans>
            A new secret signs from now on. The one it replaces keeps signing beside it for a day,
            so the receiver can switch to the new secret without refusing an event.
          </Trans>
        </p>
      </ConfirmDialog>
      <WebhookSecretDialog secret={secret} rotated onClose={() => setSecret(undefined)} />
      <ConfirmDialog
        open={deleting}
        onOpenChange={(open) => {
          if (!open) {
            remove.reset();
            setDeleting(false);
          }
        }}
        title={t`Delete ${name}`}
        confirmLabel={<Trans>Delete webhook</Trans>}
        destructive
        pending={remove.isPending}
        refusal={remove.error}
        onConfirm={() => remove.mutate(undefined)}
      >
        <p>
          <Trans>Nothing more is sent under it. This cannot be undone.</Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}
