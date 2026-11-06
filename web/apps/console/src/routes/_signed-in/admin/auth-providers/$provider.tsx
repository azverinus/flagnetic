import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { AuthProviderScreen } from "@/components/auth-provider-detail";

export const Route = createFileRoute("/_signed-in/admin/auth-providers/$provider")({
  staticData: { crumb: msg`Auth provider` },
  component: AuthProvider,
});

function AuthProvider() {
  const { provider } = Route.useParams();
  return <AuthProviderScreen slug={provider} />;
}
