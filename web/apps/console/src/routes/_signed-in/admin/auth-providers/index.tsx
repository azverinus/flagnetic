import { createFileRoute } from "@tanstack/react-router";

import { AuthProviderList } from "@/components/auth-provider-list";

export const Route = createFileRoute("/_signed-in/admin/auth-providers/")({
  component: AuthProviders,
});

function AuthProviders() {
  return <AuthProviderList />;
}
