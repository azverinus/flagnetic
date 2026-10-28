import type { Webhook, WebhookDeliveryStatus } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";

import { Badge } from "@/components/ui/badge";
import { deliveryStatus, isPlatformDisabled, type Tone } from "@/lib/webhooks";

const badgeOf = {
  success: "default",
  failure: "destructive",
  pending: "muted",
  muted: "muted",
} as const satisfies Record<Tone, "default" | "destructive" | "muted">;

/** Whether a subscription is sent events, and when the platform stopped it, why. */
export function WebhookStateBadge({ webhook }: { webhook: Webhook }) {
  if (webhook.enabled) {
    return (
      <Badge>
        <Trans>Enabled</Trans>
      </Badge>
    );
  }
  return isPlatformDisabled(webhook) ? (
    <Badge variant="destructive" className="whitespace-nowrap">
      <Trans>Disabled after persistent failure</Trans>
    </Badge>
  ) : (
    <Badge variant="muted">
      <Trans>Disabled</Trans>
    </Badge>
  );
}

/** Where a delivery stands. */
export function DeliveryStatusBadge({ status }: { status: WebhookDeliveryStatus }) {
  const { i18n } = useLingui();
  const { label, tone } = deliveryStatus(i18n, status);
  return <Badge variant={badgeOf[tone]}>{label}</Badge>;
}
