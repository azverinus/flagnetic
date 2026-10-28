import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { WebhookScreen } from "@/components/webhook-detail";
import { validateWebhookSearch } from "@/lib/webhooks";

export const Route = createFileRoute("/_signed-in/admin/webhooks/$webhook")({
  validateSearch: validateWebhookSearch,
  staticData: { crumb: msg`Webhook` },
  component: InstanceWebhook,
});

function InstanceWebhook() {
  const { webhook } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <WebhookScreen
      scope="instance"
      webhook={webhook}
      search={search}
      onSearch={(next) => void navigate({ search: next, replace: true })}
    />
  );
}
