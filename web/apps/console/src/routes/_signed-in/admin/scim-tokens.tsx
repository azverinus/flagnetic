import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { ScimTokensScreen } from "@/components/scim-tokens";

export const Route = createFileRoute("/_signed-in/admin/scim-tokens")({
  staticData: { crumb: msg`SCIM tokens` },
  component: ScimTokensScreen,
});
