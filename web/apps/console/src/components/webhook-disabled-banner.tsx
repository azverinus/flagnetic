import { plural } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { TriangleAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { WebhookLink } from "@/components/webhook-link";
import { useCan } from "@/lib/access";
import {
  isPlatformDisabled,
  type WebhookScope,
  webhookApi,
  webhooksKey,
  webhooksQuery,
} from "@/lib/webhooks";

/**
 * The banner above a project's screens for those who manage its webhooks,
 * naming each subscription the platform disabled for failing. Others see
 * nothing, and read nothing: the list is not theirs to ask for.
 */
export function ProjectWebhookBanner({ project }: { project: string }) {
  const can = useCan("webhook.manage");
  return can ? <WebhookDisabledBanner scope={{ project }} /> : null;
}

/**
 * The subscriptions of a scope the platform disabled, each with a way to
 * enable it again, until none is. A subscription a member disabled is no
 * news to them, so it is not listed.
 */
export function WebhookDisabledBanner({ scope }: { scope: WebhookScope }) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const webhooks = useQuery({ ...webhooksQuery(scope), refetchInterval: 60_000 });
  const enable = useMutation({
    mutationFn: (webhook: string) => webhookApi(scope).enable(webhook),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: webhooksKey(scope) });
      toast.success(t`The webhook is enabled.`);
    },
  });
  const disabled = (webhooks.data ?? []).filter(isPlatformDisabled);
  if (disabled.length === 0) return null;
  return (
    <div className="mx-auto w-full max-w-6xl px-6 pt-6">
      <div
        role="status"
        className="flex items-start gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm"
      >
        <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <p className="font-medium">
            {t`${plural(disabled.length, {
              one: "The platform disabled # webhook after its receiver kept failing. It is sent nothing until it is enabled again.",
              other:
                "The platform disabled # webhooks after their receivers kept failing. They are sent nothing until they are enabled again.",
            })}`}
          </p>
          <ul className="flex flex-col gap-2">
            {disabled.map((webhook) => {
              const { name } = webhook;
              return (
                <li key={webhook.id} className="flex flex-wrap items-center gap-3">
                  <WebhookLink
                    scope={scope}
                    webhook={webhook.id}
                    className="font-medium underline-offset-4 hover:underline"
                  >
                    {name}
                  </WebhookLink>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={enable.isPending}
                    onClick={() => enable.mutate(webhook.id)}
                  >
                    <Trans>Enable {name}</Trans>
                  </Button>
                </li>
              );
            })}
          </ul>
        </div>
      </div>
    </div>
  );
}
