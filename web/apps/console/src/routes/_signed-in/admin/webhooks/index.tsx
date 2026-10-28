import { createFileRoute } from "@tanstack/react-router";

import { WebhookList } from "@/components/webhook-list";

export const Route = createFileRoute("/_signed-in/admin/webhooks/")({
  component: InstanceWebhooks,
});

function InstanceWebhooks() {
  return <WebhookList scope="instance" />;
}
