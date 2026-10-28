import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { WebhookScreen } from "@/components/webhook-detail";
import { validateWebhookSearch } from "@/lib/webhooks";

export const Route = createFileRoute("/_signed-in/projects/$project/webhooks/$webhook")({
  validateSearch: validateWebhookSearch,
  staticData: { crumb: msg`Webhook` },
  component: ProjectWebhook,
});

function ProjectWebhook() {
  const { project, webhook } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <WebhookScreen
      scope={{ project }}
      webhook={webhook}
      search={search}
      // The log's filters and page replace the address, so Back leaves the
      // screen instead of walking through them.
      onSearch={(next) => void navigate({ search: next, replace: true })}
    />
  );
}
