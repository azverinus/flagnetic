import { createFileRoute } from "@tanstack/react-router";

import { WebhookList } from "@/components/webhook-list";

export const Route = createFileRoute("/_signed-in/projects/$project/webhooks/")({
  component: ProjectWebhooks,
});

function ProjectWebhooks() {
  const { project } = Route.useParams();
  return <WebhookList scope={{ project }} />;
}
