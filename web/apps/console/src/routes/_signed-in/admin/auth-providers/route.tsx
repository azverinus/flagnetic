import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** The instance's auth providers: the list, and one provider's page under it. */
export const Route = createFileRoute("/_signed-in/admin/auth-providers")({
  staticData: { crumb: msg`Auth providers` },
});
