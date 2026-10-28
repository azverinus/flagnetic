import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** The instance's webhooks: the list, and one subscription under it. */
export const Route = createFileRoute("/_signed-in/admin/webhooks")({
  staticData: { crumb: msg`Webhooks` },
});
